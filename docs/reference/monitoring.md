# Monitoring & observability

scitq exposes runtime state as **Prometheus-format metrics** on an
HTTP `/metrics` endpoint. External tooling (Zabbix, Prometheus,
Grafana, VictoriaMetrics, Datadog agent, ...) consumes that endpoint;
alerting rules and dashboards live in the monitoring tool of your
choice.

This is the **admin monitoring path** — leaked workers, DB pool
saturation, quota exhaustion, deletion jobs hanging, and any other
"something is off with the server" signal are metrics with
thresholds, not push messages. For user-facing convenience pings
("your workflow finished"), see the
[Notifications section](configuration.md#notifications-user-facing) in
the configuration reference.

The design intent behind the split is that alerting logic (silence,
acknowledge, escalate, correlate, on-call rotation) belongs in the
monitoring tool, which already has all of that as first-class
features — scitq's job is just to be honest about its state.

## The `/metrics` endpoint

- **URL**: `http://<server>:<http_port>/metrics`
    - `http_port` is `scitq.http_port` in `scitq.yaml`; when unset it
      defaults to `scitq.port + 1` (see the CLI / config docs).
    - Served alongside the UI and MCP endpoint on the same HTTP
      server — no extra port to configure, no extra listener.
- **Format**: Prometheus text-format 0.0.4 (what
  `prometheus/client_golang` emits). Compatible with:
    - Prometheus scrapes (native)
    - Grafana Cloud / VictoriaMetrics / Thanos (native)
    - Zabbix ≥ 4.2 HTTP items with the Prometheus preprocessor
    - Datadog agent's `openmetrics` check
    - `curl <server>/metrics` for a manual sanity check
- **Auth**: none. The endpoint carries only aggregate state (no
  tokens, no user data, no task payloads). Restrict access via
  network policy or a reverse proxy if you need to.
- **Refresh cadence**: gauges refresh every 10 seconds from a
  goroutine that samples the DB + watchdog memory. Counters
  increment at their event sites in real time. `scitq_up` is set
  once at startup and stays at `1` for the life of the process — if
  it disappears from a scrape, the process died; if it stops
  updating with other gauges frozen, the refresh loop stalled.

## Metric catalog

The set is deliberately small — each entry either catches a category
of leak we've hit or seen coming. Add more in
`server/metrics/metrics.go` as needed; one line per metric.

### Worker inventory & leak detection

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `scitq_workers` | gauge | `status`, `provider`, `permanent` | Non-deleted workers grouped by row status (R/O/I/…), cloud provider (`azure.primary`, `openstack.ovh`, `local.local`, …), and whether they're marked permanent. Dashboard slice-and-dice metric. |
| `scitq_worker_idle_seconds` | gauge | `worker_id`, `worker_name`, `permanent` | Seconds since this worker last finished a task (or since the watchdog started tracking it, for a "born idle" worker). **Non-permanent workers reading above `2 × scitq.idle_timeout` are the direct signature of a leaked worker** (see the 2026-09-09 worker-6629 incident). |
| `scitq_worker_active_tasks_drift` | gauge | `worker_id`, `worker_name` | DB active-task count (`status IN A/C/D/O/R AND NOT hidden`) minus watchdog in-memory active-task count. Should be `0` in steady state; sustained non-zero means the watchdog memory has diverged from the DB — the class of bug that let worker 6629 leak. |
| `scitq_worker_running_tasks` | gauge | `worker_id`, `worker_name`, `permanent` | Number of tasks the WORKER CLIENT itself reports as currently executing (container/exec launched, upload not started). **This is the ground-truth runtime signal**: independent of any DB status bucket, sourced from the client's own `executingTasks` map (populated at semaphore acquisition + container/exec launch, cleared when execution ends). A stuck hidden R task cannot inflate it. Pair with `scitq_worker_idle_seconds` for a leak-proof leak-detection alert. |

### Task & workflow shape

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `scitq_tasks` | gauge | `status` | Task count per status, excluding `hidden`. |
| `scitq_task_pending_seconds_max` | gauge | — | Age (seconds) of the oldest task stuck in `P` or `W`. Grows when the queue is starving for workers. |
| `scitq_workflow_active` | gauge | — | Number of workflows in `R` (Running) or `D` (Debug). |

### Recruiter & job health

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `scitq_recruiter_launches_total` | counter | `provider`, `region`, `outcome` | Cumulative deployments the recruiter attempted. `outcome` is `success` / `error` / `quota` / `blacklisted`. |
| `scitq_deletion_jobs_stuck` | gauge | — | Worker-deletion jobs (`action=D`) that have been in `R` for > 10 min. Nonzero means a cloud-side delete is hanging — orphan VM risk. |
| `scitq_watchdog_reclaims_total` | counter | `reason` | Times the watchdog has reset `A/C/D/O` tasks back to `P` because their worker went offline. |

### Process health

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `scitq_up` | gauge | — | `1` while the server is running. Also a scrape-target liveness signal. |
| `scitq_db_connections_open` | gauge | — | Established connections in the DB pool (`sql.DB.Stats().OpenConnections`). |
| `scitq_db_connections_in_use` | gauge | — | Connections currently checked out (`sql.DB.Stats().InUse`). Sustained near the pool limit means the server is DB-bound. |

Plus the usual Go / process metrics that `prometheus/client_golang`
registers by default (goroutine count, memstats, file descriptors,
CPU time). Prefix `go_` / `process_`.

## Setup examples

### Prometheus

```yaml
# prometheus.yml
scrape_configs:
  - job_name: scitq
    scrape_interval: 30s
    static_configs:
      - targets: ['alpha2.gmt.bio:8081']  # scitq HTTP port
```

Alerting rules (one file per severity, or all in one):

```yaml
# scitq-alerts.rules.yml
groups:
- name: scitq
  interval: 30s
  rules:
  - alert: ScitqWorkerLeaked
    # Leak-proof: idle_seconds is timestamp-based (time since last
    # task S/F), running_tasks is the CLIENT's own count of live
    # executions. When both signals agree "nothing has finished in a
    # while AND nothing is running right now", it's a real leak — no
    # stuck-hidden-R DB row can hide the truth from either signal.
    expr: scitq_worker_idle_seconds{permanent="false"} > 2 * 300 and scitq_worker_running_tasks{permanent="false"} == 0
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "scitq worker {{ $labels.worker_name }} ({{ $labels.worker_id }}) idle for {{ $value | humanizeDuration }} — auto-delete didn't fire"
      description: "Non-permanent worker: last task completed >2× idle_timeout ago AND client reports zero running executions. Check server logs for the watchdog reason; the row may still hold a stale hidden A/C/D/O/R task."

  - alert: ScitqActiveTasksDrift
    expr: abs(scitq_worker_active_tasks_drift) > 0
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: "scitq watchdog memory drifted from DB on worker {{ $labels.worker_name }}"
      description: "Steady non-zero drift means a task-status transition didn't update the watchdog. This is the root cause of the 2026-09-09 leak; if it fires post-fix, a new code path forgot to notify."

  - alert: ScitqDeletionStuck
    expr: scitq_deletion_jobs_stuck > 0
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "{{ $value }} scitq worker-delete jobs stuck > 10 min"
      description: "Cloud-side delete hanging — check provider console for orphan VMs."

  - alert: ScitqPendingStarvation
    expr: scitq_task_pending_seconds_max > 3600
    for: 15m
    labels:
      severity: warning
    annotations:
      summary: "scitq oldest pending task is {{ $value | humanizeDuration }}"

  - alert: ScitqDown
    expr: up{job="scitq"} == 0
    for: 2m
    labels:
      severity: critical
    annotations:
      summary: "scitq scrape target is down"
```

### Zabbix (≥ 6.0)

**Quick path — import the shipped template.** Grab
[`zabbix-template-scitq.json`](zabbix-template-scitq.json) and import
it via **Configuration → Templates → Import** (Zabbix ≥ 6.0). It
carries: the HTTP-agent master item, the eight scalar dependent
items, a low-level discovery rule that materialises per-worker items
for every worker exposed by the server, and the starter triggers.
After import, go to **Configuration → Hosts → `<your scitq host>` →
Templates** and link `Template App scitq`. Within one refresh
interval (30s default) Latest data shows every scitq_* series live.

The template was exported from the alpha2 deployment where it's in
production; if the export format's version drifts past your Zabbix
build, the JSON is easy to hand-edit (top-level
`zabbix_export.version`).

The shipped template's leak trigger is leak-proof: it ANDs
`scitq_worker_idle_seconds > 2×idle_timeout` with a
`scitq_worker_running_tasks == 0` clause. The runtime-count metric
is the ground truth from the CLIENT's own executingTasks map, so
neither a stuck hidden R DB row nor a long-running task can misfire
the alert (idle_seconds climbs during long tasks, but running_tasks
stays ≥ 1). Rollout note: **upgrade all clients first**, then link
the template to the host — a pre-fix client reports
`running_tasks=0` (default value) even when busy, which would
trigger a false positive if it's also non-permanent and has been
idle a while.

**Manual path — build it yourself in the UI.** If you'd rather see
how the pieces fit rather than importing a black box:

Create a **Host** for the scitq server, add a **Master item** of type
**HTTP agent** that fetches `/metrics`, then attach one **dependent
item** per metric with a **Prometheus pattern** preprocessing step.

Master item:

- **Name**: scitq metrics
- **Type**: HTTP agent
- **Key**: `scitq.metrics.raw`
- **URL**: `http://alpha2.gmt.bio:8081/metrics`
- **Update interval**: `30s`
- **History**: `0` (raw text, no need to store)

Dependent items (examples — repeat per metric of interest):

| Item name                      | Key                                       | Type of information | Preprocessing (Prometheus pattern)                                                            |
|--------------------------------|-------------------------------------------|---------------------|-----------------------------------------------------------------------------------------------|
| Worker idle max                | `scitq.worker.idle_max`                    | Numeric (float)     | `max(scitq_worker_idle_seconds{permanent="false"})` (via a Prometheus preprocess + JS aggregator, or use a discovery rule) |
| Active-task drift (abs max)    | `scitq.drift.max`                          | Numeric (float)     | `max(scitq_worker_active_tasks_drift)`                                                        |
| Deletion jobs stuck            | `scitq.jobs.deletion_stuck`                | Numeric (unsigned)  | `scitq_deletion_jobs_stuck`                                                                   |
| Pending seconds max            | `scitq.tasks.pending_max`                  | Numeric (float)     | `scitq_task_pending_seconds_max`                                                              |
| Server up                      | `scitq.up`                                 | Numeric (unsigned)  | `scitq_up`                                                                                    |

Triggers (starter set — tune the durations per environment):

- `{Template scitq:scitq.worker.idle_max.last()}>600` — **HIGH**,
  "Non-permanent worker idle > 10 min"
- `{Template scitq:scitq.drift.max.min(15m)}>0` — **WARNING**,
  "Watchdog memory drifted from DB for 15 min"
- `{Template scitq:scitq.jobs.deletion_stuck.min(10m)}>0` — **WARNING**,
  "Worker-delete jobs stuck > 10 min"
- `{Template scitq:scitq.tasks.pending_max.min(15m)}>3600` — **WARNING**,
  "Oldest pending task is over 1 h"
- `{Template scitq:scitq.up.max(5m)}=0` — **DISASTER**, "scitq server
  down"

For per-worker breakdowns (rather than "max across the fleet"), use a
**low-level discovery** item whose preprocessing pulls the label
values from the metrics endpoint; each discovered worker gets its own
item + trigger. That's a Zabbix-side setup detail, not scitq's
concern.

### Grafana

Point Grafana at the same Prometheus (or scrape scitq directly via
Grafana Agent). A basic dashboard covers most of the needs:

- Row 1: `scitq_up`, `scitq_workflow_active`, `sum(scitq_workers)`,
  `sum(scitq_tasks{status="R"})` — top-line KPIs.
- Row 2: `scitq_worker_idle_seconds{permanent="false"}` (top-N table),
  `scitq_worker_active_tasks_drift` (top-N).
- Row 3: `sum by (status) (scitq_tasks)` stacked area, `rate(scitq_recruiter_launches_total{outcome="success"}[5m])`,
  `rate(scitq_watchdog_reclaims_total[5m])`.
- Row 4: `scitq_db_connections_open`, `scitq_db_connections_in_use`
  vs. `max_db_concurrency`.

## Adding a metric

Pattern in `server/metrics/metrics.go`:

```go
var MyNewGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
    Name: "scitq_my_thing",
    Help: "What it measures and why it matters.",
}, []string{"dim_a", "dim_b"})
```

Then either:

- Update the gauge from `server/metrics_refresh.go` on each tick
  (read DB + watchdog + config, call `MyNewGauge.WithLabelValues(...).Set(...)`).
- OR increment at the event site if it's a counter:
  `MyNewCounter.WithLabelValues(...).Inc()`.

That's the whole extension surface. Any Prometheus label rule
applies (bounded cardinality — don't put `task_id` or `command` in a
label, use `worker_id` / `step_id` scope).