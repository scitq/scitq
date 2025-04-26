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

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/gmtsciencedev/scitq2/server/memory"
	"github.com/gmtsciencedev/scitq2/server/protofilter"
)

type RecruiterKey struct {
	StepID int
	Rank   int
}

type RecruiterState struct {
	LastTrigger time.Time
}

type Recruiter struct {
	StepID            int
	Rank              int
	TimeoutSeconds    int
	Protofilter       string
	WorkerConcurrency int
	WorkerPrefetch    int
	MaximumWorkers    *int
	Rounds            int
	PendingTasks      int
	ActiveWorkers     int
	NeededWorkers     int
	WorkflowID        int
	TimeoutPassed     bool // <- new field
}

func ListActiveRecruiters(db *sql.DB, now time.Time, recruiterTimers map[RecruiterKey]RecruiterState, wfcMem map[int]WorkflowCounter) ([]Recruiter, error) {
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
            COUNT(w.worker_id) AS active_workers
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
            COUNT(t.task_id) > 0 AND
            COUNT(w.worker_id) < CEIL(COUNT(t.task_id) * 1.0 / (MAX(r.worker_concurrency) * MAX(r.rounds)))
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
			// First time seeing this recruiter, but don't set LastTrigger yet.
			r.TimeoutPassed = true
		} else {
			timeout := time.Duration(r.TimeoutSeconds) * time.Second
			r.TimeoutPassed = now.Sub(state.LastTrigger) >= timeout
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
	FlavorID int
	RegionID int
}

