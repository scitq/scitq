// Package metrics exposes scitq server state as Prometheus-format
// gauges + counters. External monitoring (Zabbix, Prometheus, Grafana,
// whatever the ops team picks next) consumes the /metrics endpoint;
// thresholds and alerting stay entirely on the monitoring side.
//
// Design intent (see 2026-09-10 discussion after the worker-6629 leak):
// scitq's job is to be honest about state — worker inventory, task
// queue shape, watchdog drift, DB pool health. The alerting logic
// (silence, escalate, correlate, page on-call) belongs in the
// monitoring tool, not baked into the server.
//
// Two flavours of metric here:
//
//   - Gauges snapshot state. They're re-written periodically by the
//     refresh goroutine in server.go, which owns the DB + watchdog
//     handles and calls the Set* helpers below on each tick.
//
//   - Counters accumulate at event sites (recruiter launch, watchdog
//     reclaim). Call the Inc* helpers directly from those sites.
//
// Adding a new metric is one entry in the var block + one call site
// (or one line in the server-side refresh loop for a gauge).
package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metric declarations. Registered on the default Prometheus registry
// via promauto so a `promhttp.Handler()` picks them up automatically.
var (
	Up = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_up",
		Help: "1 while the scitq server is running. Also a scrape-target liveness signal — if this stops updating for > 30s, the server (or the refresh goroutine) has stalled.",
	})

	Workers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scitq_workers",
		Help: "Number of workers, labelled by status (R/O/I/…), provider, and whether they're permanent.",
	}, []string{"status", "provider", "permanent"})

	WorkerIdleSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scitq_worker_idle_seconds",
		Help: "Seconds since this worker last finished a task (or, for born-idle workers, since the watchdog started tracking it). Non-permanent workers reading above 2× scitq.idle_timeout are the direct 09-09 worker-6629 leak signature.",
	}, []string{"worker_id", "worker_name", "permanent"})

	WorkerActiveTasksDrift = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scitq_worker_active_tasks_drift",
		Help: "DB active-task count (status IN A/C/D/O/R AND NOT hidden) minus watchdog in-memory active-task count. Steady non-zero means the watchdog memory is out of sync with reality — the drift that let worker 6629 leak. Should be 0 in steady state after the 2026-09-10 retryTaskInternal + ResyncActiveTasks fixes.",
	}, []string{"worker_id", "worker_name"})

	WorkerRunningTasks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scitq_worker_running_tasks",
		Help: "Number of tasks the WORKER CLIENT itself reports as currently executing (container/exec launched, upload not started). Runtime ground truth, independent of any server-side DB status bucket — a stuck hidden R task (2026-09-09 worker-6629 leak) cannot inflate this because the client's executingTasks map only tracks its own live launches. Pair with scitq_worker_idle_seconds to build a leak-proof alert: idle_seconds > threshold AND running_tasks == 0.",
	}, []string{"worker_id", "worker_name", "permanent"})

	Tasks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scitq_tasks",
		Help: "Number of tasks by status (excludes hidden — hidden rows are superseded retry-parents, not queue state).",
	}, []string{"status"})

	TaskPendingSecondsMax = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_task_pending_seconds_max",
		Help: "Age (in seconds) of the oldest task stuck in P/W. Grows when the queue is starving for workers.",
	})

	WorkflowsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_workflow_active",
		Help: "Number of workflows in Running or Debug status.",
	})

	DeletionJobsStuck = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_deletion_jobs_stuck",
		Help: "Number of worker-deletion jobs (action=D) that have been in R (running) for more than 10 minutes. Nonzero means a cloud-side delete is hanging — orphan VM risk.",
	})

	DBConnectionsOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_db_connections_open",
		Help: "Established connections in the DB pool (sql.DB.Stats().OpenConnections).",
	})

	DBConnectionsInUse = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scitq_db_connections_in_use",
		Help: "DB connections currently checked out of the pool (sql.DB.Stats().InUse). Sustained near the pool limit means the server is DB-bound.",
	})

	RecruiterLaunches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scitq_recruiter_launches_total",
		Help: "Cumulative worker deployments the recruiter has attempted, labelled by outcome (success/quota/error/blacklisted).",
	}, []string{"provider", "region", "outcome"})

	WatchdogReclaims = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scitq_watchdog_reclaims_total",
		Help: "Cumulative times the watchdog has reset A/C/D/O tasks back to P because their worker went offline.",
	}, []string{"reason"})
)

// Bool converts a boolean to the "true"/"false" strings we use as
// Prometheus label values. Keeps every call site consistent —
// otherwise one place ends up using "1"/"0" and Prometheus treats the
// two series as different, which defeats aggregation.
func Bool(b bool) string {
	return strconv.FormatBool(b)
}

// ResetWorkerGauges clears every per-worker series. Called at the
// top of each refresh tick so a just-deleted worker's series stops
// showing stale values on the next scrape — otherwise Prometheus
// keeps carrying the last value forward and the leak alert would
// fire on a worker that no longer exists.
func ResetWorkerGauges() {
	WorkerIdleSeconds.Reset()
	WorkerActiveTasksDrift.Reset()
	WorkerRunningTasks.Reset()
	Workers.Reset()
}

// Handler is the /metrics endpoint. Wire it on the server's HTTP mux.
func Handler() http.Handler {
	return promhttp.Handler()
}
