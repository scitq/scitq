package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	pq "github.com/lib/pq"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/websocket"
	"github.com/scitq/scitq/utils"
)

// NewStepAgg creates a new StepAgg with sensible defaults.
func NewStepAgg() *StepAgg {
	return &StepAgg{
		RunningTasks: make(map[int32]time.Time),
		RetryingSet:  make(map[int32]bool),
		Download: Accumulator{
			Min: math.MaxFloat64,
		},
		Upload: Accumulator{
			Min: math.MaxFloat64,
		},
		SuccessRun: Accumulator{
			Min: math.MaxFloat64,
		},
		FailRun: Accumulator{
			Min: math.MaxFloat64,
		},
	}
}

// In-memory aggregation types for step/workflow stats
// Accumulator holds aggregation stats for durations/counts.
type Accumulator struct {
	Count int32
	Sum   float64
	Min   float64
	Max   float64
}

// StepAgg aggregates statistics for a step within a workflow.
type StepAgg struct {
	// Task status counters
	Waiting      int32
	Pending      int32
	Accepted     int32
	Running      int32
	Uploading    int32
	Succeeded    int32
	Failed       int32
	ReallyFailed int32 // tasks that have exhausted all retries and are failed
	Retrying     int32            // tasks currently being retried
	RetryingSet  map[int32]bool  // set of task IDs currently retrying
	Total        int32

	// Accumulators for durations
	Download   Accumulator
	Upload     Accumulator
	SuccessRun Accumulator
	FailRun    Accumulator

	// RunningTasks maps taskID to start time
	RunningTasks map[int32]time.Time

	// Time bounds (epoch seconds)
	StartTime *int64
	EndTime   *int64
}

// stepKey identifies a step across the whole cluster. Value type so it
// works as a map key.
type stepKey struct {
	workflowID int32
	stepID     int32
}

// StepStatsAgg holds in-memory aggregated statistics for steps in workflows.
type StepStatsAgg struct {
	mu   sync.Mutex
	data map[int32]map[int32]*StepAgg // workflow_id -> step_id -> *StepAgg
	// dirty is the set of steps whose counters have changed since the
	// last snapshot flush. FlushLoop reads and clears this set on a
	// 100 ms tick and emits a snapshot per workflow. Populated by
	// Adjust and by the direct-manipulation sites (UpdateTaskStatus)
	// via MarkDirty. See specs/task_transitions.md for the design
	// (snapshot dispatch replacing per-transition deltas).
	dirty map[stepKey]struct{}
}

