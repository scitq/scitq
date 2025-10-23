package recruitment

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/protofilter"
	ws "github.com/scitq/scitq/server/websocket"
	"github.com/scitq/scitq/utils"
)

type WorkflowCounter struct {
	Counter int
	Maximum *int
}

// getWorkflowCounters loads the current number of active workers and the maximum number of allowed workers per workflow
func getWorkflowCounters(db *sql.DB, workflowCounterMemory map[int32]WorkflowCounter) error {
	// Step 1: Load live worker counts per workflow (includes workflows with zero workers)
	log.Printf("ℹ️ Adjusting workflow counters")
	rows, err := db.Query(`
        SELECT
            wf.workflow_id,
            COUNT(w.worker_id) AS worker_count,
			wf.maximum_workers
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

	for rows.Next() {
		var wfCounter WorkflowCounter
		var workflowID int32
		if err := rows.Scan(&workflowID, &wfCounter.Counter, &wfCounter.Maximum); err != nil {
			return fmt.Errorf("failed to scan workflow worker count: %w", err)
		}
		workflowCounterMemory[workflowID] = wfCounter
	}

	if err := rows.Err(); err != nil {
		return err
	}

	return nil
}

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
	CurrentWorkers        int
	Rounds                int
	PendingTasks          int
	ActiveTaskrate        int
	TargetTaskrate        int
	PotentialTaskrate     int
	WorkflowID            int32
	TimeoutPassed         bool // <- new field
	LastTrigger           time.Time
	RemainingUntilTimeout time.Duration
}

func listActiveRecruiters(db *sql.DB, now time.Time, recruiterTimers map[RecruiterKey]RecruiterState, wfcMem map[int32]WorkflowCounter) ([]Recruiter, error) {
	const query = `
        WITH pt_agg AS (
            SELECT step_id, COUNT(*) AS pending
            FROM task
            WHERE status = 'P'
            GROUP BY step_id
        ),
        active_agg AS (
            SELECT step_id, COALESCE(SUM(weight),0) AS active_taskrate
            FROM task
            WHERE status = 'R'
            GROUP BY step_id
        ),
        worker_load AS (
            SELECT
                w.worker_id,
                w.step_id,
                w.concurrency,
                COALESCE(SUM(t.weight),0) AS load
            FROM worker w
            LEFT JOIN task t ON t.worker_id = w.worker_id AND t.status IN ('A','C','D','O','R')
            GROUP BY w.worker_id, w.step_id, w.concurrency
        ),
        worker_agg AS (
            SELECT
                step_id,
                COUNT(*) AS current_workers,
                SUM(concurrency) AS potential_taskrate, -- total capacity
                SUM(GREATEST(concurrency - load, 0)) AS free_taskrate     -- capacity left (what we actually need)
            FROM worker_load
            GROUP BY step_id
        )
        SELECT
            r.step_id,
            r.rank,
            r.timeout,
            r.protofilter,
            r.worker_concurrency,
            r.worker_prefetch,
            r.rounds,
            r.cpu_per_task,
            r.memory_per_task,
            r.disk_per_task,
            r.prefetch_percent,
            r.concurrency_min,
            r.concurrency_max,
            r.maximum_workers AS step_maximum,
            COALESCE(wagg.current_workers, 0) AS current_workers,
            wf.workflow_id,
            pa.pending AS pending_tasks,
            CEIL(pa.pending * 1.0 / r.rounds) AS target_taskrate,
            COALESCE(aa.active_taskrate,0) AS active_taskrate,
            COALESCE(wagg.free_taskrate, 0) AS potential_taskrate   -- NOTE: we store *free* capacity in potential_taskrate
        FROM
            recruiter r
        JOIN
            step s ON s.step_id = r.step_id
        JOIN
            workflow wf ON wf.workflow_id = s.workflow_id
        JOIN
            pt_agg pa ON pa.step_id = r.step_id
        LEFT JOIN
            active_agg aa ON aa.step_id = r.step_id
        LEFT JOIN
            worker_agg wagg ON wagg.step_id = r.step_id
        GROUP BY
            r.step_id, r.rank, r.timeout, r.protofilter,
            r.worker_concurrency, r.worker_prefetch,
            r.maximum_workers, r.rounds,
            r.cpu_per_task, r.memory_per_task, r.disk_per_task,
            r.prefetch_percent, r.concurrency_min, r.concurrency_max,
            wf.workflow_id, pa.pending, aa.active_taskrate, wagg.current_workers, wagg.free_taskrate
        HAVING
            CEIL(pa.pending * 1.0 / r.rounds) > COALESCE(wagg.free_taskrate, 0)
        ORDER BY
            r.step_id, r.rank
    `
	//			AND (wf.maximum_workers IS NULL OR COUNT(w.worker_id) < wf.maximum_workers)
	//			AND (r.maximum_workers  IS NULL OR COUNT(w.worker_id) < r.maximum_workers)
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Recruiter
	for rows.Next() {
		var r Recruiter
		err := rows.Scan(
			&r.StepID,
			&r.Rank,
			&r.TimeoutSeconds,
			&r.Protofilter,
			&r.WorkerConcurrency,
			&r.WorkerPrefetch,
			&r.Rounds,
			&r.CpuPerTask,
			&r.MemoryPerTask,
			&r.DiskPerTask,
			&r.PrefetchPercent,
			&r.ConcurrencyMin,
			&r.ConcurrencyMax,
			&r.MaximumWorkers,
			&r.CurrentWorkers,
			&r.WorkflowID,
			&r.PendingTasks,
			&r.TargetTaskrate,
			&r.ActiveTaskrate,
			&r.PotentialTaskrate,
		)
		if err != nil {
			return nil, err
		}

		// DEBUG
		var stepMaxStr string
		if r.MaximumWorkers != nil {
			stepMaxStr = fmt.Sprintf("%d", *r.MaximumWorkers)
		} else {
			stepMaxStr = "unlimited"
		}
		log.Printf("ℹ️ [DEBUG] Found active recruiter: step_id=%d rank=%d pending_tasks=%d active_taskrate=%d current_workers=%d step_maximum=%s workflow_maximum=%v\n",
			r.StepID, r.Rank, r.PendingTasks, r.ActiveTaskrate, r.CurrentWorkers, stepMaxStr, wfcMem[r.WorkflowID].Maximum)
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

		results = append(results, r)
	}

	return results, rows.Err()
}

type FlavorDetail struct {
	FlavorID int32
	RegionID int32
	Cpu      int32
	Memory   float64
	Disk     float64
}

// fetchRecruiterFlavors fetches available flavors (for recruitment) and all flavors (for recycling)
// for each recruiter.
func fetchRecruiterFlavors(
	db *sql.DB,
	recruiters []Recruiter,
) (
	map[RecruiterKey][]FlavorDetail, // recruitmentMap: available only
	map[RecruiterKey][]FlavorDetail, // recyclingMap: all flavors
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

	recruitmentMap := make(map[RecruiterKey][]FlavorDetail)
	recyclingMap := make(map[RecruiterKey][]FlavorDetail)
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
		flavorDetail := FlavorDetail{
			FlavorID: flavorID,
			RegionID: regionID,
			Cpu:      cpu,
			Memory:   mem,
			Disk:     disk,
		}
		if available {
			recruitmentMap[key] = append(recruitmentMap[key], flavorDetail)
		}
		recyclingMap[key] = append(recyclingMap[key], flavorDetail)
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
	Scope       string
	WorkflowID  *int32
	Cpu         *int32
	Memory      *float64
	Disk        *float64
	Occupation  float64
}

// Note: This function now returns each worker's recyclability scope ('G' or 'W') and
// the originating workflow id (via the worker's current step). Workers with scopes
// 'N' (never) or 'T' (temporary block) are excluded at the SQL level.
func findRecyclableWorkers(
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
			coalesce(sum(t.weight),0)/w.concurrency AS current_load,
			w.recyclable_scope,
			s.workflow_id,
			f.cpu,
			f.mem,
			f.disk
		FROM
			worker w
		LEFT JOIN
			task t ON t.worker_id = w.worker_id AND t.status IN ('A','C','D','O','R')
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
			COALESCE(sum(t.weight),0) < w.concurrency
    `
	log.Printf("Trying to find with Flavors %v| Regions %v", allAllowedFlavorIDs, allAllowedRegionIDs)
	rows, err := db.Query(query, pq.Array(allAllowedFlavorIDs), pq.Array(allAllowedRegionIDs))
	if err != nil {
		return nil, err
	}

	var workers []RecyclableWorker
	var stepFreeSlots = make(map[int32]int) // step_id -> free slots
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
			&w.Occupation, // current_load
			&w.Scope,
			&workflowIDProxy,
			&cpu,
			&memory,
			&disk,
		); err != nil {
			rows.Close()
			return nil, err
		}
		w.StepID = utils.NullInt32ToPtr(stepIDproxy)
		if w.StepID != nil {
			// Count free slots per step for informational/debugging purposes
			stepFreeSlots[*w.StepID] += max(w.Concurrency-int(math.Ceil(w.Occupation*float64(w.Concurrency))), 0)
		}
		w.WorkflowID = utils.NullInt32ToPtr(workflowIDProxy)
		w.Cpu = utils.NullInt32ToPtr(cpu)
		w.Memory = utils.NullFloat64ToPtr(memory)
		w.Disk = utils.NullFloat64ToPtr(disk)

		// Compute occupation
		log.Printf("[DEBUG] Worker %d: occupation %.2f", w.WorkerID, w.Occupation)
		if w.Occupation >= 0.99 {
			log.Printf("[DEBUG] Skipping worker %d: fully occupied", w.WorkerID)
			continue
		}

		workers = append(workers, w)
	}
	rows.Close()

	log.Printf("[DEBUG] Found %d recyclable workers, with free slots per step: %v", len(workers), stepFreeSlots)
	// Filter out workers whose step has pending tasks exceeding the free slots on recyclable workers
	const stepQuery = `
		SELECT
			step_id,
			COUNT(task_id) AS pending_tasks
		FROM
			task
		WHERE
			status = 'P'
			AND step_id = ANY($1::int[])
		GROUP BY
			step_id
    `
	var stepIDs []int32
	for stepID := range stepFreeSlots {
		stepIDs = append(stepIDs, stepID)
	}
	log.Printf("[DEBUG] Querying pending tasks for steps %v", stepIDs)
	stepRows, err := db.Query(stepQuery, pq.Array(stepIDs))
	if err != nil {
		log.Printf("⚠️ Failed to query pending tasks per step: %v", err)
		return nil, err
	}
	defer stepRows.Close()

	var stepPendingTasks = make(map[int32]int) // step_id -> pending tasks
	for stepRows.Next() {
		var stepID int32
		var pending int
		if err := stepRows.Scan(&stepID, &pending); err != nil {
			log.Printf("⚠️ Failed to scan pending tasks per step: %v", err)
			return nil, err
		}
		log.Printf("[DEBUG] ℹ️ Step %d has %d pending tasks and %d free slots on recyclable workers", stepID, pending, stepFreeSlots[stepID])
		if pending > stepFreeSlots[stepID] {
			stepPendingTasks[stepID] = pending
		}
	}

	freeWorkers := []RecyclableWorker{}
	for _, w := range workers {
		if w.StepID == nil {
			// idle worker → fully recyclable
			freeWorkers = append(freeWorkers, w)
			continue
		}
		if stepPendingTasks[*w.StepID] > 0 {
			log.Printf("[DEBUG] ℹ️ Skipping worker %d (step %d) for recycling as its step has %d pending tasks exceeding free slots %d",
				w.WorkerID, *w.StepID, stepPendingTasks[*w.StepID], stepFreeSlots[*w.StepID])
			stepPendingTasks[*w.StepID] -= max(w.Concurrency-int(math.Ceil(w.Occupation*float64(w.Concurrency))), 0)
			continue
		}
		freeWorkers = append(freeWorkers, w)
	}

	return freeWorkers, stepRows.Err()
}

