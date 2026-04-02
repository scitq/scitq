# Flavor stats & worker soft-delete

## Motivation

Workers are currently hard-deleted from the database after cloud undeploy completes. This causes `worker_event.worker_id` and `job.worker_id` to become NULL (`ON DELETE SET NULL`), severing the link between historical events and the worker that produced them. This makes post-mortem analysis and statistical aggregation impossible.

Beyond preserving history, we want to build **flavor-level operational stats** from real production data:

1. **Spot eviction rates** — the public Azure eviction figures are inaccurate. We should compute our own, per flavor×region, modulated by time-of-day and day-of-week.
2. **Aborted deploy rates** — deploys can fail silently (Azure: stuck for >20 min) or noisily (OVH: hard failure, sometimes leaving orphan VMs in the provider).
3. **Orphan VM cleanup** — detect VMs in the provider that match our naming pattern but correspond to no active worker (or match a soft-deleted worker), and clean them up.

## Part 1: Worker soft-delete

### Schema change

New migration: add `deleted_at` to worker.

```sql
ALTER TABLE worker ADD COLUMN deleted_at TIMESTAMPTZ NULL;
ALTER TABLE worker ADD COLUMN aggregated_at TIMESTAMPTZ NULL;

-- Replace unique on worker_name (currently unconditional) with partial index
-- so a new worker can reuse a name after the old one is soft-deleted.
ALTER TABLE worker DROP CONSTRAINT worker_worker_name_key;
CREATE UNIQUE INDEX worker_name_active ON worker (worker_name) WHERE deleted_at IS NULL;

-- Index for aggregation/prune queries
CREATE INDEX idx_worker_deleted ON worker (deleted_at) WHERE deleted_at IS NOT NULL;
```

No change to `worker_event` or `job` tables — the FK stays, `ON DELETE SET NULL` is kept as a safety net, but `DELETE FROM worker` is never executed in normal operation.

### Behavior change

**DeleteWorker** (server.go) currently has two hard-delete paths:

1. Non-permanent, undeployed → `DELETE FROM worker ... RETURNING provider, cpu, mem` (quota adjustment)
2. Permanent, offline/installing → `DELETE FROM worker WHERE worker_id=$1`

Both become:

```sql
UPDATE worker SET deleted_at = now(), status = 'D' WHERE worker_id = $1
```

Quota adjustment (decrementing provider worker count) stays — it runs in the same transaction before the soft-delete.

### Queries requiring `deleted_at IS NULL` filter

All queries that list "live" workers must exclude soft-deleted rows:

| Query | Location |
|---|---|
| `ListWorkers` | server.go |
| `FetchWorkersForWatchdog` | server.go |
| `findRecyclableWorkers` | recruitment.go |
| `listActiveRecruiters` CTE (worker_load) | recruitment.go |
| `listRecruitersForStep` CTE (worker_load) | recruitment.go |
| `getWorkflowCounters` | recruitment.go |
| `RegisterWorker` existence check | server.go |
| `UpdateWorkerStatus` | server.go |

Queries that intentionally include historical workers (stats, events) do **not** add the filter.

### Worker name uniqueness

Worker names follow the pattern `{ServerName}worker{ID}` (e.g. `alpha2worker3047`). The name is unique per worker_id, so conflicts between soft-deleted and new workers are impossible in practice. The partial unique index is a safety net.

## Part 2: Eviction stats (Azure spot)

### What we track

Eviction = a spot worker is reclaimed by Azure before its tasks complete. Observable as: worker transitions to `F`/`L`/`O` status unexpectedly while tasks were running, or tasks on the worker fail with eviction-specific signals.

### Data model

```sql
CREATE TABLE flavor_stats (
    flavor_id    INT NOT NULL REFERENCES flavor(flavor_id),
    region_id    INT NOT NULL REFERENCES region(region_id),
    hour_of_week SMALLINT NOT NULL,  -- 0-167 (hour 0 = Monday 00:00 UTC)
    worker_hours FLOAT NOT NULL DEFAULT 0,  -- total worker-hours observed
    evictions    INT NOT NULL DEFAULT 0,     -- eviction count in this bucket
    PRIMARY KEY (flavor_id, region_id, hour_of_week)
);
```