// NewStepStatsAgg creates a new StepStatsAgg with initialized internal maps.
func NewStepStatsAgg(db *sql.DB) (*StepStatsAgg, error) {
	agg := &StepStatsAgg{
		data: make(map[int32]map[int32]*StepAgg),
	}

	// Single query: include steps with zero tasks via LEFT JOIN,
	// compute status counts, totals, duration accumulators,
	// and gather running tasks (task_id, run_started_at) as separate arrays per group.
	rows, err := db.Query(`
		SELECT
			s.workflow_id,
			s.step_id,
			-- totals and status counters (count only non-null task rows).
			-- ALL active-state buckets filter NOT hidden: a manual retry of
			-- a non-terminal task (e.g. one stuck in C/O) hides the
			-- original row without changing its status, so an unfiltered
			-- COUNT would persistently overcount Accepted/Running/etc.
			-- until someone rebuilds the aggregator from scratch. The
			-- delta path (UpdateTaskStatus) already handles the hide
			-- correctly; this query is what every 5-min Reconcile reads,
			-- so any missing filter here is exactly the drift surface.
			COUNT(*) FILTER (WHERE NOT t.hidden) AS total,
			COUNT(*) FILTER (WHERE t.status = 'W' AND NOT t.hidden) AS waiting,
			COUNT(*) FILTER (WHERE t.status IN ('P','I') AND NOT t.hidden) AS pending,
			-- 'A' (Assigned) belongs to the accepted/"Starting" bucket the
			-- delta path uses (see UpdateTaskStatus). Previously omitted
			-- here, which made Reconcile silently un-count A tasks every
			-- 5 min — Total stayed right, per-status buckets drifted.
			COUNT(*) FILTER (WHERE t.status IN ('A','C','D','O') AND NOT t.hidden) AS accepted,
			COUNT(*) FILTER (WHERE t.status = 'R' AND NOT t.hidden) AS running,
			COUNT(*) FILTER (WHERE t.status IN ('U','V') AND NOT t.hidden) AS uploading,
			COUNT(*) FILTER (WHERE t.status = 'S' AND NOT t.hidden) AS succeeded,
			COUNT(*) FILTER (WHERE t.status = 'F' AND t.hidden) AS failed,
			COUNT(*) FILTER (WHERE t.status = 'F' AND NOT t.hidden) AS reallyfailed,
			COUNT(*) FILTER (WHERE NOT t.hidden AND t.previous_task_id IS NOT NULL AND t.status NOT IN ('S','F')) AS retrying,

			-- download/upload accumulators
			COALESCE(SUM(t.download_duration), 0) AS dl_sum,
			COALESCE(MIN(t.download_duration), 0) AS dl_min,
			COALESCE(MAX(t.download_duration), 0) AS dl_max,
			COALESCE(SUM(t.upload_duration), 0) AS up_sum,
			COALESCE(MIN(t.upload_duration), 0) AS up_min,
			COALESCE(MAX(t.upload_duration), 0) AS up_max,
			COUNT(*) FILTER (WHERE t.download_duration IS NOT NULL) AS dl_count,
			COUNT(*) FILTER (WHERE t.upload_duration IS NOT NULL) AS up_count,

			-- run accumulators split by outcome
			COUNT(*) FILTER (WHERE t.status = 'S') AS run_s_count,
			COALESCE(SUM(t.run_duration) FILTER (WHERE t.status = 'S'), 0) AS run_s_sum,
			COALESCE(MIN(t.run_duration) FILTER (WHERE t.status = 'S'), 0) AS run_s_min,
			COALESCE(MAX(t.run_duration) FILTER (WHERE t.status = 'S'), 0) AS run_s_max,

			COUNT(*) FILTER (WHERE t.status IN ('F','V')) AS run_f_count,
			COALESCE(SUM(t.run_duration) FILTER (WHERE t.status = 'F'), 0) AS run_f_sum,
			COALESCE(MIN(t.run_duration) FILTER (WHERE t.status = 'F'), 0) AS run_f_min,
			COALESCE(MAX(t.run_duration) FILTER (WHERE t.status = 'F'), 0) AS run_f_max,

			-- time bounds (epoch seconds)
			COALESCE(MIN(EXTRACT(EPOCH FROM t.run_started_at) - t.download_duration)::bigint, 0) AS start_epoch,
			COALESCE(
				MAX( (EXTRACT(EPOCH FROM t.run_started_at) + t.run_duration + t.upload_duration) )
				FILTER (WHERE t.status IN ('S','F') AND t.run_started_at IS NOT NULL),
				0
			)::bigint AS end_epoch,

			-- running tasks as separate arrays (NOT hidden, same reason as
			-- the running counter above — a hidden R task is a ghost).
			array_agg(t.task_id) FILTER (WHERE t.status = 'R' AND NOT t.hidden AND t.run_started_at IS NOT NULL) AS running_task_ids,
			array_agg(EXTRACT(EPOCH FROM t.run_started_at)::bigint) FILTER (WHERE t.status = 'R' AND NOT t.hidden AND t.run_started_at IS NOT NULL) AS running_task_times,

			-- in-flight retry clones so RetryingSet can decrement Retrying when they finish
			COALESCE(array_agg(t.task_id) FILTER (WHERE NOT t.hidden AND t.previous_task_id IS NOT NULL AND t.status NOT IN ('S','F')), '{}') AS retrying_task_ids
		FROM step s
		LEFT JOIN task t ON t.step_id = s.step_id
		GROUP BY s.workflow_id, s.step_id
		ORDER BY s.workflow_id, s.step_id
	`)
	if err != nil {
		// If the query fails, return error
		return nil, fmt.Errorf("failed to query step stats aggregation: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			workflowID, stepID                                    int32
			total, waiting, pending, accepted, running, uploading int32
			succeeded, failed, reallyfailed, retrying              int32
			dlSum, dlMin, dlMax, upSum, upMin, upMax              float64
			dlCount, upCount                                      int32
			runSCount                                             int32
			runSSum, runSMin, runSMax                             float64
			runFCount                                             int32
			runFSum, runFMin, runFMax                             float64
			startEpoch, endEpoch                                  sql.NullInt64
			runningIDs                                            pq.Int64Array
			runningTimes                                          pq.Int64Array
			retryingIDs                                           pq.Int64Array
		)
		if err := rows.Scan(
			&workflowID,
			&stepID,
			&total, &waiting, &pending, &accepted, &running, &uploading, &succeeded, &failed, &reallyfailed, &retrying,
			&dlSum, &dlMin, &dlMax, &upSum, &upMin, &upMax,
			&dlCount, &upCount,
			&runSCount, &runSSum, &runSMin, &runSMax,
			&runFCount, &runFSum, &runFMin, &runFMax,
			&startEpoch, &endEpoch,
			&runningIDs,
			&runningTimes,
			&retryingIDs,
		); err != nil {
			log.Printf("Step stats reconstruction error: could not parse line : %v", err)
			continue
		}

		startPtr := utils.NullInt64ToPtr(startEpoch)
		endPtr := utils.NullInt64ToPtr(endEpoch)

		if agg.data[workflowID] == nil {
			agg.data[workflowID] = make(map[int32]*StepAgg)
		}
		retryingSet := make(map[int32]bool, len(retryingIDs))
		for _, id := range retryingIDs {
			if id > 0 {
				retryingSet[int32(id)] = true
			}
		}
		sagg := &StepAgg{
			Waiting:      waiting,
			Pending:      pending,
			Accepted:     accepted,
			Running:      running,
			Uploading:    uploading,
			Succeeded:    succeeded,
			Failed:       failed,
			ReallyFailed: reallyfailed,
			Retrying:     retrying,
			RetryingSet:  retryingSet,
			Total:        total,
			Download:     Accumulator{Count: dlCount, Sum: dlSum, Min: dlMin, Max: dlMax},
			Upload:       Accumulator{Count: upCount, Sum: upSum, Min: upMin, Max: upMax},
			SuccessRun: Accumulator{
				Count: runSCount, Sum: runSSum, Min: runSMin, Max: runSMax,
			},
			FailRun: Accumulator{
				Count: runFCount, Sum: runFSum, Min: runFMin, Max: runFMax,
			},
			RunningTasks: make(map[int32]time.Time),
			StartTime:    startPtr,
			EndTime:      endPtr,
		}
		// Populate RunningTasks from returned arrays (task_id[], epoch[])
		if len(runningIDs) > 0 && len(runningIDs) == len(runningTimes) {
			for i := range runningIDs {
				// runningIDs and runningTimes may contain zero values if SQL returned NULLs; skip zeros
				if runningIDs[i] == 0 || runningTimes[i] == 0 {
					continue
				}
				tid := int32(runningIDs[i])
				startedAt := time.Unix(int64(runningTimes[i]), 0).UTC()
				sagg.RunningTasks[tid] = startedAt
			}
		}
		agg.data[workflowID][stepID] = sagg
	}
	// If rows.Err() is non-nil, we still return what we loaded.

	return agg, nil
}

