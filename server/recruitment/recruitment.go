package recruitment

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/memory"
	"github.com/scitq/scitq/server/protofilter"
	ws "github.com/scitq/scitq/server/websocket"
)

type RecruiterKey struct {
	StepID int32
	Rank   int
}

type RecruiterState struct {
	LastTrigger time.Time
}

type Recruiter struct {
	StepID                int32
	Rank                  int
	TimeoutSeconds        int
	Protofilter           string
	WorkerConcurrency     *int
	WorkerPrefetch        *int
	CpuPerTask            *int
	MemoryPerTask         *float32
	DiskPerTask           *float32
	ConcurrencyMax        *int
	ConcurrencyMin        *int
	PrefetchPercent       *int
	MaximumWorkers        *int
	Rounds                int
	PendingTasks          int
	ActiveTaskrate        int
	TargetTaskrate        int
	WorkflowID            int32
	TimeoutPassed         bool // <- new field
	LastTrigger           time.Time
	RemainingUntilTimeout time.Duration
}

func ListActiveRecruiters(db *sql.DB, now time.Time, recruiterTimers map[RecruiterKey]RecruiterState, wfcMem map[int32]WorkflowCounter) ([]Recruiter, error) {
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
			r.cpu_per_task,
			r.memory_per_task,
			r.disk_per_task,
			r.prefetch_percent,
			r.concurrency_min,
			r.concurrency_max,
			wf.maximum_workers,
			wf.workflow_id,
			COUNT(t.task_id) AS pending_tasks,
			COALESCE(SUM(w.concurrency), 0) AS active_taskrate,
			CEIL(COUNT(t.task_id) * 1.0 / r.rounds) AS target_taskrate
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
			r.cpu_per_task, r.memory_per_task, r.disk_per_task,
			r.prefetch_percent, r.concurrency_min, r.concurrency_max,
			wf.maximum_workers, wf.workflow_id
		HAVING
			COUNT(t.task_id) > 0
			AND COALESCE(SUM(w.concurrency), 0) < CEIL(COUNT(t.task_id) * 1.0 / r.rounds)
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
			&r.CpuPerTask,
			&r.MemoryPerTask,
			&r.DiskPerTask,
			&r.PrefetchPercent,
			&r.ConcurrencyMin,
			&r.ConcurrencyMax,
			&workflow_maximum_workers,
			&r.WorkflowID,
			&r.PendingTasks,
			&r.ActiveTaskrate,
			&r.TargetTaskrate,
		)
		if err != nil {
			return nil, err
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
	FlavorID int32
	RegionID int32
	Cpu      int32
	Memory   float64
	Disk     float64
}

