package recruitment

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/memory"
	"github.com/scitq/scitq/server/protofilter"
)

type RecruiterKey struct {
	StepID uint32
	Rank   int
}

type RecruiterState struct {
	LastTrigger time.Time
}

type Recruiter struct {
	StepID                uint32
	Rank                  int
	TimeoutSeconds        int
	Protofilter           string
	WorkerConcurrency     int
	WorkerPrefetch        int
	MaximumWorkers        *int
	Rounds                int
	PendingTasks          int
	ActiveWorkers         int
	NeededWorkers         int
	WorkflowID            uint32
	TimeoutPassed         bool // <- new field
	LastTrigger           time.Time
	RemainingUntilTimeout time.Duration
}

func ListActiveRecruiters(db *sql.DB, now time.Time, recruiterTimers map[RecruiterKey]RecruiterState, wfcMem map[uint32]WorkflowCounter) ([]Recruiter, error) {
	const query = `
        SELECT
			r.step_id,
			r.rank,
			r.timeout,
			r.protofilter,
			r.worker_concurrency,
			r.worker_prefetch,
			r.maximum_workers,
			r.rounds,
			wf.maximum_workers,
			wf.workflow_id,
			COUNT(t.task_id) AS pending_tasks,
			COUNT(DISTINCT w.worker_id) AS active_workers
		FROM
			recruiter r
		JOIN
			task t ON t.step_id = r.step_id AND t.status = 'P'
		JOIN
			step s ON s.step_id = r.step_id
		JOIN
			workflow wf ON wf.workflow_id = s.workflow_id
		LEFT JOIN
			worker w ON w.step_id = r.step_id
		GROUP BY
			r.step_id, r.rank, r.timeout, r.protofilter,
			r.worker_concurrency, r.worker_prefetch,
			r.maximum_workers, r.rounds,
			wf.maximum_workers, wf.workflow_id
		HAVING
			COUNT(t.task_id) > 0
			AND
			COUNT(DISTINCT w.worker_id) < CEIL(COUNT(t.task_id) * 1.0 / (r.worker_concurrency * r.rounds))
		ORDER BY
			r.step_id, r.rank
    `

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Recruiter
	var workflow_maximum_workers *int
	for rows.Next() {
		var r Recruiter
		err := rows.Scan(
			&r.StepID,
			&r.Rank,
			&r.TimeoutSeconds,
			&r.Protofilter,
			&r.WorkerConcurrency,
			&r.WorkerPrefetch,
			&r.MaximumWorkers,
			&r.Rounds,
			&workflow_maximum_workers,
			&r.WorkflowID,
			&r.PendingTasks,
			&r.ActiveWorkers,
		)
		if err != nil {
			return nil, err
		}
		neededTotal := (r.PendingTasks + (r.WorkerConcurrency * r.Rounds) - 1) / (r.WorkerConcurrency * r.Rounds)
		if r.MaximumWorkers != nil {
			neededTotal = min(neededTotal, *r.MaximumWorkers)
		}
		r.NeededWorkers = neededTotal - r.ActiveWorkers
		if r.NeededWorkers < 0 {
			r.NeededWorkers = 0
		}

		key := RecruiterKey{StepID: r.StepID, Rank: r.Rank}
		state, seen := recruiterTimers[key]
		if !seen {
			// Start the timeout window now (seconds-based)
			state.LastTrigger = now
			r.TimeoutPassed = false
			r.LastTrigger = state.LastTrigger
			r.RemainingUntilTimeout = time.Duration(r.TimeoutSeconds) * time.Second
			recruiterTimers[key] = state
		} else {
			timeout := time.Duration(r.TimeoutSeconds) * time.Second
			elapsed := now.Sub(state.LastTrigger)
			r.TimeoutPassed = elapsed >= timeout
			r.LastTrigger = state.LastTrigger
			if !r.TimeoutPassed {
				r.RemainingUntilTimeout = timeout - elapsed
			} else {
				r.RemainingUntilTimeout = 0
			}
		}

		// Update the workflow counter memory
		wfc, ok := wfcMem[r.WorkflowID]
		if !ok {
			wfcMem[r.WorkflowID] = WorkflowCounter{
				Counter: 0,
				Maximum: workflow_maximum_workers,
			}
		} else {
			wfcMem[r.WorkflowID] = WorkflowCounter{
				Counter: wfc.Counter,
				Maximum: workflow_maximum_workers,
			}
		}

		results = append(results, r)
	}

	return results, rows.Err()
}