// Reconcile rebuilds the in-memory stats from the database,
// fixing any drift between cached counters and actual task states.
func (a *StepStatsAgg) Reconcile(db *sql.DB) {
	fresh, err := NewStepStatsAgg(db)
	if err != nil {
		log.Printf("⚠️ stats reconciliation failed: %v", err)
		return
	}
	a.mu.Lock()
	a.data = fresh.data
	// Mark every step dirty so the next flush ships the reconciled
	// state to every subscribed UI. Reconcile and normal operation
	// converge on the same wire protocol — no special "Reconcile just
	// happened" event needed. See specs/task_transitions.md.
	for wfID, steps := range a.data {
		for stepID := range steps {
			a.markDirtyLocked(wfID, stepID)
		}
	}
	a.mu.Unlock()
	log.Printf("♻️ stats reconciled from DB")
}

// markDirtyLocked adds (workflowID, stepID) to the dirty set. Caller
// MUST hold a.mu. Lazy-inits the map so the aggregator can be
// constructed by callers without a dirty-map allocation until the
// first mutation.
func (a *StepStatsAgg) markDirtyLocked(workflowID, stepID int32) {
	if a.dirty == nil {
		a.dirty = make(map[stepKey]struct{})
	}
	a.dirty[stepKey{workflowID, stepID}] = struct{}{}
}

