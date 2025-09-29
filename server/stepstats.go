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
)

// NewStepAgg creates a new StepAgg with sensible defaults.
func NewStepAgg() *StepAgg {
	return &StepAgg{
		RunningTasks: make(map[int32]time.Time),
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
	Total        int32

	// Accumulators for durations
	Download   Accumulator
	Upload     Accumulator
	SuccessRun Accumulator
	FailRun    Accumulator

	// RunningTasks maps taskID to start time
	RunningTasks map[int32]time.Time

	// Time bounds (epoch seconds)
	StartTime *int32
	EndTime   *int32
}

// StepStatsAgg holds in-memory aggregated statistics for steps in workflows.
type StepStatsAgg struct {
	mu   sync.Mutex
	data map[int32]map[int32]*StepAgg // workflow_id -> step_id -> *StepAgg
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
			-- totals and status counters (count only non-null task rows)
			COUNT(t.task_id) AS total,
			COUNT(*) FILTER (WHERE t.status = 'W') AS waiting,
			COUNT(*) FILTER (WHERE t.status IN ('P','I')) AS pending,
			COUNT(*) FILTER (WHERE t.status IN ('C','D','O')) AS accepted,
			COUNT(*) FILTER (WHERE t.status = 'R') AS running,
			COUNT(*) FILTER (WHERE t.status IN ('U','V')) AS uploading,
			COUNT(*) FILTER (WHERE t.status = 'S') AS succeeded,
			COUNT(*) FILTER (WHERE t.status = 'F' AND t.hidden) AS failed,
			COUNT(*) FILTER (WHERE t.status = 'F' AND NOT t.hidden) AS reallyfailed,

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

			-- running tasks as separate arrays
			array_agg(t.task_id) FILTER (WHERE t.status = 'R' AND t.run_started_at IS NOT NULL) AS running_task_ids,
			array_agg(EXTRACT(EPOCH FROM t.run_started_at)::bigint) FILTER (WHERE t.status = 'R' AND t.run_started_at IS NOT NULL) AS running_task_times
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
			succeeded, failed, reallyfailed                       int32
			dlSum, dlMin, dlMax, upSum, upMin, upMax              float64
			dlCount, upCount                                      int32
			runSCount                                             int32
			runSSum, runSMin, runSMax                             float64
			runFCount                                             int32
			runFSum, runFMin, runFMax                             float64
			startEpoch, endEpoch                                  sql.NullInt64
			runningIDs                                            pq.Int64Array
			runningTimes                                          pq.Int64Array
		)
		if err := rows.Scan(
			&workflowID,
			&stepID,
			&total, &waiting, &pending, &accepted, &running, &uploading, &succeeded, &failed, &reallyfailed,
			&dlSum, &dlMin, &dlMax, &upSum, &upMin, &upMax,
			&dlCount, &upCount,
			&runSCount, &runSSum, &runSMin, &runSMax,
			&runFCount, &runFSum, &runFMin, &runFMax,
			&startEpoch, &endEpoch,
			&runningIDs,
			&runningTimes,
		); err != nil {
			log.Printf("[DEBUG] Could not parse line : %v", err)
			continue
		}

		var startPtr, endPtr *int32
		if startEpoch.Valid && startEpoch.Int64 > 0 {
			v := int32(startEpoch.Int64)
			startPtr = &v
		}
		if endEpoch.Valid && endEpoch.Int64 > 0 {
			v := int32(endEpoch.Int64)
			endPtr = &v
		}

		if agg.data[workflowID] == nil {
			agg.data[workflowID] = make(map[int32]*StepAgg)
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