`hour_of_week` encodes both day-of-week and time-of-day in a single dimension (168 buckets). The eviction rate for a bucket is `evictions / worker_hours`.

### Collection

A periodic **aggregation job** (e.g. every hour) scans soft-deleted workers that haven't been aggregated yet:

```sql
SELECT w.worker_id, w.flavor_id, w.region_id, w.created_at, w.deleted_at,
       w.status AS final_status,
       EXISTS (
           SELECT 1 FROM job j
           WHERE j.worker_id = w.worker_id AND j.action = 'D' AND j.status = 'S'
       ) AS was_cleanly_deleted
FROM worker w
WHERE w.deleted_at IS NOT NULL
  AND w.aggregated_at IS NULL
  AND w.flavor_id IS NOT NULL
  AND w.region_id IS NOT NULL;
```

A new column `aggregated_at TIMESTAMPTZ NULL` on worker marks rows that have been processed. For each worker:

- Compute the alive range `created_at` → `deleted_at`
- Distribute the duration across `hour_of_week` buckets, incrementing `worker_hours`
- If evicted (see detection below), increment `evictions` for the bucket where eviction occurred
- Set `aggregated_at = now()`

The aggregation window naturally decays: old `flavor_stats` rows accumulate data from many workers, but as flavors go out of fashion their `worker_hours` stop growing while newer flavors accumulate fresh data. The recruiter can further weight by recency if needed (e.g. only consider stats from the last N days by adding a `period_start` dimension — but the simple 168-bucket model is a good starting point).

### Eviction detection

A worker deletion is an **eviction** when:
- The worker's provider is spot-capable (Azure with `use_spot = true`)
- The deletion was **not** initiated by scitq (i.e., not from a `Job` with action='D')
- The worker was in a running state (`R`, `F`, `L`) with active tasks at the time of loss

Concretely: the watchdog detects a lost worker (no ping for X seconds) → sets status to `L` → triggers soft-delete. If the worker had `is_permanent = false` and was on a spot-capable provider, it's an eviction.

### Query

```sql
-- Eviction rate per flavor×region for current hour-of-week
SELECT fs.flavor_id, fs.region_id, f.flavor_name, r.region_name,
       fs.evictions::float / NULLIF(fs.worker_hours, 0) AS eviction_per_hour
FROM flavor_stats fs
JOIN flavor f USING (flavor_id)
JOIN region r USING (region_id)
WHERE fs.hour_of_week = $1  -- current hour_of_week
  AND fs.worker_hours > 10  -- minimum sample size
ORDER BY eviction_per_hour;
```

The recruiter can use this to prefer flavors with lower eviction rates at the current time, replacing the inaccurate public eviction figures in `flavor_region.eviction`.

## Part 3: Aborted deploy stats

### What we track

A deploy is **aborted** when:
- The `job` with action='C' ends in status 'F'
- Or the worker stays in status 'I' (Installing) beyond a threshold (e.g. 20 min for Azure, 10 min for OVH)

### Data model

Same `flavor_stats` table, additional columns:

```sql
ALTER TABLE flavor_stats
    ADD COLUMN deploy_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN deploy_failures INT NOT NULL DEFAULT 0;
```

Deploy failure rate = `deploy_failures / deploy_attempts`.

### Collection

Same periodic aggregation job. For each soft-deleted worker with `aggregated_at IS NULL`:
- Look up its creation job (`job` with `action='C'` and matching `worker_id`)
- Increment `deploy_attempts` for the flavor×region×hour_of_week bucket (based on `job.created_at`)
- If job status is 'F', also increment `deploy_failures`

Workers that never made it past 'I' status (deploy failed) are still soft-deleted and thus picked up by the aggregator.

### Provider-specific failure modes

| Provider | Failure mode | Detection |
|---|---|---|
| Azure | Deploy stuck (>20 min) | Job timeout → status 'F' |
| OVH/Openstack | Hard failure, sometimes leaves orphan VM | Job fails, orphan detected by cleanup |

## Part 4: Orphan VM cleanup

### Problem

When a deploy fails or a worker is evicted, the cloud VM may persist in the provider while scitq has no active worker for it. This wastes money and can cause confusion.

### Detection