// MarkDirty is the entry point for callers that mutated the aggregator
// under a self-held lock (e.g. UpdateTaskStatus, which acquires
// a.mu.Lock() itself and manipulates a.data[wid][sid].Waiting-- etc.
// directly). Once every such site is migrated to Adjust or
// TransitionTask, this can go private again.
func (a *StepStatsAgg) MarkDirty(workflowID, stepID int32) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.markDirtyLocked(workflowID, stepID)
	a.mu.Unlock()
}

// MarkDirtyLocked is the same as MarkDirty but assumes the caller
// already holds a.mu — used inside UpdateTaskStatus which holds the
// lock across its aggregator work.
func (a *StepStatsAgg) MarkDirtyLocked(workflowID, stepID int32) {
	if a == nil {
		return
	}
	a.markDirtyLocked(workflowID, stepID)
}

// EnsureStep guarantees an entry exists for (workflowID, stepID).
func (a *StepStatsAgg) EnsureStep(workflowID, stepID int32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.data[workflowID] == nil {
		a.data[workflowID] = make(map[int32]*StepAgg)
	}
	if _, ok := a.data[workflowID][stepID]; !ok {
		a.data[workflowID][stepID] = NewStepAgg()
	}
}

// Adjust runs fn with the StepAgg for (workflowID, stepID) while holding the
// aggregator mutex. It lazily creates any missing map entries so call sites
// don't need to care about initialisation order. This is the canonical way
// to mutate counters from anywhere outside UpdateTaskStatus; using it
// eliminates the two classes of drift caused by unlocked direct map access:
//   - races with concurrent writers (int32 increments are not atomic)
//   - updates to a StepAgg that Reconcile has just orphaned by swapping data
func (a *StepStatsAgg) Adjust(workflowID, stepID int32, fn func(*StepAgg)) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.data == nil {
		a.data = make(map[int32]map[int32]*StepAgg)
	}
	if _, ok := a.data[workflowID]; !ok {
		a.data[workflowID] = make(map[int32]*StepAgg)
	}
	agg, ok := a.data[workflowID][stepID]
	if !ok {
		agg = NewStepAgg()
		a.data[workflowID][stepID] = agg
	}
	fn(agg)
	// Any Adjust caller mutated the aggregator; mark the step dirty
	// so the next FlushLoop tick emits a snapshot. Callers that go
	// through Adjust get snapshot dispatch for free.
	a.markDirtyLocked(workflowID, stepID)
}

// RemoveStep deletes a step entry by its stepID, scanning all workflows to locate it.
func (a *StepStatsAgg) RemoveStep(stepID int32) int32 {
	a.mu.Lock()
	var workflowId int32
	defer a.mu.Unlock()
	for wfID, steps := range a.data {
		if _, ok := steps[stepID]; ok {
			delete(steps, stepID)
			if len(steps) == 0 {
				delete(a.data, wfID)
			}
			workflowId = wfID
			break
		}
	}
	return workflowId
}

func (a *StepStatsAgg) RemoveWorkflow(workflowID int32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.data, workflowID)
}

