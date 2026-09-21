package server

import (
	"context"
	"database/sql"
	"log"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// workerStatsHistorySweepInterval is the tick period for the retention
// sweeper. One hour is fine: rows expire on the scale of
// worker_stats_retention_hours (defaults to 168 = 7 days), so a
// sub-hour sweeper only adds DB churn without improving accuracy.
const workerStatsHistorySweepInterval = 1 * time.Hour

// persistWorkerStatsSample writes one row to worker_stats_history.
// Called from PingAndTakeNewTasks in a goroutine so the ping handler
// isn't blocked on the DB roundtrip. Fetches the worker's current
// step_id inside the same INSERT via a subselect — no separate join.
//
// Errors are logged and swallowed: worker stats history is a
// diagnostic, not part of the scheduling loop; a hiccup on this
// insert must never affect the ping response.
func (s *taskQueueServer) persistWorkerStatsSample(workerID int32, stats *pb.WorkerStats) {
	if stats == nil {
		return
	}
	// Snapshot pointer values so the goroutine doesn't race against the
	// caller reusing the WorkerStats struct.
	cpuP := nullFloat32ForStats(stats.PeakCpuPercent)
	memP := nullFloat32ForStats(stats.PeakMemPercent)
	ioP := nullFloat32ForStats(stats.PeakIowaitPercent)
	diskP := nullFloat32ForStats(stats.PeakDiskPercent)
	// Current values are proto scalars, not optional — 0 is a valid
	// reading (idle worker) so we always write them. A client that
	// doesn't send stats never reaches here (guarded by the caller).
	cpu := stats.GetCpuUsagePercent()
	mem := stats.GetMemUsagePercent()
	iowait := stats.GetIowaitPercent()
	effConc := stats.GetEffectiveConcurrency()
	running := stats.GetRunningTasks()
	throttleTS := stats.GetLastThrottleAt()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var throttleArg interface{}
		if throttleTS > 0 {
			throttleArg = time.Unix(throttleTS, 0)
		}
		// step_id snapshot: fetch what the worker is serving right now.
		// A separate SELECT lets us leave the column NULL on a legacy
		// row (no worker record for this ID), which is what
		// ON DELETE SET NULL would produce anyway when the worker is
		// eventually deleted.
		var stepID sql.NullInt32
		_ = s.db.QueryRowContext(ctx,
			`SELECT step_id FROM worker WHERE worker_id = $1`, workerID,
		).Scan(&stepID)
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO worker_stats_history (
				worker_id, sampled_at, step_id,
				cpu_percent, mem_percent, iowait_percent,
				peak_cpu_percent, peak_mem_percent, peak_iowait_percent,
				peak_disk_percent,
				effective_concurrency, running_tasks, last_throttle_at
			) VALUES (
				$1, NOW(), $2,
				$3, $4, $5,
				$6, $7, $8,
				$9,
				$10, $11, $12
			)
		`,
			workerID, stepID,
			cpu, mem, iowait,
			cpuP, memP, ioP,
			diskP,
			effConc, running, throttleArg,
		)
		if err != nil {
			// A per-worker duplicate (worker_id, sampled_at) collision is
			// impossible in practice (NOW() has microsecond precision and
			// no worker pings twice in the same microsecond) but a
			// startup burst of catch-up pings could in theory hit it. If
			// we ever start seeing this in the wild, switch to a serial
			// bigserial PK — for now, log and move on.
			log.Printf("⚠️ worker_stats_history: insert for worker %d failed: %v", workerID, err)
		}
	}()
}

// nullFloat32ForStats translates an optional proto float pointer into
// a driver-friendly value. Nil pointer → nil (NULL in DB). A concrete
// pointer, even to 0.0, is preserved as-is: the client only sets the
// pointer when its peak sampler actually observed a value, so 0.0
// means "the sampler ran and saw zero", not "no data".
func nullFloat32ForStats(p *float32) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

// runWorkerStatsHistorySweep runs on an hourly ticker while the server
// is up, deleting rows past the retention window. Uses a bounded
// per-sweep DELETE so an overgrown table doesn't lock the server for
// tens of seconds on the first sweep after enabling the feature.
func (s *taskQueueServer) runWorkerStatsHistorySweep(stopChan <-chan struct{}) {
	hours := s.cfg.Scitq.WorkerStatsRetentionHours
	if hours <= 0 {
		return
	}
	// Fire once at startup after a short delay, then on the ticker —
	// so a server that has been down for longer than the retention
	// window drops the stale rows promptly.
	initialDelay := time.NewTimer(30 * time.Second)
	defer initialDelay.Stop()
	ticker := time.NewTicker(workerStatsHistorySweepInterval)
	defer ticker.Stop()

	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		// LIMIT via a subquery keeps a first-sweep of a large backlog
		// bounded per iteration. Repeat until the affected count comes
		// back below the batch size — then the table has caught up.
		const batchSize = 50000
		for {
			res, err := s.db.ExecContext(ctx, `
				DELETE FROM worker_stats_history
				 WHERE ctid IN (
					SELECT ctid FROM worker_stats_history
					 WHERE sampled_at < NOW() - ($1 || ' hours')::interval
					 LIMIT $2
				 )
			`, hours, batchSize)
			if err != nil {
				log.Printf("⚠️ worker_stats_history: sweep failed: %v", err)
				return
			}
			n, _ := res.RowsAffected()
			if n < batchSize {
				return
			}
		}
	}

	select {
	case <-stopChan:
		return
	case <-initialDelay.C:
		sweep()
	}
	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			sweep()
		}
	}
}