func containsInt(slice []int32, val int32) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// recycle workers and update task weights in DB if needed
func recycleWorkers(
	db *sql.DB,
	recyclableWorkers []RecyclableWorker, // <- directly what FindRecyclableWorkers returns
	workerIDs []int32,
	recruiter *Recruiter,
	allowedFlavorIDs []int32,
	newConcurrencyByWorkerID map[int32]int,
	newPrefetchByWorkerID map[int32]int,
	remainingTaskRate int,
) (int, int) {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return 0, 0
	}

	newConcurrencies := make([]int, 0, len(workerIDs))
	newPrefetches := make([]int, 0, len(workerIDs))

	recyclableMap := make(map[int32]RecyclableWorker)
	for _, w := range recyclableWorkers {
		recyclableMap[w.WorkerID] = w
	}

	var potentialTaskrate, potentialWorkerNumber int
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
		if 1.0-w.Occupation < 1.0/float64(newConcurrency) {
			// The worker is too occupied to handle any new task at this concurrency
			log.Printf("⚠️ Worker %d is too occupied (%.2f) to handle new concurrency %d", id, w.Occupation, newConcurrency)
			continue
		}
		potentialTaskrate += int(float64(newConcurrency) * (1.0 - w.Occupation))
		potentialWorkerNumber++
		newConcurrencies = append(newConcurrencies, newConcurrency)
		newPrefetches = append(newPrefetches, newPrefetchByWorkerID[id])
		if w.Concurrency != newConcurrency {
			scale := float64(newConcurrency) / float64(w.Concurrency)
			_, err := tx.Exec(`
                UPDATE task
                SET weight = weight * $1
                WHERE worker_id = $2
                  AND status IN ('A','C','R','D','U')
            `, scale, id)
			if err != nil {
				log.Printf("⚠️ Failed to update task weights for worker %d: %v", id, err)
				// continue; do not abort the rest
			}
		}
		if potentialTaskrate >= remainingTaskRate {
			// Stop here: we have enough potential taskrate to cover the recruiter's needs
			log.Printf("⚠️ Stopping recycling at worker %d to avoid overshooting the recruiter's needs", id)
			break
		}
		if recruiter.MaximumWorkers != nil && potentialWorkerNumber+recruiter.CurrentWorkers >= *recruiter.MaximumWorkers {
			// Stop here: we have enough potential workers to cover the recruiter's max workers limit
			log.Printf("⚠️ Stopping recycling at worker %d to avoid exceeding the recruiter's maximum_workers limit", id)
			break
		}
	}

	if len(workerIDs) == 0 {
		log.Printf("⚠️ No compatible recyclable workers were found.")
		tx.Rollback()
		return 0, 0
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
		return 0, 0
	}
	defer rows2.Close()

	var affected, recycledTaskrate int
	for rows2.Next() {
		affected++
		var workerId int32
		var workerName, stepName sql.NullString
		if err := rows2.Scan(&workerId, &workerName, &stepName); err != nil {
			log.Printf("⚠️ Failed to scan updated worker row: %v", err)
			tx.Rollback()
			return 0, 0
		}
		stepDisplayName := utils.NullStringToString(stepName)
		workerDisplayName := utils.NullStringToString(workerName)
		recycledTaskrate += max(int(float64(newConcurrencyByWorkerID[workerId])*(1.0-recyclableMap[workerId].Occupation)), 0)

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
		return 0, 0
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Commit failed: %v", err)
		return 0, 0
	}

	recruiter.CurrentWorkers += affected
	recruiter.ActiveTaskrate += recycledTaskrate
	return affected, recycledTaskrate
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
	flavorList []FlavorDetail,
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
		var selected FlavorDetail
		var regionInfo RegionInfo

		for _, fr := range flavorList {
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

		// ---- ADDITION: recruiter MaximumWorkers guard clause ----
		if recruiter.MaximumWorkers != nil && recruiter.CurrentWorkers+deployed >= *recruiter.MaximumWorkers {
			log.Printf("⚠️ Step %d reached maximum workers (%d), giving up deployment", recruiter.StepID, *recruiter.MaximumWorkers)
			return deployed, nil
		}
		// ---- END ADDITION ----

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

		//qm.RegisterLaunch(regionInfo.Name, regionInfo.Provider, int32(newConcurrency), float32(newPrefetch))

		deployed++
		remainingTaskrate -= newConcurrency
		if workflowCounterMemory.Maximum != nil && workflowCounterMemory.Counter >= *workflowCounterMemory.Maximum {
			log.Printf("⚠️ Workflow has reached maximum workers for step %d, stopping deployment", recruiter.StepID)
			break
		}
		if recruiter.MaximumWorkers != nil && recruiter.CurrentWorkers+deployed >= *recruiter.MaximumWorkers {
			log.Printf("⚠️ Recruiter has reached maximum_workers for step %d, stopping deployment", recruiter.StepID)
			break
		}
	}

	return deployed, nil
}