// GetStepStats implements the gRPC endpoint for step-level statistics aggregation.
func (s *taskQueueServer) GetStepStats(ctx context.Context, req *pb.StepStatsRequest) (*pb.StepStatsResponse, error) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	now := time.Now().UTC()

	// Determine which steps to include based on workflow_id and step_ids
	type stepKey struct {
		workflowID int32
		stepID     int32
	}

	var includedSteps []stepKey
	if req.WorkflowId != nil && *req.WorkflowId != 0 {
		stepsMap, ok := s.stats.data[*req.WorkflowId]
		if ok {
			if len(req.StepIds) > 0 {
				stepIDSet := make(map[int32]struct{}, len(req.StepIds))
				for _, id := range req.StepIds {
					stepIDSet[id] = struct{}{}
				}
				for stepID := range stepsMap {
					if _, found := stepIDSet[stepID]; found {
						includedSteps = append(includedSteps, stepKey{*req.WorkflowId, stepID})
					}
				}
			} else {
				for stepID := range stepsMap {
					includedSteps = append(includedSteps, stepKey{*req.WorkflowId, stepID})
				}
			}
		}
	} else {
		// Include all workflow/step pairs if no workflow filter
		for wfID, stepsMap := range s.stats.data {
			if len(req.StepIds) > 0 {
				stepIDSet := make(map[int32]struct{}, len(req.StepIds))
				for _, id := range req.StepIds {
					stepIDSet[id] = struct{}{}
				}
				for stepID := range stepsMap {
					if _, found := stepIDSet[stepID]; found {
						includedSteps = append(includedSteps, stepKey{wfID, stepID})
					}
				}
			} else {
				for stepID := range stepsMap {
					includedSteps = append(includedSteps, stepKey{wfID, stepID})
				}
			}
		}
	}

	if len(includedSteps) == 0 {
		return &pb.StepStatsResponse{Stats: nil}, nil
	}

	// Fetch step names in a single query
	stepIDs := make([]int32, 0, len(includedSteps))
	stepIDSet := make(map[int32]struct{}, len(includedSteps))
	for _, sk := range includedSteps {
		if _, exists := stepIDSet[sk.stepID]; !exists {
			stepIDSet[sk.stepID] = struct{}{}
			stepIDs = append(stepIDs, sk.stepID)
		}
	}

	stepNames := make(map[int32]string)
	if len(stepIDs) > 0 {
		placeholders := make([]string, len(stepIDs))
		args := make([]interface{}, len(stepIDs))
		for i, id := range stepIDs {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		query := fmt.Sprintf(`SELECT step_id, step_name FROM step WHERE step_id IN (%s)`, strings.Join(placeholders, ","))
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query step names: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var stepID int32
			var stepName sql.NullString
			if err := rows.Scan(&stepID, &stepName); err != nil {
				return nil, fmt.Errorf("failed to scan step name: %w", err)
			}
			if stepName.Valid {
				stepNames[stepID] = stepName.String
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating step names: %w", err)
		}
	}

	// Build output stats slice
	out := make([]*pb.StepStats, 0, len(includedSteps))
	for _, sk := range includedSteps {
		stepAgg := s.stats.data[sk.workflowID][sk.stepID]

		// Compute Running accumulator on-the-fly
		nowTime := time.Now()
		var count int32
		var sum, min, max float64
		for tid := range stepAgg.RunningTasks {
			if startedAt, ok := stepAgg.RunningTasks[tid]; ok {
				dur := nowTime.Sub(startedAt)
				count++
				sum += dur.Seconds()
				if min == 0 || dur.Seconds() < min {
					min = dur.Seconds()
				}
				if dur.Seconds() > max {
					max = dur.Seconds()
				}
			}
		}
		if count == 0 {
			min = 0
			max = 0
		}

		stats := &pb.StepStats{
			StepId:            sk.stepID,
			StepName:          stepNames[sk.stepID],
			TotalTasks:        stepAgg.Total,
			WaitingTasks:      stepAgg.Waiting,
			PendingTasks:      stepAgg.Pending,
			AcceptedTasks:     stepAgg.Accepted,
			RunningTasks:      stepAgg.Running,
			UploadingTasks:    stepAgg.Uploading,
			SuccessfulTasks:   stepAgg.Succeeded,
			FailedTasks:       stepAgg.Failed,
			ReallyFailedTasks: stepAgg.ReallyFailed,
			Download: &pb.Accum{
				Count: stepAgg.Download.Count,
				Sum:   float32(stepAgg.Download.Sum),
				Min:   float32(stepAgg.Download.Min),
				Max:   float32(stepAgg.Download.Max),
			},
			Upload: &pb.Accum{
				Count: stepAgg.Upload.Count,
				Sum:   float32(stepAgg.Upload.Sum),
				Min:   float32(stepAgg.Upload.Min),
				Max:   float32(stepAgg.Upload.Max),
			},
			SuccessRun: &pb.Accum{
				Count: stepAgg.SuccessRun.Count,
				Sum:   float32(stepAgg.SuccessRun.Sum),
				Min:   float32(stepAgg.SuccessRun.Min),
				Max:   float32(stepAgg.SuccessRun.Max),
			},
			FailedRun: &pb.Accum{
				Count: stepAgg.FailRun.Count,
				Sum:   float32(stepAgg.FailRun.Sum),
				Min:   float32(stepAgg.FailRun.Min),
				Max:   float32(stepAgg.FailRun.Max),
			},
			RunningRun: &pb.Accum{
				Count: count,
				Sum:   float32(sum),
				Min:   float32(min),
				Max:   float32(max),
			},
			StartTime:     stepAgg.StartTime,
			EndTime:       stepAgg.EndTime,
			StatsEvalTime: int32(now.Unix()),
		}

		out = append(out, stats)
	}

	// Sort output by StepId ascending
	sort.Slice(out, func(i, j int) bool {
		return out[i].StepId < out[j].StepId
	})

	return &pb.StepStatsResponse{Stats: out}, nil
}

