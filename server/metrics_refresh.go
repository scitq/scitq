package server

import (
	"context"
	"database/sql"
	"log"
	"math"
	"strconv"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/metrics"
)

// metricsRefreshInterval is the tick period for the refresh loop.
// 10s matches the watchdog tick — the metrics package can't move
// faster than the underlying state does, and Prometheus scrape
// intervals of 15-60s make anything under 10s wasted work.
const metricsRefreshInterval = 10 * time.Second

// deletionJobStuckThreshold: a worker-delete job (action=D) still in
// R state past this age suggests the cloud-side delete is hanging.
// Value here echoes the InstanceLimitCooldown default (10 min): if
// deletes take longer than that we already expect them to have
// finished, and Azure's own delete usually completes in <2 min.
const deletionJobStuckThreshold = 10 * time.Minute

// runMetricsRefresh mounts the refresh goroutine that periodically
// samples the DB and watchdog memory and updates the gauges in
// server/metrics. Counters are updated at their event sites (see
// recruitment/recruitment.go and reclaimOfflineWorkerTasks in
// server.go) and don't need refresh treatment.
//
// The goroutine exits when stopChan closes — same lifecycle as the
// watchdog, which is what we tie into. Doesn't panic on DB errors:
// per-metric errors are logged and that metric keeps its previous
// value (Prometheus scrapes will just observe a stale gauge, which
// the monitoring side sees via the `scitq_up` liveness check).
func (s *taskQueueServer) runMetricsRefresh(stopChan <-chan struct{}) {
	// scitq_up is set once here and left at 1 for the life of the
	// process. If the refresh goroutine or the whole server dies,
	// the metric stops updating; monitoring detects that via the
	// `up` and staleness signals on their side.
	metrics.Up.Set(1)

	ticker := time.NewTicker(metricsRefreshInterval)
	defer ticker.Stop()

	// Fire once immediately so the first scrape after startup sees
	// real numbers rather than zeros.
	s.refreshMetrics(context.Background())

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			s.refreshMetrics(context.Background())
		}
	}
}

// refreshMetrics rewrites every gauge. Each block is independent and
// logs its own failure — a single bad query shouldn't wipe every
// gauge on this tick.
func (s *taskQueueServer) refreshMetrics(ctx context.Context) {
	// Wipe per-worker series first so a just-deleted worker's stale
	// values disappear on the next scrape. Prometheus otherwise keeps
	// carrying the last observed value forward.
	metrics.ResetWorkerGauges()

	s.refreshWorkerMetrics(ctx)
	s.refreshTaskMetrics(ctx)
	s.refreshWorkflowAndJobMetrics(ctx)
	s.refreshDBPoolMetrics()
}