func FetchRecruiterFlavorRegions(db *sql.DB, recruiters []Recruiter) (map[RecruiterKey][]RecruiterFlavorRegion, map[int]RegionInfo, error) {
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
				p.provider_id
			FROM flavor f
			JOIN flavor_region fr ON f.flavor_id = fr.flavor_id
			JOIN region r ON fr.region_id = r.region_id
			JOIN provider p ON p.provider_id = f.provider_id
            WHERE %s
			ORDER BY fr.cost
        `, p.Key.StepID, p.Key.Rank, p.Condition))
		log.Printf("Recruiter SQL part for step_id %d, rank %d: %s", p.Key.StepID, p.Key.Rank, unionQueries[len(unionQueries)-1])
	}

	finalQuery := strings.Join(unionQueries, " UNION ALL ")

	rows, err := db.Query(finalQuery)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	mapping := make(map[RecruiterKey][]RecruiterFlavorRegion)
	regionInfoMap := make(map[int]RegionInfo)

	for rows.Next() {
		var stepID, rank, flavorID, regionID, providerID int
		var regionName string
		var provider string
		if err := rows.Scan(&stepID, &rank, &flavorID, &regionID, &regionName, &provider, &providerID); err != nil {
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
	WorkerID    int
	FlavorID    int
	RegionID    int
	Concurrency int
	Running     int
}

func FindRecyclableWorkers(
	db *sql.DB,
	allAllowedFlavorIDs []int,
	allAllowedRegionIDs []int,
) ([]RecyclableWorker, error) {
	if len(allAllowedFlavorIDs) == 0 || len(allAllowedRegionIDs) == 0 {
		return nil, nil
	}

	const query = `
        WITH worker_running_tasks AS (
            SELECT
                w.worker_id,
                w.flavor_id,
                w.region_id,
                w.concurrency,
                COUNT(t.task_id) FILTER (WHERE t.status = 'R') AS running_tasks,
                COUNT(t.task_id) FILTER (WHERE t.status = 'P' OR t.status = 'A') AS blocked_tasks
            FROM
                worker w
            LEFT JOIN
                task t ON t.worker_id = w.worker_id
            GROUP BY
                w.worker_id, w.flavor_id, w.region_id, w.concurrency
        )
        SELECT
            worker_id, flavor_id, region_id, concurrency, running_tasks
        FROM
            worker_running_tasks
        WHERE
            running_tasks < concurrency
            AND blocked_tasks = 0
            AND flavor_id = ANY($1)
            AND region_id = ANY($2)
    `

	rows, err := db.Query(query, pq.Array(allAllowedFlavorIDs), pq.Array(allAllowedRegionIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []RecyclableWorker
	for rows.Next() {
		var w RecyclableWorker
		if err := rows.Scan(
			&w.WorkerID,
			&w.FlavorID,
			&w.RegionID,
			&w.Concurrency,
			&w.Running,
		); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}

	return workers, rows.Err()
}

func containsInt(slice []int, val int) bool {
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
	workerIDs []int,
	stepID int,
	newConcurrency int,
	prefetch int,
	allowedFlavorIDs []int,
	weightMemory *sync.Map,
) int {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return 0
	}

	ids := make([]string, 0, len(workerIDs))
	tempWeightUpdates := make(map[int]*sync.Map) // worker_id → *sync.Map[task_id]float64

	recyclableMap := make(map[int]RecyclableWorker)
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
					tid := key.(int)
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
	flavorIDs map[int]struct{},
	regionIDs map[int]struct{},
	needed int,
) []int {
	selected := make([]int, 0, needed)
	for _, w := range recyclable {
		if _, ok := flavorIDs[w.FlavorID]; !ok {
			continue
		}
		if _, ok := regionIDs[w.RegionID]; !ok {
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
	ProviderID int
}

func deployWorkers(
	ctx context.Context,
	qm *QuotaManager,
	creator WorkerCreator,
	stepID int,
	concurrency int,
	prefetch int,
	allowedFlavorIDs []int,
	allowedRegionIDs []int,
	regionInfoMap map[int]RegionInfo,
	howMany int,
	workflowCounterMemory *WorkflowCounter,
) (int, error) {
	if howMany <= 0 {
		return 0, nil
	}

	deployed := 0

	for i := 0; i < howMany; i++ {
		found := false
		var selectedFlavorID, selectedRegionID int
		var regionInfo RegionInfo

		// Try flavors/regions cautiously
		for _, flavorID := range allowedFlavorIDs {
			for _, regionID := range allowedRegionIDs {
				ri, ok := regionInfoMap[regionID]
				if !ok {
					log.Printf("⚠️ Unknown region_id %d", regionID)
					continue
				}

				if qm.CanLaunch(ri.Name, ri.Provider, int32(concurrency), float32(prefetch)) {
					selectedFlavorID = flavorID
					selectedRegionID = regionID
					regionInfo = ri
					found = true
					break
				}
			}
			if found {
				break
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

		// Actually create the worker
		_, err := creator.CreateWorker(ctx, &pb.WorkerRequest{
			FlavorId:    uint32(selectedFlavorID),
			ProviderId:  uint32(regionInfo.ProviderID),
			RegionId:    uint32(selectedRegionID),
			StepId:      uint32(stepID),
			Number:      1,
			Concurrency: uint32(concurrency),
			Prefetch:    uint32(prefetch),
		})
		if err != nil {
			log.Printf("⚠️ Failed to create worker (flavor %d region %d): %v", selectedFlavorID, selectedRegionID, err)
			return deployed, err
		}
		log.Printf("✅ Deployed worker: step=%d flavor=%d region=%d provider=%s", stepID, selectedFlavorID, selectedRegionID, regionInfo.Provider)
		workflowCounterMemory.Counter++

		// Update QuotaManager accounting only if creation succeeded
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
	workflowCounterMemory map[int]WorkflowCounter,
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
	allFlavorIDs := make(map[int]struct{})
	allRegionIDs := make(map[int]struct{})
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

	recyclableMap := make(map[int]RecyclableWorker)
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

		// Build recruiter-specific allowed flavor/region sets
		recruiterFlavorIDs := make(map[int]struct{})
		recruiterRegionIDs := make(map[int]struct{})
		for _, fr := range flavorRegionList {
			recruiterFlavorIDs[fr.FlavorID] = struct{}{}
			recruiterRegionIDs[fr.RegionID] = struct{}{}
		}

		needed := recruiter.NeededWorkers
		if needed <= 0 {
			continue
		}

		// Try recycling first
		selectedWorkerIDs := selectWorkersForRecruiter(recyclableWorkers, recruiterFlavorIDs, recruiterRegionIDs, needed)
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
		}

		if needed <= 0 || !recruiter.TimeoutPassed {
			continue
		}

		// Try deploying if still needed
		allowedFlavorIDs := keys(recruiterFlavorIDs)
		allowedRegionIDs := keys(recruiterRegionIDs)
		wfc := workflowCounterMemory[recruiter.WorkflowID]

		deployed, err := deployWorkers(
			ctx,
			qm,
			creator,
			recruiter.StepID,
			recruiter.WorkerConcurrency,
			recruiter.WorkerPrefetch,
			allowedFlavorIDs,
			allowedRegionIDs,
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
	}

	return nil
}

func keys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type WorkflowCounter struct {
	Counter int
	Maximum *int
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

		var workflowCounterMemory map[int]WorkflowCounter
		err := memory.LoadMemory(ctx, db, "workflow_counters", &workflowCounterMemory)
		if err != nil {
			log.Printf("ℹ️ No existing workflow_counters memory found, starting fresh")
			workflowCounterMemory = make(map[int]WorkflowCounter)
		}

		recruiterTimers := make(map[RecruiterKey]RecruiterState)

		for {
			select {
			case <-ctx.Done():
				log.Println("🛑 Recruiter loop stopped")
				return

			case now := <-ticker.C:
				err := RecruiterCycle(ctx, db, qm, creator, recruiterTimers, weightMemory, workflowCounterMemory, now)
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