// --- Snapshot dispatch (see specs/task_transitions.md) --------------
//
// The WS `step-stats` topic carries snapshots of step counters, not
// per-transition deltas. Every write path that mutates the aggregator
// (Adjust, MarkDirty from UpdateTaskStatus, Reconcile) adds the affected
// step to the `dirty` set. A background goroutine reads and clears
// that set every 100 ms, snapshots each dirty step under the mutex,
// then emits one WS message per workflow (containing every dirty step
// for that workflow) outside the mutex.
//
// Idle workflows produce zero traffic. Reconnect / initial hydration
// still comes via `GetStepStats` for now — a follow-up will add a
// welcome-snapshot on subscribe.

// StepCounterSnapshot is the wire shape shipped over WS. Field names
// match the UI's step-row fields so the client can `Object.assign` and
// be done. Kept flat (not nested under a `counters:` map) to keep the
// payload small and the client-side reactivity trivial.
type StepCounterSnapshot struct {
	StepId          int32  `json:"stepId"`
	TotalTasks      int32  `json:"totalTasks"`
	WaitingTasks    int32  `json:"waitingTasks"`
	PendingTasks    int32  `json:"pendingTasks"`
	AcceptedTasks   int32  `json:"acceptedTasks"`
	RunningTasks    int32  `json:"runningTasks"`
	UploadingTasks  int32  `json:"uploadingTasks"`
	SuccessfulTasks int32  `json:"successfulTasks"`
	FailedTasks     int32  `json:"failedTasks"`      // hidden retried parents
	ReallyFailedTasks int32 `json:"reallyFailedTasks"` // visible F only
	RetryingTasks   int32  `json:"retryingTasks"`
	// Accumulators — sent as flat min/max/avg for the UI's duration
	// column. UI already renders these; format matches today's
	// StepStats proto response.
	SuccessRun stepRunStats `json:"successRun"`
	FailedRun  stepRunStats `json:"failedRun"`
	RunningRun stepRunStats `json:"runningRun"`
	Download   stepRunStats `json:"download"`
	Upload     stepRunStats `json:"upload"`
	StartTime  *int64       `json:"startTime,omitempty"`
	EndTime    *int64       `json:"endTime,omitempty"`
}

type stepRunStats struct {
	Count   int32   `json:"count"`
	Average float32 `json:"average"`
	Min     float32 `json:"min"`
	Max     float32 `json:"max"`
}