func RecruiterCycle(
	ctx context.Context,
	db *sql.DB,
	qm *QuotaManager,
	creator WorkerCreator,
	recruiterTimers map[RecruiterKey]RecruiterState,
	workflowCounterMemory map[int32]WorkflowCounter,
	now time.Time,
) error {

	err := getWorkflowCounters(db, workflowCounterMemory)
	if err != nil {
		log.Printf("⚠️ Could not adjust workflow counters: %v", err)
	}

	recruiters, err := listActiveRecruiters(db, now, recruiterTimers, workflowCounterMemory)
	if err != nil {
		return fmt.Errorf("failed to list active recruiters: %w", err)
	}

	if len(recruiters) == 0 {
		log.Println("ℹ️ No active recruiters")
		return nil
	}

	// Fetch recruiter flavor/region info: recruitmentMap, recyclingMap, regionInfoMap
	recruitmentMap, recyclingMap, regionInfoMap, err := fetchRecruiterFlavors(db, recruiters)
	if err != nil {
		return fmt.Errorf("failed to fetch flavor/region info: %w", err)
	}

	// Aggregate all allowed flavor_ids and region_ids across all recruiters for recycling.
	// Using map[int32]struct{} as a lightweight set to collect unique IDs.
	allFlavorIDs := make(map[int32]struct{})
	allRegionIDs := make(map[int32]struct{})
	for _, frList := range recyclingMap {
		for _, fr := range frList {
			allFlavorIDs[fr.FlavorID] = struct{}{}
			allRegionIDs[fr.RegionID] = struct{}{}
		}
	}

	recyclableWorkers, err := findRecyclableWorkers(db, keys(allFlavorIDs), keys(allRegionIDs))
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

		remainingTaskrate := recruiter.TargetTaskrate - recruiter.PotentialTaskrate
		if remainingTaskrate <= 0 {
			log.Printf("Recruiter step=%d rank=%d already meets target throughput (taskrate %d >= target %d).",
				recruiter.StepID, recruiter.Rank, recruiter.ActiveTaskrate, recruiter.TargetTaskrate)
			continue
		}

		if hasRecyclableWorkers {
			// Try recycling first (select workers to fill the throughput gap)
			selectedWorkerIDs := selectWorkersForRecruiter(recyclableWorkers, recruiterFlavorIDs, recruiterRegionIDs, remainingTaskrate, recruiter.StepID, recruiter.WorkflowID, recruiter)
			if len(selectedWorkerIDs) > 0 {
				// Calculate total taskrate being recycled
				newConcurrencyByWorkerID := make(map[int32]int)
				newPrefetchByWorkerID := make(map[int32]int)
				for _, wid := range selectedWorkerIDs {
					if w, ok := recyclableMap[wid]; ok {
						newConcurrency := computeConcurrencyForRecruiterWorker(recruiter, w)
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
				affected, recycledTaskrate := recycleWorkers(
					db,
					recyclableWorkers,
					selectedWorkerIDs,
					&recruiter,
					keys(recruiterFlavorIDs),
					newConcurrencyByWorkerID,
					newPrefetchByWorkerID,
					remainingTaskrate,
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

// StartRecruitmentLoop runs the recruitment loop every interval duration
func StartRecruiterLoop(
	ctx context.Context,
	db *sql.DB,
	qm *QuotaManager,
	creator WorkerCreator,
	recruiterInterval int,
) {
	go func() {
		ticker := time.NewTicker(time.Duration(recruiterInterval) * time.Second)
		defer func() {
			ticker.Stop()
			log.Println("✅ Recruiter loop fully stopped")
		}()

		workflowCounterMemory := make(map[int32]WorkflowCounter)
		recruiterTimers := make(map[RecruiterKey]RecruiterState)

		for {
			select {
			case <-ctx.Done():
				log.Println("🛑 Recruiter loop stopped")
				return

			case now := <-ticker.C:
				err := RecruiterCycle(ctx, db, qm, creator, recruiterTimers, workflowCounterMemory, now)
				if err != nil {
					log.Printf("⚠️ Recruiter cycle failed: %v", err)
				}

			}
		}
	}()
}