type RecruiterFlavorRegion struct {
	FlavorID uint32
	RegionID uint32
	Cpu      int32
	Memory   float32
}

func FetchRecruiterFlavorRegions(db *sql.DB, recruiters []Recruiter) (map[RecruiterKey][]RecruiterFlavorRegion, map[uint32]RegionInfo, error) {
	type recruiterSQLPart struct {
		Key       RecruiterKey
		Condition string
		Args      []interface{}
	}

	var parts []recruiterSQLPart

	for _, r := range recruiters {
		conditions, err := protofilter.ParseProtofilter(r.Protofilter)
		if err != nil {
			return nil, nil, fmt.Errorf("failed parsing protofilter for step_id %d, rank %d: %w", r.StepID, r.Rank, err)
		}

		parts = append(parts, recruiterSQLPart{
			Key:       RecruiterKey{StepID: r.StepID, Rank: r.Rank},
			Condition: strings.Join(conditions, " AND "),
		})
	}

	var unionQueries []string
	for _, p := range parts {
		unionQueries = append(unionQueries, fmt.Sprintf(`
			SELECT 
				%[1]d AS step_id, %[2]d AS rank,
				f.flavor_id,
				r.region_id,
				r.region_name,
				p.provider_name||'.'||p.config_name as provider,
				p.provider_id,
				f.cpu,
				f.mem,
				fr.cost
			FROM flavor f
			JOIN flavor_region fr ON f.flavor_id = fr.flavor_id
			JOIN region r ON fr.region_id = r.region_id
			JOIN provider p ON p.provider_id = f.provider_id
            WHERE %s
        `, p.Key.StepID, p.Key.Rank, p.Condition))
		//log.Printf("Recruiter SQL part for step_id %d, rank %d: %s", p.Key.StepID, p.Key.Rank, unionQueries[len(unionQueries)-1])
	}

	finalQuery := strings.Join(unionQueries, " UNION ALL ") + " ORDER BY cost"

	rows, err := db.Query(finalQuery)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	mapping := make(map[RecruiterKey][]RecruiterFlavorRegion)
	regionInfoMap := make(map[uint32]RegionInfo)

	for rows.Next() {
		var stepID, flavorID, regionID, providerID uint32
		var rank int
		var cpu int32
		var mem float32
		var cost float32
		var regionName string
		var provider string
		if err := rows.Scan(&stepID, &rank, &flavorID, &regionID, &regionName, &provider, &providerID, &cpu, &mem, &cost); err != nil {
			return nil, nil, err
		}
		key := RecruiterKey{StepID: stepID, Rank: rank}
		mapping[key] = append(mapping[key], RecruiterFlavorRegion{
			FlavorID: flavorID,
			RegionID: regionID,
		})
		// Store region info if not already known
		if _, exists := regionInfoMap[regionID]; !exists {
			regionInfoMap[regionID] = RegionInfo{
				Name:       regionName,
				Provider:   provider,
				ProviderID: providerID,
			}
		}
	}

	return mapping, regionInfoMap, rows.Err()
}

type RecyclableWorker struct {
	WorkerID    uint32
	FlavorID    uint32
	RegionID    uint32
	StepID      *uint32
	Concurrency int
	Running     int
}