func FetchRecruiterFlavorRegions(
	db *sql.DB,
	recruiters []Recruiter,
) (
	map[RecruiterKey][]RecruiterFlavorRegion, // recruitmentMap: available only
	map[RecruiterKey][]RecruiterFlavorRegion, // recyclingMap: all flavors
	map[int32]RegionInfo,
	error,
) {
	type recruiterSQLPart struct {
		Key       RecruiterKey
		Condition string
		Args      []interface{}
	}

	var parts []recruiterSQLPart

	for _, r := range recruiters {
		conditions, err := protofilter.ParseProtofilter(r.Protofilter)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed parsing protofilter for step_id %d, rank %d: %w", r.StepID, r.Rank, err)
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
				f.disk,
				fr.cost,
				fr.available
			FROM flavor f
			JOIN flavor_region fr ON f.flavor_id = fr.flavor_id
			JOIN region r ON fr.region_id = r.region_id
			JOIN provider p ON p.provider_id = f.provider_id
            WHERE %s
        `, p.Key.StepID, p.Key.Rank, p.Condition))
	}

	finalQuery := strings.Join(unionQueries, " UNION ALL ") + " ORDER BY cost"

	rows, err := db.Query(finalQuery)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	recruitmentMap := make(map[RecruiterKey][]RecruiterFlavorRegion)
	recyclingMap := make(map[RecruiterKey][]RecruiterFlavorRegion)
	regionInfoMap := make(map[int32]RegionInfo)

	for rows.Next() {
		var stepID, flavorID, regionID, providerID int32
		var rank int
		var cpu int32
		var mem, cost, disk float64
		var regionName string
		var provider string
		var available bool
		if err := rows.Scan(&stepID, &rank, &flavorID, &regionID, &regionName, &provider, &providerID, &cpu, &mem, &disk, &cost, &available); err != nil {
			return nil, nil, nil, err
		}
		key := RecruiterKey{StepID: stepID, Rank: rank}
		region := RecruiterFlavorRegion{
			FlavorID: flavorID,
			RegionID: regionID,
			Cpu:      cpu,
			Memory:   mem,
			Disk:     disk,
		}
		if available {
			recruitmentMap[key] = append(recruitmentMap[key], region)
		}
		recyclingMap[key] = append(recyclingMap[key], region)
		// Store region info if not already known
		if _, exists := regionInfoMap[regionID]; !exists {
			regionInfoMap[regionID] = RegionInfo{
				Name:       regionName,
				Provider:   provider,
				ProviderID: providerID,
			}
		}
	}

	return recruitmentMap, recyclingMap, regionInfoMap, rows.Err()
}

type RecyclableWorker struct {
	WorkerID    int32
	FlavorID    int32
	RegionID    int32
	StepID      *int32
	Concurrency int
	Running     int
	Scope       string
	WorkflowID  *int32
	Cpu         *int32
	Memory      *float64
	Disk        *float64
}

// Note: This function now returns each worker's recyclability scope ('G' or 'W') and
// the originating workflow id (via the worker's current step). Workers with scopes
// 'N' (never) or 'T' (temporary block) are excluded at the SQL level.
func FindRecyclableWorkers(
	db *sql.DB,
	allAllowedFlavorIDs []int32,
	allAllowedRegionIDs []int32,
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
			COUNT(t.task_id) FILTER (WHERE t.status = 'R') AS running_tasks,
			w.recyclable_scope,
			s.workflow_id,
			f.cpu,
			f.mem,
			f.disk
		FROM
			worker w
		LEFT JOIN
			task t ON t.worker_id = w.worker_id
		LEFT JOIN
			step s ON s.step_id = w.step_id
		LEFT JOIN
			flavor f ON f.flavor_id = w.flavor_id
		WHERE
			w.flavor_id = ANY($1::int[])
			AND w.region_id = ANY($2::int[])
			AND w.recyclable_scope IN ('G','W')
		GROUP BY
			w.worker_id, w.flavor_id, w.region_id, w.concurrency, w.step_id, w.recyclable_scope, s.workflow_id,
    		f.cpu, f.mem, f.disk
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
		var stepIDproxy, workflowIDProxy, cpu sql.NullInt32
		var memory, disk sql.NullFloat64
		if err := rows.Scan(
			&w.WorkerID,
			&w.FlavorID,
			&w.RegionID,
			&stepIDproxy,
			&w.Concurrency,
			&w.Running,
			&w.Scope,
			&workflowIDProxy,
			&cpu,
			&memory,
			&disk,
		); err != nil {
			return nil, err
		}
		if stepIDproxy.Valid {
			w.StepID = &stepIDproxy.Int32
		}
		if workflowIDProxy.Valid {
			w.WorkflowID = &workflowIDProxy.Int32
		}
		if cpu.Valid {
			w.Cpu = &cpu.Int32
		}
		if memory.Valid {
			w.Memory = &memory.Float64
		}
		if disk.Valid {
			w.Disk = &disk.Float64
		}
		workers = append(workers, w)
	}

	return workers, rows.Err()
}

func containsInt(slice []int32, val int32) bool {
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
	workerIDs []int32,
	recruiter Recruiter,
	allowedFlavorIDs []int32,
	newConcurrencyByWorkerID map[int32]int,
	newPrefetchByWorkerID map[int32]int,
	weightMemory *sync.Map,
) int {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return 0
	}

	newConcurrencies := make([]int, 0, len(workerIDs))
	newPrefetches := make([]int, 0, len(workerIDs))
	tempWeightUpdates := make(map[int32]*sync.Map) // worker_id → *sync.Map[task_id]float64

	recyclableMap := make(map[int32]RecyclableWorker)
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

		newConcurrency := newConcurrencyByWorkerID[id]
		newConcurrencies = append(newConcurrencies, newConcurrency)
		newPrefetches = append(newPrefetches, newPrefetchByWorkerID[id])
		if w.Concurrency != newConcurrency {
			scale := float64(newConcurrency) / float64(w.Concurrency)

			var currentMap *sync.Map
			if val, ok := weightMemory.Load(id); ok {
				currentMap = val.(*sync.Map)
				newMap := &sync.Map{}
				currentMap.Range(func(key, value any) bool {
					tid := key.(int32)
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

	if len(workerIDs) == 0 {
		log.Printf("⚠️ No compatible recyclable workers were found.")
		tx.Rollback()
		return 0
	}

	// Collect updated worker ids and names using RETURNING so we can emit richer WS events
	q := `
			WITH updated AS (
              UPDATE worker w
              SET concurrency = u.new_concurrency,
                  prefetch = u.new_prefetch,
                  step_id = $4
              FROM (
                SELECT UNNEST($1::int[]) AS worker_id,
                       UNNEST($2::int[]) AS new_concurrency,
                       UNNEST($3::int[]) AS new_prefetch
              ) u
              WHERE w.worker_id = u.worker_id
              RETURNING w.worker_id, w.worker_name, w.step_id
            )
            SELECT uq.worker_id, uq.worker_name, s.step_name
            FROM updated uq
            LEFT JOIN step s ON s.step_id = uq.step_id
		`

	rows2, err := tx.Query(q,
		pq.Array(workerIDs),
		pq.Array(newConcurrencies),
		pq.Array(newPrefetches),
		recruiter.StepID,
	)
	if err != nil {
		log.Printf("⚠️ Failed to update workers: %v", err)
		tx.Rollback()
		return 0
	}
	defer rows2.Close()

	var affected int
	for rows2.Next() {
		affected++
		var workerId int32
		var workerName, stepName sql.NullString
		if err := rows2.Scan(&workerId, &workerName, &stepName); err != nil {
			log.Printf("⚠️ Failed to scan updated worker row: %v", err)
			tx.Rollback()
			return 0
		}

		var stepDisplayName, workerDisplayName string
		if stepName.Valid {
			stepDisplayName = stepName.String
		}
		if workerName.Valid {
			workerDisplayName = workerName.String
		}

		// Emit WS update, including the worker name when available
		ws.EmitWS("worker", workerId, "updated", struct {
			WorkerId    int32  `json:"workerId"`
			Name        string `json:"name,omitempty"`
			StepId      int32  `json:"stepId"`
			StepName    string `json:"stepName,omitempty"`
			Concurrency int    `json:"concurrency"`
			Prefetch    int    `json:"prefetch"`
		}{
			WorkerId:    workerId,
			Name:        workerDisplayName,
			StepId:      recruiter.StepID,
			StepName:    stepDisplayName,
			Concurrency: newConcurrencyByWorkerID[workerId],
			Prefetch:    newPrefetchByWorkerID[workerId],
		})
	}
	if err := rows2.Err(); err != nil {
		log.Printf("⚠️ Error iterating updated workers: %v", err)
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

	return affected
}

// computeConcurrencyForRecruiterWorker returns the concurrency this worker would have
// under the given recruiter. Static recruiters use worker_concurrency as-is.
// Dynamic recruiters compute min(cpu/mem/disk per-task) and clamp to min/max.
func computeConcurrencyForRecruiterWorker(r Recruiter, w RecyclableWorker) int {
	// Static path
	if r.WorkerConcurrency != nil && *r.WorkerConcurrency > 0 {
		return *r.WorkerConcurrency
	}

	// Dynamic path
	ratios := []float64{}
	if r.CpuPerTask != nil && w.Cpu != nil && *r.CpuPerTask > 0 {
		ratios = append(ratios, float64(*w.Cpu)/float64(*r.CpuPerTask))
	}
	if r.MemoryPerTask != nil && w.Memory != nil && *r.MemoryPerTask > 0 {
		ratios = append(ratios, float64(*w.Memory)/float64(*r.MemoryPerTask))
	}
	if r.DiskPerTask != nil && w.Disk != nil && *r.DiskPerTask > 0 {
		ratios = append(ratios, float64(*w.Disk)/float64(*r.DiskPerTask))
	}

	// If nothing defined (unlikely), fallback to 1
	if len(ratios) == 0 {
		return 1
	}

	// min floor of ratios
	min := ratios[0]
	for _, v := range ratios[1:] {
		if v < min {
			min = v
		}
	}
	c := int(math.Floor(min))

	// clamp by recruiter min/max
	if r.ConcurrencyMin != nil && c < int(*r.ConcurrencyMin) {
		c = int(*r.ConcurrencyMin)
	}
	if r.ConcurrencyMax != nil && *r.ConcurrencyMax > 0 && c > int(*r.ConcurrencyMax) {
		c = int(*r.ConcurrencyMax)
	}
	if c < 1 {
		c = 1
	}
	return c
}

// selectWorkersForRecruiter selects recyclable workers to fill a throughput gap (neededTaskrate),
// summing up their concurrency until the total meets or exceeds neededTaskrate.
func selectWorkersForRecruiter(
	recyclable []RecyclableWorker,
	flavorIDs map[int32]struct{},
	regionIDs map[int32]struct{},
	neededTaskrate int,
	recruiterStepID int32,
	recruiterWorkflowID int32,
	r Recruiter,
) []int32 {
	selected := make([]int32, 0)
	totalTaskrate := 0
	for _, w := range recyclable {
		if _, ok := flavorIDs[w.FlavorID]; !ok {
			continue
		}
		if _, ok := regionIDs[w.RegionID]; !ok {
			continue
		}
		// Honor recyclability scope:
		// - 'G' (global): no restriction
		// - 'W' (workflow): only recyclable within the same workflow
		// - others already filtered out at SQL level
		if w.Scope == "W" {
			if w.WorkflowID == nil || *w.WorkflowID != recruiterWorkflowID {
				continue
			}
		}
		if w.StepID != nil && *w.StepID == recruiterStepID {
			continue
		}
		selected = append(selected, w.WorkerID)
		totalTaskrate += computeConcurrencyForRecruiterWorker(r, w)
		if totalTaskrate >= neededTaskrate {
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
	ProviderID int32
}

func deployWorkers(
	ctx context.Context,
	qm *QuotaManager,
	creator WorkerCreator,
	recruiter Recruiter,
	flavorRegionList []RecruiterFlavorRegion,
	regionInfoMap map[int32]RegionInfo,
	remainingTaskrate int,
	workflowCounterMemory *WorkflowCounter,
) (int, error) {
	if remainingTaskrate <= 0 {
		return 0, nil
	}

	deployed := 0

	for remainingTaskrate > 0 {
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
			log.Printf("⚠️ Quota exhausted or no flavor/region available for step %d", recruiter.StepID)
			return deployed, nil
		}

		if workflowCounterMemory.Maximum != nil && workflowCounterMemory.Counter >= *workflowCounterMemory.Maximum {
			log.Printf("⚠️ Workflow has reached maximum workers for step %d, giving up", recruiter.StepID)
			return deployed, nil
		}

		newConcurrency := computeConcurrencyForRecruiterWorker(recruiter,
			RecyclableWorker{Cpu: &selected.Cpu, Memory: &selected.Memory, Disk: &selected.Disk})
		var newPrefetch int
		if recruiter.WorkerPrefetch != nil {
			newPrefetch = *recruiter.WorkerPrefetch
		} else {
			if recruiter.PrefetchPercent != nil {
				newPrefetch = (newConcurrency * *recruiter.PrefetchPercent) / 100
			}
		}

		_, err := creator.CreateWorker(ctx, &pb.WorkerRequest{
			FlavorId:    selected.FlavorID,
			ProviderId:  regionInfo.ProviderID,
			RegionId:    selected.RegionID,
			StepId:      &recruiter.StepID,
			Number:      1,
			Concurrency: int32(newConcurrency),
			Prefetch:    int32(newPrefetch),
		})
		if err != nil {
			log.Printf("⚠️ Failed to create worker (flavor %d region %d): %v", selected.FlavorID, selected.RegionID, err)
			return deployed, err
		}

		log.Printf("✅ Deployed worker: step=%d flavor=%d region=%d provider=%s", recruiter.StepID, selected.FlavorID, selected.RegionID, regionInfo.Provider)
		workflowCounterMemory.Counter++

		qm.RegisterLaunch(regionInfo.Name, regionInfo.Provider, int32(newConcurrency), float32(newPrefetch))

		deployed++
		remainingTaskrate -= newConcurrency
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
	workflowCounterMemory map[int32]WorkflowCounter,
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

	// Fetch recruiter flavor/region info: recruitmentMap, recyclingMap, regionInfoMap
	recruitmentMap, recyclingMap, regionInfoMap, err := FetchRecruiterFlavorRegions(db, recruiters)
	if err != nil {
		return fmt.Errorf("failed to fetch flavor/region info: %w", err)
	}

	// Aggregate all allowed flavor_ids and region_ids across all recruiters for recycling
	allFlavorIDs := make(map[int32]struct{})
	allRegionIDs := make(map[int32]struct{})
	for _, frList := range recyclingMap {
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

	recyclableMap := make(map[int32]RecyclableWorker)
	for _, w := range recyclableWorkers {
		recyclableMap[w.WorkerID] = w
	}

	for _, recruiter := range recruiters {
		key := RecruiterKey{StepID: recruiter.StepID, Rank: recruiter.Rank}
		// Use recruitmentMap for cloud deployment
		flavorRegionList := recruitmentMap[key]
		// Use recyclingMap for recycling logic
		recyclingFlavorRegionList := recyclingMap[key]

		if len(flavorRegionList) == 0 {
			log.Printf("⚠️ No flavor/region candidates for recruiter step=%d rank=%d", recruiter.StepID, recruiter.Rank)
			continue
		}

		// Build recruiter-specific allowed flavor/region sets for recycling
		recruiterFlavorIDs := make(map[int32]struct{})
		recruiterRegionIDs := make(map[int32]struct{})
		for _, fr := range recyclingFlavorRegionList {
			recruiterFlavorIDs[fr.FlavorID] = struct{}{}
			recruiterRegionIDs[fr.RegionID] = struct{}{}
		}

		neededTaskrate := recruiter.TargetTaskrate - recruiter.ActiveTaskrate
		if neededTaskrate <= 0 {
			log.Printf("Recruiter step=%d rank=%d already meets target throughput.", recruiter.StepID, recruiter.Rank)
			continue
		}

		remainingTaskrate := neededTaskrate

		if hasRecyclableWorkers {
			// Try recycling first (select workers to fill the throughput gap)
			selectedWorkerIDs := selectWorkersForRecruiter(recyclableWorkers, recruiterFlavorIDs, recruiterRegionIDs, remainingTaskrate, recruiter.StepID, recruiter.WorkflowID, recruiter)
			if len(selectedWorkerIDs) > 0 {
				// Calculate total taskrate being recycled
				recycledTaskrate := 0
				newConcurrencyByWorkerID := make(map[int32]int)
				newPrefetchByWorkerID := make(map[int32]int)
				for _, wid := range selectedWorkerIDs {
					if w, ok := recyclableMap[wid]; ok {
						newConcurrency := computeConcurrencyForRecruiterWorker(recruiter, w)
						recycledTaskrate += newConcurrency
						newConcurrencyByWorkerID[wid] = newConcurrency
						if recruiter.WorkerPrefetch != nil {
							newPrefetchByWorkerID[wid] = *recruiter.WorkerPrefetch
						} else {
							if recruiter.PrefetchPercent != nil {
								newPrefetchByWorkerID[wid] = int(float32(newConcurrency) * float32(*recruiter.PrefetchPercent) / 100.0)
							} else {
								log.Printf("!! Recruiter %d:%d should have either WorkerPrefetch or PrefetchPercent !!",
									recruiter.StepID, recruiter.Rank)
								newPrefetchByWorkerID[wid] = 0
							}
						}
					}
				}
				affected := recycleWorkers(
					db,
					recyclableWorkers,
					selectedWorkerIDs,
					recruiter,
					keys(recruiterFlavorIDs),
					newConcurrencyByWorkerID,
					newPrefetchByWorkerID,
					weightMemory,
				)
				remainingTaskrate -= recycledTaskrate
				if remainingTaskrate < 0 {
					remainingTaskrate = 0
				}
				log.Printf("♻️ Recycled %d workers (total taskrate=%d) for step=%d (still need throughput=%d)", affected, recycledTaskrate, recruiter.StepID, remainingTaskrate)
			} else {
				log.Printf("No recyclable workers compatible with recruiter %d for step %d", recruiter.Rank, recruiter.StepID)
			}
		}

		if remainingTaskrate <= 0 {
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

		// Enforce workflow-level maximum before cloud deploy (recycling is allowed to exceed caps)
		wfc := workflowCounterMemory[recruiter.WorkflowID]
		if wfc.Maximum != nil && wfc.Counter >= *wfc.Maximum {
			log.Printf("⚠️ Workflow %d at maximum workers (%d) — skipping cloud deploy for step=%d rank=%d (recycling allowed)",
				recruiter.WorkflowID, *wfc.Maximum, recruiter.StepID, recruiter.Rank)
			continue
		}

		// Try deploying if still needed
		// Compute how many workers to deploy, rounding up
		//howMany := (remainingTaskrate + recruiter.WorkerConcurrency - 1) / recruiter.WorkerConcurrency
		log.Printf("⏱️ Timeout passed for step=%d rank=%d; attempting cloud deploy (need throughput=%d, worker concurrency=%d, launching workers for %d task rate)",
			recruiter.StepID, recruiter.Rank, remainingTaskrate, recruiter.WorkerConcurrency, remainingTaskrate)

		deployed, err := deployWorkers(
			ctx,
			qm,
			creator,
			recruiter,
			flavorRegionList,
			regionInfoMap,
			remainingTaskrate,
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

func keys(m map[int32]struct{}) []int32 {
	out := make([]int32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type WorkflowCounter struct {
	Counter int
	Maximum *int
}

func AdjustWorkflowCounters(db *sql.DB, workflowCounterMemory map[int32]WorkflowCounter) error {
	// Step 1: Load live worker counts per workflow (includes workflows with zero workers)
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

	// Step 2: Scan and adjust for **current** workflows
	current := make(map[int32]int) // workflow_id -> live worker count

	for rows.Next() {
		var workflowID int32
		var liveCount int
		if err := rows.Scan(&workflowID, &liveCount); err != nil {
			return fmt.Errorf("failed to scan workflow worker count: %w", err)
		}
		current[workflowID] = liveCount

		mem, exists := workflowCounterMemory[workflowID]
		if !exists {
			// Initialize memory from live state; Maximum is preserved/updated later by ListActiveRecruiters
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

	if err := rows.Err(); err != nil {
		return err
	}

	// Step 3: Remove **stale** workflows from memory (those not present anymore in DB)
	for wfID := range workflowCounterMemory {
		if _, ok := current[wfID]; !ok {
			log.Printf("🧹 Removing stale workflow %d from recruitment memory (no longer present)", wfID)
			delete(workflowCounterMemory, wfID)
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

		var workflowCounterMemory map[int32]WorkflowCounter
		err := memory.LoadMemory(ctx, db, "workflow_counters", &workflowCounterMemory)
		if err != nil {
			log.Printf("ℹ️ No existing workflow_counters memory found, starting fresh")
			workflowCounterMemory = make(map[int32]WorkflowCounter)
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
