package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultWorkerStatsHistoryLimit caps the row count returned by
// ListWorkerStatsHistory when the caller doesn't set an explicit limit.
// 10k is roughly one worker × 14 hours at a 5-second ping cadence — a
// large enough window that most single-worker debug queries fit, small
// enough that a range-over-a-whole-fleet-for-a-week query is truncated
// rather than DoS-ing the caller.
const defaultWorkerStatsHistoryLimit = 10000

// maxWorkerStatsHistoryLimit hard-caps any caller-supplied limit so a
// bogus value doesn't run away with server memory. 200k rows ≈ 20 MB
// on the wire; comfortably absorbable, and matches the retention-side
// batch size for symmetry.
const maxWorkerStatsHistoryLimit = 200000

// filterEmpty returns true when the filter has no discriminant set. The
// history table is unbounded until the retention sweep hits, so a
// filterless read would page through days of data across every worker —
// almost never what an operator wants. Rejecting this shape at the RPC
// boundary is friendlier than trying to guess a default window.
func filterEmpty(f *pb.WorkerStatsHistoryFilter) bool {
	if f == nil {
		return true
	}
	return f.WorkflowId == nil && f.WorkerId == nil && f.StepId == nil &&
		f.StartEpoch == nil && f.EndEpoch == nil
}

// buildHistoryWhere returns a SQL WHERE clause and its args, from the
// filter. The workflow_id path joins through step; when no workflow_id
// is set, no join is needed (worker_id / step_id / time cover the
// common single-worker debug case cheaply). Returns the join clause
// separately so the summary query can share the same predicate.
func buildHistoryWhere(f *pb.WorkerStatsHistoryFilter) (join string, where string, args []any) {
	var conds []string
	if f.WorkflowId != nil {
		// The history row's step_id is a snapshot of the worker's
		// assignment at ping time (see persistWorkerStatsSample). Join
		// to step to resolve which workflow that step belongs to; a
		// row whose worker was idle at ping time has step_id=NULL and
		// is therefore excluded from a workflow-scoped query — the
		// worker was demonstrably not serving that workflow at that
		// instant.
		join = " JOIN step s ON s.step_id = h.step_id "
		conds = append(conds, fmt.Sprintf("s.workflow_id = $%d", len(args)+1))
		args = append(args, *f.WorkflowId)
	}
	if f.WorkerId != nil {
		conds = append(conds, fmt.Sprintf("h.worker_id = $%d", len(args)+1))
		args = append(args, *f.WorkerId)
	}
	if f.StepId != nil {
		conds = append(conds, fmt.Sprintf("h.step_id = $%d", len(args)+1))
		args = append(args, *f.StepId)
	}
	if f.StartEpoch != nil {
		conds = append(conds, fmt.Sprintf("h.sampled_at >= to_timestamp($%d)", len(args)+1))
		args = append(args, *f.StartEpoch)
	}
	if f.EndEpoch != nil {
		conds = append(conds, fmt.Sprintf("h.sampled_at <= to_timestamp($%d)", len(args)+1))
		args = append(args, *f.EndEpoch)
	}
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	return
}

// ListWorkerStatsHistory returns raw samples matching the filter, oldest
// first, capped at the requested limit (default 10k, hard cap 200k).
// The `dropped` field in the response is non-zero when the cap was hit
// — the caller can retry with a tighter time range to get complete
// coverage.
func (s *taskQueueServer) ListWorkerStatsHistory(ctx context.Context, req *pb.WorkerStatsHistoryFilter) (*pb.WorkerStatsHistoryList, error) {
	if s.cfg.Scitq.WorkerStatsRetentionHours <= 0 {
		return nil, status.Error(codes.FailedPrecondition,
			"worker stats history is disabled (scitq.worker_stats_retention_hours == 0)")
	}
	if filterEmpty(req) {
		return nil, status.Error(codes.InvalidArgument,
			"WorkerStatsHistoryFilter requires at least one of workflow_id, worker_id, step_id, start_epoch, end_epoch")
	}

	limit := int32(defaultWorkerStatsHistoryLimit)
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	if limit > maxWorkerStatsHistoryLimit {
		limit = maxWorkerStatsHistoryLimit
	}

	join, where, args := buildHistoryWhere(req)
	args = append(args, limit+1) // +1 so we can detect truncation
	query := `
		SELECT h.worker_id, w.worker_name,
		       EXTRACT(EPOCH FROM h.sampled_at)::bigint,
		       h.step_id,
		       h.cpu_percent, h.mem_percent, h.iowait_percent,
		       h.peak_cpu_percent, h.peak_mem_percent, h.peak_iowait_percent,
		       h.effective_concurrency, h.running_tasks,
		       COALESCE(EXTRACT(EPOCH FROM h.last_throttle_at)::bigint, 0)
		  FROM worker_stats_history h
		  JOIN worker w ON w.worker_id = h.worker_id
	` + join + where + fmt.Sprintf(`
		 ORDER BY h.sampled_at ASC
		 LIMIT $%d
	`, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("worker stats history query: %w", err)
	}
	defer rows.Close()

	res := &pb.WorkerStatsHistoryList{Samples: make([]*pb.WorkerStatsHistorySample, 0, limit)}
	for rows.Next() {
		var (
			workerID                                          int32
			workerName                                        string
			sampledAt                                         int64
			stepID                                            sql.NullInt32
			cpu, mem, iowait                                  sql.NullFloat64
			peakCPU, peakMem, peakIowait                      sql.NullFloat64
			effConc, running                                  sql.NullInt32
			lastThrottle                                      int64
		)
		if err := rows.Scan(
			&workerID, &workerName, &sampledAt, &stepID,
			&cpu, &mem, &iowait,
			&peakCPU, &peakMem, &peakIowait,
			&effConc, &running, &lastThrottle,
		); err != nil {
			continue
		}
		if int32(len(res.Samples)) >= limit {
			res.Dropped = 1 // just a signal; exact count not tracked
			break
		}
		sample := &pb.WorkerStatsHistorySample{
			WorkerId:   workerID,
			WorkerName: workerName,
			SampledAt:  sampledAt,
		}
		if stepID.Valid {
			v := stepID.Int32
			sample.StepId = &v
		}
		if cpu.Valid {
			v := float32(cpu.Float64)
			sample.CpuPercent = &v
		}
		if mem.Valid {
			v := float32(mem.Float64)
			sample.MemPercent = &v
		}
		if iowait.Valid {
			v := float32(iowait.Float64)
			sample.IowaitPercent = &v
		}
		if peakCPU.Valid {
			v := float32(peakCPU.Float64)
			sample.PeakCpuPercent = &v
		}
		if peakMem.Valid {
			v := float32(peakMem.Float64)
			sample.PeakMemPercent = &v
		}
		if peakIowait.Valid {
			v := float32(peakIowait.Float64)
			sample.PeakIowaitPercent = &v
		}
		if effConc.Valid {
			v := effConc.Int32
			sample.EffectiveConcurrency = &v
		}
		if running.Valid {
			v := running.Int32
			sample.RunningTasks = &v
		}
		if lastThrottle > 0 {
			sample.LastThrottleAt = &lastThrottle
		}
		res.Samples = append(res.Samples, sample)
	}
	return res, nil
}