func snapshotFromAgg(stepID int32, agg *StepAgg, now time.Time) StepCounterSnapshot {
	total := agg.Waiting + agg.Pending + agg.Accepted + agg.Running +
		agg.Uploading + agg.Succeeded + agg.ReallyFailed
	snap := StepCounterSnapshot{
		StepId:            stepID,
		TotalTasks:        total,
		WaitingTasks:      agg.Waiting,
		PendingTasks:      agg.Pending,
		AcceptedTasks:     agg.Accepted,
		RunningTasks:      agg.Running,
		UploadingTasks:    agg.Uploading,
		SuccessfulTasks:   agg.Succeeded,
		FailedTasks:       agg.Failed,
		ReallyFailedTasks: agg.ReallyFailed,
		RetryingTasks:     agg.Retrying,
		SuccessRun:        toStepRunStats(agg.SuccessRun),
		FailedRun:         toStepRunStats(agg.FailRun),
		Download:          toStepRunStats(agg.Download),
		Upload:            toStepRunStats(agg.Upload),
		StartTime:         agg.StartTime,
		EndTime:           agg.EndTime,
	}
	// RunningRun is derived from RunningTasks (start times → current
	// elapsed durations). Matches the existing GetStepStats logic.
	if len(agg.RunningTasks) > 0 {
		var sum, minV, maxV float64
		minV = math.MaxFloat64
		for _, start := range agg.RunningTasks {
			d := now.Sub(start).Seconds()
			if d < 0 {
				d = 0
			}
			sum += d
			if d < minV {
				minV = d
			}
			if d > maxV {
				maxV = d
			}
		}
		count := int32(len(agg.RunningTasks))
		avg := float32(sum / float64(count))
		snap.RunningRun = stepRunStats{Count: count, Average: avg, Min: float32(minV), Max: float32(maxV)}
	}
	return snap
}

func toStepRunStats(a Accumulator) stepRunStats {
	if a.Count == 0 {
		return stepRunStats{}
	}
	minV := a.Min
	if minV == math.MaxFloat64 {
		minV = 0
	}
	return stepRunStats{
		Count:   a.Count,
		Average: float32(a.Sum / float64(a.Count)),
		Min:     float32(minV),
		Max:     float32(a.Max),
	}
}

// FlushOnce collects the current dirty set under the mutex, snapshots
// each dirty step's counters, clears the set, then emits one WS message
// per workflow OUTSIDE the mutex. Zero traffic when nothing is dirty.
func (a *StepStatsAgg) FlushOnce() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if len(a.dirty) == 0 {
		a.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	// Group dirty steps by workflow so we emit one WS message per
	// workflow topic, matching the existing subscription shape.
	byWorkflow := make(map[int32][]StepCounterSnapshot)
	for key := range a.dirty {
		wf := a.data[key.workflowID]
		if wf == nil {
			continue
		}
		agg := wf[key.stepID]
		if agg == nil {
			continue
		}
		byWorkflow[key.workflowID] = append(
			byWorkflow[key.workflowID],
			snapshotFromAgg(key.stepID, agg, now),
		)
	}
	// Clear the set atomically with the read so the next tick starts
	// fresh — any transition arriving between the unlock and the
	// emit will mark itself dirty and be picked up next tick.
	a.dirty = nil
	a.mu.Unlock()

	// Emit outside the mutex — websocket.Broadcast can block if a
	// client's send buffer is full, and we don't want that to serialize
	// aggregator updates.
	for wfID, steps := range byWorkflow {
		websocket.EmitWS("step-stats", wfID, "snapshot", struct {
			Steps []StepCounterSnapshot `json:"steps"`
		}{Steps: steps})
	}
}

// FlushLoop drives FlushOnce on a fixed cadence. Caller wires stop.
func (a *StepStatsAgg) FlushLoop(interval time.Duration, stop <-chan struct{}) {
	if a == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.FlushOnce()
		case <-stop:
			// Final flush on shutdown so anything pending gets one
			// last chance to reach the UI before the WS layer closes.
			a.FlushOnce()
			return
		}
	}
}