func FindRecyclableWorkers(
	db *sql.DB,
	allAllowedFlavorIDs []uint32,
	allAllowedRegionIDs []uint32,
) ([]RecyclableWorker, error) {
	if len(allAllowedFlavorIDs) == 0 {
		log.Printf("No compatible flavors available to recycle")
		return nil, nil
	}
	if len(allAllowedRegionIDs) == 0 {
		log.Printf("No available region found to recycle")
		return nil, nil
	}

	const query = `
		SELECT
			w.worker_id,
			w.flavor_id,
			w.region_id,
			w.step_id,
			w.concurrency,
			COUNT(t.task_id) FILTER (WHERE t.status = 'R') AS running_tasks
		FROM
			worker w
		LEFT JOIN
			task t ON t.worker_id = w.worker_id
		WHERE
			w.flavor_id = ANY($1::int[])
			AND w.region_id = ANY($2::int[])
		GROUP BY
			w.worker_id, w.flavor_id, w.region_id, w.concurrency, w.step_id
		HAVING
			COUNT(t.task_id) FILTER (WHERE t.status = 'R') < w.concurrency
			AND COUNT(t.task_id) FILTER (WHERE t.status IN ('P', 'A')) = 0
    `
	log.Printf("Trying to find with Flavors %v| Regions %v", allAllowedFlavorIDs, allAllowedRegionIDs)
	rows, err := db.Query(query, pq.Array(allAllowedFlavorIDs), pq.Array(allAllowedRegionIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []RecyclableWorker
	for rows.Next() {
		var w RecyclableWorker
		var stepIDproxy sql.NullInt32
		if err := rows.Scan(
			&w.WorkerID,
			&w.FlavorID,
			&w.RegionID,
			&stepIDproxy,
			&w.Concurrency,
			&w.Running,
		); err != nil {
			return nil, err
		}
		if stepIDproxy.Valid {
			s := uint32(stepIDproxy.Int32)
			w.StepID = &s
		}
		workers = append(workers, w)
	}

	return workers, rows.Err()
}

func containsInt(slice []uint32, val uint32) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// recycle workers and update weight memory if needed
func recycleWorkers(
	db *sql.DB,
	recyclableWorkers []RecyclableWorker, // <- directly what FindRecyclableWorkers returns
	workerIDs []uint32,
	stepID uint32,
	newConcurrency int,
	prefetch int,
	allowedFlavorIDs []uint32,
	weightMemory *sync.Map,
) int {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return 0
	}

	ids := make([]string, 0, len(workerIDs))
	tempWeightUpdates := make(map[uint32]*sync.Map) // worker_id → *sync.Map[task_id]float64

	recyclableMap := make(map[uint32]RecyclableWorker)
	for _, w := range recyclableWorkers {
		recyclableMap[w.WorkerID] = w
	}

	for _, id := range workerIDs {
		w, ok := recyclableMap[id]
		if !ok {
			log.Printf("⚠️ Worker %d not found in recyclable list", id)
			continue
		}

		if !containsInt(allowedFlavorIDs, w.FlavorID) {
			log.Printf("⚠️ Worker %d has an incompatible flavor_id %d", id, w.FlavorID)
			continue
		}

		ids = append(ids, fmt.Sprintf("%d", id))

		if w.Concurrency != newConcurrency {
			scale := float64(newConcurrency) / float64(w.Concurrency)

			var currentMap *sync.Map
			if val, ok := weightMemory.Load(id); ok {
				currentMap = val.(*sync.Map)
				newMap := &sync.Map{}
				currentMap.Range(func(key, value any) bool {
					tid := key.(uint32)
					weight := value.(float64)
					newMap.Store(tid, weight*scale)
					return true
				})
				tempWeightUpdates[id] = newMap
			} else {
				newMap := &sync.Map{}
				rows, err := tx.Query(`
                    SELECT task_id
                    FROM task
                    WHERE worker_id = $1
                    AND status IN ('A','C','R','D','U')`, id)
				if err != nil {
					log.Printf("⚠️ Failed to list tasks for worker %d: %v", id, err)
					continue
				}
				for rows.Next() {
					var tid int
					if err := rows.Scan(&tid); err == nil {
						newMap.Store(tid, scale)
					}
				}
				rows.Close()
				tempWeightUpdates[id] = newMap
			}
		}
	}

	if len(ids) == 0 {
		log.Printf("⚠️ No compatible recyclable workers were found.")
		tx.Rollback()
		return 0
	}

	idList := fmt.Sprintf("(%s)", strings.Join(ids, ","))

	q := fmt.Sprintf(`
        UPDATE worker
        SET step_id = $1, concurrency = $2, prefetch = $3
        WHERE worker_id IN %s`, idList)

	result, err := tx.Exec(q, stepID, newConcurrency, prefetch)
	if err != nil {
		log.Printf("⚠️ Failed to update workers: %v", err)
		tx.Rollback()
		return 0
	}

	affected, err := result.RowsAffected()
	if err != nil {
		log.Printf("⚠️ Failed to get rows affected: %v", err)
		tx.Rollback()
		return 0
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Commit failed: %v", err)
		return 0
	}

	for wid, m := range tempWeightUpdates {
		weightMemory.Store(wid, m)
	}

	return int(affected)
}

func selectWorkersForRecruiter(
	recyclable []RecyclableWorker,
	flavorIDs map[uint32]struct{},
	regionIDs map[uint32]struct{},
	needed int,
	recruiterStepID uint32,
) []uint32 {
	selected := make([]uint32, 0, needed)
	for _, w := range recyclable {
		if _, ok := flavorIDs[w.FlavorID]; !ok {
			continue
		}
		if _, ok := regionIDs[w.RegionID]; !ok {
			continue
		}
		if w.StepID != nil && *w.StepID == recruiterStepID {
			continue
		}
		selected = append(selected, w.WorkerID)
		if len(selected) >= needed {
			break
		}
	}
	return selected
}

type WorkerCreator interface {
	CreateWorker(ctx context.Context, req *pb.WorkerRequest) (*pb.WorkerIds, error)
}

type RegionInfo struct {
	Name       string
	Provider   string
	ProviderID uint32
}

func deployWorkers(
	ctx context.Context,
	qm *QuotaManager,
	creator WorkerCreator,
	stepID uint32,
	concurrency int,
	prefetch int,
	flavorRegionList []RecruiterFlavorRegion,
	regionInfoMap map[uint32]RegionInfo,
	howMany int,
	workflowCounterMemory *WorkflowCounter,
) (int, error) {
	if howMany <= 0 {
		return 0, nil
	}

	deployed := 0

	for i := 0; i < howMany; i++ {
		found := false
		var selected RecruiterFlavorRegion
		var regionInfo RegionInfo

		for _, fr := range flavorRegionList {
			ri, ok := regionInfoMap[fr.RegionID]
			if !ok {
				log.Printf("⚠️ Unknown region_id %d", fr.RegionID)
				continue
			}

			if qm.CanLaunch(ri.Name, ri.Provider, fr.Cpu, fr.Memory) {
				selected = fr
				regionInfo = ri
				found = true
				break
			} else {
				log.Printf("⚠️ Quota exhausted for flavor %d region %d", fr.FlavorID, fr.RegionID)
			}
		}

		if !found {
			log.Printf("⚠️ Quota exhausted or no flavor/region available for step %d", stepID)
			return deployed, nil
		}

		if workflowCounterMemory.Maximum != nil && workflowCounterMemory.Counter >= *workflowCounterMemory.Maximum {
			log.Printf("⚠️ Workflow has reached maximum workers for step %d, giving up", stepID)
			return deployed, nil
		}

		_, err := creator.CreateWorker(ctx, &pb.WorkerRequest{
			FlavorId:    selected.FlavorID,
			ProviderId:  regionInfo.ProviderID,
			RegionId:    selected.RegionID,
			StepId:      &stepID,
			Number:      1,
			Concurrency: uint32(concurrency),
			Prefetch:    uint32(prefetch),
		})
		if err != nil {
			log.Printf("⚠️ Failed to create worker (flavor %d region %d): %v", selected.FlavorID, selected.RegionID, err)
			return deployed, err
		}

		log.Printf("✅ Deployed worker: step=%d flavor=%d region=%d provider=%s", stepID, selected.FlavorID, selected.RegionID, regionInfo.Provider)
		workflowCounterMemory.Counter++

		qm.RegisterLaunch(regionInfo.Name, regionInfo.Provider, int32(concurrency), float32(prefetch))

		deployed++
	}

	return deployed, nil
}

func RecruiterCycle(
	ctx context.Context,
	db *sql.DB,
	qm *QuotaManager,
	creator WorkerCreator,
	recruiterTimers map[RecruiterKey]RecruiterState,
	weightMemory *sync.Map,
	workflowCounterMemory map[uint32]WorkflowCounter,
	now time.Time,
) error {
	recruiters, err := ListActiveRecruiters(db, now, recruiterTimers, workflowCounterMemory)
	if err != nil {
		return fmt.Errorf("failed to list active recruiters: %w", err)
	}

	if len(recruiters) == 0 {
		log.Println("ℹ️ No active recruiters")
		return nil
	}

	recruiterFlavorRegionMap, regionInfoMap, err := FetchRecruiterFlavorRegions(db, recruiters)
	if err != nil {
		return fmt.Errorf("failed to fetch flavor/region info: %w", err)
	}

	// Aggregate all allowed flavor_ids and region_ids across all recruiters
	allFlavorIDs := make(map[uint32]struct{})
	allRegionIDs := make(map[uint32]struct{})
	for _, frList := range recruiterFlavorRegionMap {
		for _, fr := range frList {
			allFlavorIDs[fr.FlavorID] = struct{}{}
			allRegionIDs[fr.RegionID] = struct{}{}
		}
	}

	recyclableWorkers, err := FindRecyclableWorkers(db, keys(allFlavorIDs), keys(allRegionIDs))
	if err != nil {
		return fmt.Errorf("failed to find recyclable workers: %w", err)
	}

	hasRecyclableWorkers := len(recyclableWorkers) > 0
	if !hasRecyclableWorkers {
		log.Printf("No recyclable workers")
	}

	recyclableMap := make(map[uint32]RecyclableWorker)
	for _, w := range recyclableWorkers {
		recyclableMap[w.WorkerID] = w
	}

	for _, recruiter := range recruiters {
		key := RecruiterKey{StepID: recruiter.StepID, Rank: recruiter.Rank}
		flavorRegionList := recruiterFlavorRegionMap[key]

		if len(flavorRegionList) == 0 {
			log.Printf("⚠️ No flavor/region candidates for recruiter step=%d rank=%d", recruiter.StepID, recruiter.Rank)
			continue
		}

		// Enforce workflow-level maximum early
		wfc := workflowCounterMemory[recruiter.WorkflowID]
		if wfc.Maximum != nil && wfc.Counter >= *wfc.Maximum {
			log.Printf("⚠️ Workflow %d has reached maximum workers (%d), skipping recruiter step=%d rank=%d",
				recruiter.WorkflowID, *wfc.Maximum, recruiter.StepID, recruiter.Rank)
			continue
		}

		// Build recruiter-specific allowed flavor/region sets
		recruiterFlavorIDs := make(map[uint32]struct{})
		recruiterRegionIDs := make(map[uint32]struct{})
		for _, fr := range flavorRegionList {
			recruiterFlavorIDs[fr.FlavorID] = struct{}{}
			recruiterRegionIDs[fr.RegionID] = struct{}{}
		}

		needed := recruiter.NeededWorkers
		if needed <= 0 {
			log.Printf("No recruitment needed.")
			continue
		}

		if hasRecyclableWorkers {
			// Try recycling first
			selectedWorkerIDs := selectWorkersForRecruiter(recyclableWorkers, recruiterFlavorIDs, recruiterRegionIDs, needed, recruiter.StepID)
			if len(selectedWorkerIDs) > 0 {
				affected := recycleWorkers(
					db,
					recyclableWorkers,
					selectedWorkerIDs,
					recruiter.StepID,
					recruiter.WorkerConcurrency,
					recruiter.WorkerPrefetch,
					keys(recruiterFlavorIDs),
					weightMemory,
				)
				needed -= affected
				log.Printf("♻️ Recycled %d workers for step=%d (still need %d)", affected, recruiter.StepID, needed)
			} else {
				log.Printf("No recyclable workers compatible with recruiter %d for step %d", recruiter.Rank, recruiter.StepID)
			}
		}

		if needed <= 0 {
			log.Printf("No recruitment needed anymore.")
			continue
		}

		// Verbose timeout diagnostics (seconds-based)
		if !recruiter.TimeoutPassed {
			log.Printf("⏳ Recruiter step=%d rank=%d waiting %s before cloud deploy (timeout=%ds since %s)",
				recruiter.StepID,
				recruiter.Rank,
				recruiter.RemainingUntilTimeout.Truncate(time.Second),
				recruiter.TimeoutSeconds,
				recruiter.LastTrigger.Format(time.RFC3339))
			continue
		}

		// Try deploying if still needed
		log.Printf("⏱️ Timeout passed for step=%d rank=%d; attempting cloud deploy (need=%d)", recruiter.StepID, recruiter.Rank, needed)

		deployed, err := deployWorkers(
			ctx,
			qm,
			creator,
			recruiter.StepID,
			recruiter.WorkerConcurrency,
			recruiter.WorkerPrefetch,
			flavorRegionList,
			regionInfoMap,
			needed,
			&wfc,
		)

		// Update the workflow counter memory
		workflowCounterMemory[recruiter.WorkflowID] = wfc

		if err != nil {
			log.Printf("⚠️ Failed to deploy workers for step %d: %v", recruiter.StepID, err)
			continue
		}
		log.Printf("🚀 Deployed %d workers for step=%d", deployed, recruiter.StepID)
		recruiterTimers[key] = RecruiterState{LastTrigger: now}
	}

	return nil
}

func keys(m map[uint32]struct{}) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type WorkflowCounter struct {
	Counter int
	Maximum *int
}

func AdjustWorkflowCounters(db *sql.DB, workflowCounterMemory map[uint32]WorkflowCounter) error {
	// Step 1: Load live worker counts per workflow
	log.Printf("ℹ️ Adjusting workflow counters")
	rows, err := db.Query(`
        SELECT
            wf.workflow_id,
            COUNT(w.worker_id) AS worker_count
        FROM
			workflow wf
		LEFT JOIN
            step s ON wf.workflow_id = s.workflow_id
		LEFT JOIN
            worker w ON w.step_id = s.step_id
        GROUP BY
            wf.workflow_id
    `)
	if err != nil {
		return fmt.Errorf("failed to query live workers per workflow: %w", err)
	}
	defer rows.Close()

	// Step 2: Scan and adjust
	for rows.Next() {
		var workflowID uint32
		var liveCount int
		if err := rows.Scan(&workflowID, &liveCount); err != nil {
			return fmt.Errorf("failed to scan workflow worker count: %w", err)
		}

		mem, exists := workflowCounterMemory[workflowID]
		if !exists {
			// Initialize memory from live state; Maximum will be filled/updated during ListActiveRecruiters
			workflowCounterMemory[workflowID] = WorkflowCounter{Counter: liveCount, Maximum: nil}
			log.Printf("ℹ️ Initialized workflow %d counter from live state: %d", workflowID, liveCount)
			continue
		}

		if liveCount != mem.Counter {
			log.Printf("ℹ️ Syncing workflow %d counter from %d to live %d", workflowID, mem.Counter, liveCount)
			mem.Counter = liveCount
			workflowCounterMemory[workflowID] = mem
		}
	}

	return nil
}

// StartRecruitmentLoop runs the recruitment loop every interval duration
func StartRecruiterLoop(
	ctx context.Context,
	db *sql.DB,
	qm *QuotaManager,
	creator WorkerCreator,
	recruiterInterval int,
	weightMemory *sync.Map,
) {
	go func() {
		ticker := time.NewTicker(time.Duration(recruiterInterval) * time.Second)
		defer func() {
			ticker.Stop()
			log.Println("✅ Recruiter loop fully stopped")
		}()

		var workflowCounterMemory map[uint32]WorkflowCounter
		err := memory.LoadMemory(ctx, db, "workflow_counters", &workflowCounterMemory)
		if err != nil {
			log.Printf("ℹ️ No existing workflow_counters memory found, starting fresh")
			workflowCounterMemory = make(map[uint32]WorkflowCounter)
		}

		recruiterTimers := make(map[RecruiterKey]RecruiterState)

		for {
			select {
			case <-ctx.Done():
				log.Println("🛑 Recruiter loop stopped")
				return

			case now := <-ticker.C:
				err := AdjustWorkflowCounters(db, workflowCounterMemory)
				if err != nil {
					log.Printf("⚠️ Could not adjust workflow counters: %v", err)
				}

				err = RecruiterCycle(ctx, db, qm, creator, recruiterTimers, weightMemory, workflowCounterMemory, now)
				if err != nil {
					log.Printf("⚠️ Recruiter cycle failed: %v", err)
				}
				err = memory.SaveMemory(ctx, db, "workflow_counters", workflowCounterMemory)
				if err != nil {
					log.Printf("⚠️ Could not save workflow recruitment memory: %v", err)
				}
				err = memory.SaveWeightMemory(ctx, db, "weight_memory", weightMemory)
				if err != nil {
					log.Printf("⚠️ Could not save partial recycling memory: %v", err)
				}

			}
		}
	}()
}