// GetWorkerStatsSummary aggregates the same filter window by worker,
// returning MAX peaks + AVG of the "current" gauges + span metadata.
// This is the sweet spot for capacity questions ("did any worker in
// workflow X hit high memory?") — one row per worker, cheap to compute
// server-side, tiny payload.
func (s *taskQueueServer) GetWorkerStatsSummary(ctx context.Context, req *pb.WorkerStatsHistoryFilter) (*pb.WorkerStatsSummary, error) {
	if s.cfg.Scitq.WorkerStatsRetentionHours <= 0 {
		return nil, status.Error(codes.FailedPrecondition,
			"worker stats history is disabled (scitq.worker_stats_retention_hours == 0)")
	}
	if filterEmpty(req) {
		return nil, status.Error(codes.InvalidArgument,
			"WorkerStatsHistoryFilter requires at least one of workflow_id, worker_id, step_id, start_epoch, end_epoch")
	}

	join, where, args := buildHistoryWhere(req)
	query := `
		SELECT h.worker_id, w.worker_name,
		       COUNT(*),
		       EXTRACT(EPOCH FROM MIN(h.sampled_at))::bigint,
		       EXTRACT(EPOCH FROM MAX(h.sampled_at))::bigint,
		       -- The peak columns are the primary max source. When a
		       -- client didn't send peaks (older build, or the sampler
		       -- hadn't run yet), the current-value gauge is used as a
		       -- fallback so the max never regresses to NULL just
		       -- because the peaks column has less coverage.
		       MAX(GREATEST(COALESCE(h.peak_cpu_percent,    0), COALESCE(h.cpu_percent,    0))),
		       MAX(GREATEST(COALESCE(h.peak_mem_percent,    0), COALESCE(h.mem_percent,    0))),
		       MAX(GREATEST(COALESCE(h.peak_iowait_percent, 0), COALESCE(h.iowait_percent, 0))),
		       AVG(h.cpu_percent), AVG(h.mem_percent), AVG(h.iowait_percent),
		       MAX(h.running_tasks)
		  FROM worker_stats_history h
		  JOIN worker w ON w.worker_id = h.worker_id
	` + join + where + `
		 GROUP BY h.worker_id, w.worker_name
		 ORDER BY MAX(h.peak_mem_percent) DESC NULLS LAST, h.worker_id
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("worker stats summary query: %w", err)
	}
	defer rows.Close()

	res := &pb.WorkerStatsSummary{}
	for rows.Next() {
		var (
			workerID                       int32
			workerName                     string
			count                          int32
			firstEpoch, lastEpoch          int64
			maxCPU, maxMem, maxIowait      sql.NullFloat64
			avgCPU, avgMem, avgIowait      sql.NullFloat64
			maxRunning                     sql.NullInt32
		)
		if err := rows.Scan(
			&workerID, &workerName, &count, &firstEpoch, &lastEpoch,
			&maxCPU, &maxMem, &maxIowait,
			&avgCPU, &avgMem, &avgIowait,
			&maxRunning,
		); err != nil {
			continue
		}
		entry := &pb.WorkerStatsSummaryEntry{
			WorkerId:      workerID,
			WorkerName:    workerName,
			SampleCount:   count,
			FirstSampleAt: firstEpoch,
			LastSampleAt:  lastEpoch,
		}
		setF := func(dst **float32, src sql.NullFloat64) {
			if src.Valid {
				v := float32(src.Float64)
				*dst = &v
			}
		}
		setF(&entry.MaxCpuPercent, maxCPU)
		setF(&entry.MaxMemPercent, maxMem)
		setF(&entry.MaxIowaitPercent, maxIowait)
		setF(&entry.AvgCpuPercent, avgCPU)
		setF(&entry.AvgMemPercent, avgMem)
		setF(&entry.AvgIowaitPercent, avgIowait)
		if maxRunning.Valid {
			v := maxRunning.Int32
			entry.MaxRunningTasks = &v
		}
		res.Entries = append(res.Entries, entry)
	}
	return res, nil
}

