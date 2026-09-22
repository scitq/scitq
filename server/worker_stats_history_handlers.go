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
	// bucket_seconds triggers server-side downsampling: rows are
	// GROUPed by (worker_id, floor(sampled_at / bucket_seconds))
	// with MAX over peaks + AVG over current gauges. Clamp to a
	// sensible range so a wrong-scale value (0, seconds-as-hours)
	// doesn't produce a useless output.
	bucket := int32(0)
	if req.BucketSeconds != nil && *req.BucketSeconds > 0 {
		bucket = *req.BucketSeconds
		if bucket < 1 {
			bucket = 1
		}
		if bucket > 3600 {
			bucket = 3600
		}
	}
	// fields selector: mask enforced at pb assembly time so SQL stays
	// symmetric; empty/nil mask means "everything".
	mask := parseHistoryFieldMask(req.Fields)

	join, where, args := buildHistoryWhere(req)
	args = append(args, limit+1) // +1 so we can detect truncation
	var query string
	if bucket > 0 {
		// Bucketed shape: sampled_at reports the bucket's start.
		// to_timestamp(floor(epoch / bucket) * bucket) gives a stable
		// bucket boundary regardless of clock skew. peak_* use MAX
		// (peaks are the whole point of the feature — they must not
		// be diluted by AVG); current gauges use AVG for meaningful
		// plots; running_tasks + effective_concurrency use MAX; step_id
		// and last_throttle_at aggregate to MAX as a stable choice
		// (both are per-worker markers, not values that average
		// naturally).
		query = fmt.Sprintf(`
			SELECT h.worker_id, w.worker_name,
			       (EXTRACT(EPOCH FROM date_bin(interval '%d seconds', h.sampled_at, to_timestamp(0))) * 1000)::bigint AS bucket_ms,
			       MAX(h.step_id),
			       AVG(h.cpu_percent), AVG(h.mem_percent), AVG(h.iowait_percent),
			       MAX(h.peak_cpu_percent), MAX(h.peak_mem_percent), MAX(h.peak_iowait_percent),
			       MAX(h.peak_disk_percent),
			       MAX(h.effective_concurrency), MAX(h.running_tasks),
			       COALESCE(MAX(EXTRACT(EPOCH FROM h.last_throttle_at))::bigint, 0)
			  FROM worker_stats_history h
			  JOIN worker w ON w.worker_id = h.worker_id
		`, bucket) + join + where + fmt.Sprintf(`
			 GROUP BY h.worker_id, w.worker_name, bucket_ms
			 ORDER BY h.worker_id, bucket_ms ASC
			 LIMIT $%d
		`, len(args))
	} else {
		// Raw per-ping shape. sampled_at is unix MILLIseconds so two
		// pings within the same second don't collide in the returned
		// key.
		query = `
			SELECT h.worker_id, w.worker_name,
			       (EXTRACT(EPOCH FROM h.sampled_at) * 1000)::bigint AS sampled_at_ms,
			       h.step_id,
			       h.cpu_percent, h.mem_percent, h.iowait_percent,
			       h.peak_cpu_percent, h.peak_mem_percent, h.peak_iowait_percent,
			       h.peak_disk_percent,
			       h.effective_concurrency, h.running_tasks,
			       COALESCE(EXTRACT(EPOCH FROM h.last_throttle_at)::bigint, 0)
			  FROM worker_stats_history h
			  JOIN worker w ON w.worker_id = h.worker_id
		` + join + where + fmt.Sprintf(`
			 ORDER BY h.sampled_at ASC
			 LIMIT $%d
		`, len(args))
	}

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
			sampledAtMs                                       int64
			stepID                                            sql.NullInt32
			cpu, mem, iowait                                  sql.NullFloat64
			peakCPU, peakMem, peakIowait, peakDisk            sql.NullFloat64
			effConc, running                                  sql.NullInt32
			lastThrottle                                      int64
		)
		if err := rows.Scan(
			&workerID, &workerName, &sampledAtMs, &stepID,
			&cpu, &mem, &iowait,
			&peakCPU, &peakMem, &peakIowait, &peakDisk,
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
			SampledAt:  sampledAtMs,
		}
		if stepID.Valid && mask.step {
			v := stepID.Int32
			sample.StepId = &v
		}
		if cpu.Valid && mask.cpu {
			v := float32(cpu.Float64)
			sample.CpuPercent = &v
		}
		if mem.Valid && mask.mem {
			v := float32(mem.Float64)
			sample.MemPercent = &v
		}
		if iowait.Valid && mask.iowait {
			v := float32(iowait.Float64)
			sample.IowaitPercent = &v
		}
		if peakCPU.Valid && mask.peakCPU {
			v := float32(peakCPU.Float64)
			sample.PeakCpuPercent = &v
		}
		if peakMem.Valid && mask.peakMem {
			v := float32(peakMem.Float64)
			sample.PeakMemPercent = &v
		}
		if peakIowait.Valid && mask.peakIowait {
			v := float32(peakIowait.Float64)
			sample.PeakIowaitPercent = &v
		}
		if peakDisk.Valid && mask.peakDisk {
			v := float32(peakDisk.Float64)
			sample.PeakDiskPercent = &v
		}
		if effConc.Valid && mask.effConc {
			v := effConc.Int32
			sample.EffectiveConcurrency = &v
		}
		if running.Valid && mask.running {
			v := running.Int32
			sample.RunningTasks = &v
		}
		if lastThrottle > 0 && mask.lastThrottle {
			sample.LastThrottleAt = &lastThrottle
		}
		res.Samples = append(res.Samples, sample)
	}
	// Empty result: tell the caller WHY. Distinguishes "no samples in
	// window" from "the id filter matched nothing that exists" — the
	// latter is usually a typo or a filter that predates the retention
	// window.
	if len(res.Samples) == 0 {
		reason := s.diagnoseEmptyHistory(ctx, req)
		res.Reason = &reason
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
		       (EXTRACT(EPOCH FROM MIN(h.sampled_at)) * 1000)::bigint,
		       (EXTRACT(EPOCH FROM MAX(h.sampled_at)) * 1000)::bigint,
		       -- The peak columns are the primary max source. When a
		       -- client didn't send peaks (older build, or the sampler
		       -- hadn't run yet), the current-value gauge is used as a
		       -- fallback so the max never regresses to NULL just
		       -- because the peaks column has less coverage.
		       MAX(GREATEST(COALESCE(h.peak_cpu_percent,    0), COALESCE(h.cpu_percent,    0))),
		       MAX(GREATEST(COALESCE(h.peak_mem_percent,    0), COALESCE(h.mem_percent,    0))),
		       MAX(GREATEST(COALESCE(h.peak_iowait_percent, 0), COALESCE(h.iowait_percent, 0))),
		       -- Disk has no current-value column on the history row
		       -- (the live gauge is a per-disk list, not a scalar);
		       -- the sampler's per-tick MAX is the only source. Older
		       -- clients without a disk sampler yield NULL here.
		       MAX(h.peak_disk_percent),
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

	res := &pb.WorkerStatsSummary{
		Entries: make([]*pb.WorkerStatsSummaryEntry, 0, 4),
	}
	for rows.Next() {
		var (
			workerID                            int32
			workerName                          string
			count                               int32
			firstEpoch, lastEpoch               int64
			maxCPU, maxMem, maxIowait, maxDisk  sql.NullFloat64
			avgCPU, avgMem, avgIowait           sql.NullFloat64
			maxRunning                          sql.NullInt32
		)
		if err := rows.Scan(
			&workerID, &workerName, &count, &firstEpoch, &lastEpoch,
			&maxCPU, &maxMem, &maxIowait, &maxDisk,
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
		setF(&entry.MaxDiskPercent, maxDisk)
		setF(&entry.AvgCpuPercent, avgCPU)
		setF(&entry.AvgMemPercent, avgMem)
		setF(&entry.AvgIowaitPercent, avgIowait)
		if maxRunning.Valid {
			v := maxRunning.Int32
			entry.MaxRunningTasks = &v
		}
		res.Entries = append(res.Entries, entry)
	}
	if len(res.Entries) == 0 {
		reason := s.diagnoseEmptyHistory(ctx, req)
		res.Reason = &reason
	}
	return res, nil
}

// historyFieldMask lists which fields should be populated on each
// returned sample. "step" gates step_id, "lastThrottle" gates
// last_throttle_at, others match the sample field names. All-true is
// the default when the caller sends no fields (backwards compat).
type historyFieldMask struct {
	step         bool
	cpu          bool
	mem          bool
	iowait       bool
	disk         bool
	peakCPU      bool
	peakMem      bool
	peakIowait   bool
	peakDisk     bool
	effConc      bool
	running      bool
	lastThrottle bool
}

func (m historyFieldMask) allTrue() historyFieldMask {
	return historyFieldMask{
		step: true, cpu: true, mem: true, iowait: true, disk: true,
		peakCPU: true, peakMem: true, peakIowait: true, peakDisk: true,
		effConc: true, running: true, lastThrottle: true,
	}
}

// parseHistoryFieldMask turns the request's `fields` selector into a
// mask. Empty / nil input → all-true (existing behaviour). Unknown
// names are silently ignored so a typo on the client side never fails
// the request — it just yields a smaller response, which is easy to
// spot. `step` and `worker_id`/`worker_name`/`sampled_at` are always
// present (identity/time fields have no useful "off" semantics).
func parseHistoryFieldMask(fields []string) historyFieldMask {
	if len(fields) == 0 {
		return historyFieldMask{}.allTrue()
	}
	m := historyFieldMask{}
	// step_id, worker_name, sampled_at aren't gated — they're always on.
	// The peak_* aliases mirror the JSON field names on
	// WorkerStatsHistorySample; the "disk" plain form covers the
	// (not yet present) current-value disk column so the API doesn't
	// need to change when it lands.
	for _, f := range fields {
		switch f {
		case "step", "step_id":
			m.step = true
		case "cpu", "cpu_percent":
			m.cpu = true
		case "mem", "mem_percent":
			m.mem = true
		case "iowait", "iowait_percent":
			m.iowait = true
		case "disk", "disk_percent":
			m.disk = true
		case "peak_cpu", "peak_cpu_percent":
			m.peakCPU = true
		case "peak_mem", "peak_mem_percent":
			m.peakMem = true
		case "peak_iowait", "peak_iowait_percent":
			m.peakIowait = true
		case "peak_disk", "peak_disk_percent":
			m.peakDisk = true
		case "effective_concurrency":
			m.effConc = true
		case "running_tasks":
			m.running = true
		case "last_throttle_at":
			m.lastThrottle = true
		}
	}
	// step_id is a locator, not a "value" — if a caller went to the
	// trouble of naming any field, they probably still want to know
	// which step the sample belonged to. Force-on so the reduced
	// payload stays useful for any workflow-scoped query.
	m.step = true
	return m
}

// diagnoseEmptyHistory explains why history / summary came back empty.
// The distinction matters — an operator misdiagnoses "no samples" as
// "the feature doesn't work here" when the real cause is a typo'd
// worker_id or a filter that predates the retention window.
//
// Runs one small existence probe per id-shaped filter. Cheap: the
// probe is a single indexed lookup per id, only fires on empty
// results, and only when at least one id filter was set. Returns
// "no_samples" as the default — the filter parsed fine but nothing
// matched the time predicate.
func (s *taskQueueServer) diagnoseEmptyHistory(ctx context.Context, req *pb.WorkerStatsHistoryFilter) string {
	if req.WorkerId != nil {
		var exists bool
		_ = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM worker WHERE worker_id = $1)`, *req.WorkerId,
		).Scan(&exists)
		if !exists {
			return "unknown_worker"
		}
	}
	if req.StepId != nil {
		var exists bool
		_ = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM step WHERE step_id = $1)`, *req.StepId,
		).Scan(&exists)
		if !exists {
			return "unknown_step"
		}
	}
	if req.WorkflowId != nil {
		var exists bool
		_ = s.db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM workflow WHERE workflow_id = $1)`, *req.WorkflowId,
		).Scan(&exists)
		if !exists {
			return "unknown_workflow"
		}
	}
	return "no_samples"
}