// refreshWorkerMetrics writes scitq_workers, scitq_worker_idle_seconds,
// and scitq_worker_active_tasks_drift.
func (s *taskQueueServer) refreshWorkerMetrics(ctx context.Context) {
	// Aggregate composition. status/provider/permanent are the
	// dimensions any dashboard slices on.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			w.status,
			COALESCE(p.provider_name || '.' || p.config_name, 'local.local') AS provider,
			w.is_permanent,
			COUNT(*)
		FROM worker w
		LEFT JOIN region rg ON rg.region_id = w.region_id
		LEFT JOIN provider p ON p.provider_id = rg.provider_id
		WHERE w.deleted_at IS NULL
		GROUP BY w.status, provider, w.is_permanent
	`)
	if err != nil {
		log.Printf("⚠️ metrics: worker aggregate query failed: %v", err)
	} else {
		for rows.Next() {
			var status, provider string
			var permanent bool
			var count int
			if err := rows.Scan(&status, &provider, &permanent, &count); err != nil {
				continue
			}
			metrics.Workers.WithLabelValues(status, provider, metrics.Bool(permanent)).Set(float64(count))
		}
		rows.Close()
	}

	// Per-worker idle-seconds + active-task drift. Names are pulled
	// once from the DB to keep the label set stable even if the
	// watchdog snapshot loses its scrap of state between ticks.
	nameByID := map[int32]string{}
	nRows, err := s.db.QueryContext(ctx, `
		SELECT worker_id, worker_name
		FROM worker
		WHERE deleted_at IS NULL
	`)
	if err != nil {
		log.Printf("⚠️ metrics: worker name query failed: %v", err)
	} else {
		for nRows.Next() {
			var id int32
			var name string
			if err := nRows.Scan(&id, &name); err == nil {
				nameByID[id] = name
			}
		}
		nRows.Close()
	}

	// DB-side active-task count per worker. Filters hidden — same as
	// FetchWorkersForWatchdog and (after the 09-10 fix) ResyncActiveTasks.
	dbActive := map[int32]int{}
	dRows, err := s.db.QueryContext(ctx, `
		SELECT worker_id, COUNT(*)
		FROM task
		WHERE status IN ('A','C','D','O','R') AND NOT hidden
		GROUP BY worker_id
	`)
	if err != nil {
		log.Printf("⚠️ metrics: db active-task query failed: %v", err)
	} else {
		for dRows.Next() {
			var id int32
			var count int
			if err := dRows.Scan(&id, &count); err == nil {
				dbActive[id] = count
			}
		}
		dRows.Close()
	}

	// Watchdog memory snapshot: idle seconds + in-memory active count
	// per tracked worker. Only workers still present in the name map
	// get a label — a stale watchdog entry for a deleted worker isn't
	// worth an alertable series.
	for _, snap := range s.watchdog.Snapshot() {
		name, ok := nameByID[snap.WorkerID]
		if !ok {
			continue
		}
		idle := snap.IdleSeconds
		if idle < 0 {
			// Never idle → NaN, Prometheus skips the series in queries.
			idle = math.NaN()
		}
		metrics.WorkerIdleSeconds.WithLabelValues(
			workerIDLabel(snap.WorkerID), name, metrics.Bool(snap.IsPermanent),
		).Set(idle)

		drift := float64(dbActive[snap.WorkerID] - snap.ActiveTasks)
		metrics.WorkerActiveTasksDrift.WithLabelValues(
			workerIDLabel(snap.WorkerID), name,
		).Set(drift)

		// Client-reported running task count — the runtime ground
		// truth from the worker's own `executingTasks` map (see
		// client.go). Independent of any DB status; a stuck hidden R
		// task cannot inflate this. Sourced from the workerStats
		// cache the ping loop populates; a worker that hasn't pinged
		// yet (or is running an old client that doesn't report the
		// field) reads as 0, which the leak-detection alert on
		// monitoring side interprets correctly ("idle AND not doing
		// anything").
		var running float64
		if v, ok := s.workerStats.Load(snap.WorkerID); ok {
			if stats, ok := v.(*pb.WorkerStats); ok {
				running = float64(stats.GetRunningTasks())
			}
		}
		metrics.WorkerRunningTasks.WithLabelValues(
			workerIDLabel(snap.WorkerID), name, metrics.Bool(snap.IsPermanent),
		).Set(running)
	}
}

// refreshTaskMetrics writes scitq_tasks and scitq_task_pending_seconds_max.
func (s *taskQueueServer) refreshTaskMetrics(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM task
		WHERE NOT hidden
		GROUP BY status
	`)
	if err != nil {
		log.Printf("⚠️ metrics: task status query failed: %v", err)
	} else {
		// Reset then pre-seed every known task status to 0. Without
		// this pre-seed, a status whose bucket transiently reaches
		// zero disappears from the exposition entirely — and any
		// consumer pattern-matching for that specific series (e.g.
		// Zabbix's Prometheus preprocessor for scitq_tasks{status="P"})
		// errors with "no matching metrics found" until the bucket
		// re-fills. Observed 2026-09-10 after the orphan-pending
		// cleanup drove P and W to 0. Costs 14 extra series per
		// scrape — cheap relative to the debuggability win.
		metrics.Tasks.Reset()
		for _, st := range []string{"I", "W", "P", "A", "C", "D", "O", "R", "U", "V", "S", "F", "X", "Z"} {
			metrics.Tasks.WithLabelValues(st).Set(0)
		}
		for rows.Next() {
			var status string
			var count int
			if err := rows.Scan(&status, &count); err != nil {
				continue
			}
			metrics.Tasks.WithLabelValues(status).Set(float64(count))
		}
		rows.Close()
	}

	// Oldest pending age. modified_at is the last state change; for a
	// P/W task that's the moment it landed in the queue (or was last
	// promoted from W to P). NULL when no P/W exists → gauge stays 0
	// which is truthful.
	var oldest sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(modified_at))), 0)
		FROM task
		WHERE status IN ('P','W') AND NOT hidden
	`).Scan(&oldest)
	if err != nil {
		log.Printf("⚠️ metrics: pending-age query failed: %v", err)
	} else if oldest.Valid {
		metrics.TaskPendingSecondsMax.Set(oldest.Float64)
	}
}

// refreshWorkflowAndJobMetrics writes scitq_workflow_active and
// scitq_deletion_jobs_stuck.
func (s *taskQueueServer) refreshWorkflowAndJobMetrics(ctx context.Context) {
	var active int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workflow WHERE status IN ('R','D')
	`).Scan(&active)
	if err != nil {
		log.Printf("⚠️ metrics: workflow-active query failed: %v", err)
	} else {
		metrics.WorkflowsActive.Set(float64(active))
	}

	// Deletion jobs that have been R for too long. `now() - created_at`
	// captures the total age; retry cycles inside the job engine reset
	// modified_at but not created_at, so this catches jobs that keep
	// failing and retrying just as much as jobs stuck without retrying.
	var stuck int
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM job
		WHERE action = 'D'
		  AND status = 'R'
		  AND NOW() - created_at > $1
	`, deletionJobStuckThreshold).Scan(&stuck)
	if err != nil {
		log.Printf("⚠️ metrics: deletion-jobs query failed: %v", err)
	} else {
		metrics.DeletionJobsStuck.Set(float64(stuck))
	}
}

// refreshDBPoolMetrics writes scitq_db_connections_open /
// scitq_db_connections_in_use. sql.DB.Stats() is a cheap in-process
// read; no round-trip.
func (s *taskQueueServer) refreshDBPoolMetrics() {
	stats := s.db.Stats()
	metrics.DBConnectionsOpen.Set(float64(stats.OpenConnections))
	metrics.DBConnectionsInUse.Set(float64(stats.InUse))
}

// workerIDLabel renders an int32 worker ID as the string Prometheus
// wants for a label. One helper so every call site formats the same
// way — a mix of "%d" and strconv would produce two different-looking
// label values for the same worker if code drifted.
func workerIDLabel(id int32) string {
	return strconv.FormatInt(int64(id), 10)
}