Worker names follow `{ServerName}worker{ID}`. Each provider exposes a `List(region)` method returning `map[workerName]IP`.

A VM is an **orphan** when:
- Its name matches `{ServerName}worker\d+` (our naming pattern)
- There is no active worker row with that name (`deleted_at IS NULL`)

A VM is a **zombie** when:
- Its name matches an existing soft-deleted worker (`deleted_at IS NOT NULL`)

Both should be cleaned up.

### Implementation

New periodic job (runs every N minutes, configurable):

```
for each provider:
    for each region with active workers or recent deploys:
        vms = provider.List(region)
        for each vm in vms:
            if vm.name matches our pattern:
                worker = lookup worker by name (including soft-deleted)
                if worker is nil:
                    log warning "orphan VM {name} in {region}, deleting"
                    provider.Delete(name, region)
                elif worker.deleted_at is not nil:
                    log info "zombie VM {name} matches deleted worker {id}, deleting"
                    provider.Delete(name, region)
                // else: worker exists and is active, normal
```

This replaces ad-hoc cleanup and provides a systematic safety net for all failure modes.

### OVH cadaver handling

OVH failures sometimes leave VMs in a broken state that doesn't respond to normal deletion. The cleanup job should retry deletion with exponential backoff and, after N failures, emit a worker_event at level 'E' (Error) for manual intervention.

## Part 5: Worker pruning

### Problem

Soft-deleted workers, their jobs, and their events accumulate forever. We need a periodic hard-delete for old soft-deleted workers to avoid database bloat.

### Constraint: only prune aggregated workers

Pruning must only hard-delete workers whose data has already been aggregated into `flavor_stats`. This is enforced by requiring `aggregated_at IS NOT NULL`.

### FK cascade change

Change the foreign keys on `job` and `worker_event` from `ON DELETE SET NULL` to `ON DELETE CASCADE`:

```sql
-- job
ALTER TABLE job DROP CONSTRAINT job_worker_id_fkey;
ALTER TABLE job ADD CONSTRAINT job_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE CASCADE;

-- worker_event
ALTER TABLE worker_event DROP CONSTRAINT worker_event_worker_id_fkey;
ALTER TABLE worker_event ADD CONSTRAINT worker_event_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE CASCADE;
```

This is safe because hard-deletes only target old, aggregated, soft-deleted workers.

### Prune operation

```sql
DELETE FROM worker
WHERE deleted_at IS NOT NULL
  AND aggregated_at IS NOT NULL
  AND deleted_at < now() - $1::interval;
```

Where `$1` is the retention period (default: 30 days, configurable via `worker_retention` in `scitq.yaml`).

The cascade automatically removes associated `job` and `worker_event` rows.

### Scheduling

The aggregation and prune jobs run as periodic goroutines:

| Job | Frequency | Purpose |
|---|---|---|
| **Aggregation** | Hourly | Scan unaggregated soft-deleted workers, write to `flavor_stats`, stamp `aggregated_at` |
| **Prune** | Daily | Hard-delete aggregated soft-deleted workers older than retention period |

Prune always runs after aggregation in the cycle, so the invariant holds: no worker is pruned before its stats are recorded.

Both also available as manual operations:
- CLI: `scitq aggregate-stats`, `scitq prune-workers [--older-than 30d]`
- MCP: `aggregate_flavor_stats`, `prune_workers`

## Summary of changes

| Component | Change |
|---|---|
| **Migration** | `deleted_at` + `aggregated_at` on worker, partial unique index, `flavor_stats` table, FK cascade change on job/worker_event |
| **server.go** | Soft-delete in `DeleteWorker`, `deleted_at IS NULL` filters |
| **recruitment.go** | `deleted_at IS NULL` in all worker queries |
| **New: aggregation job** | Hourly: scan unaggregated soft-deleted workers → write `flavor_stats` → stamp `aggregated_at` |
| **New: prune job** | Daily: hard-delete aggregated soft-deleted workers older than retention (cascades to jobs/events) |
| **New: cleanup job** | Periodic orphan/zombie VM scan and provider-level deletion |
| **scitq.yaml** | `worker_retention` setting (default 30 days) |
| **MCP/CLI** | Expose flavor_stats (read-only), `aggregate_flavor_stats`, `prune_workers` commands |
