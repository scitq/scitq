-- Historical worker stats — one row per (worker, ping).
--
-- Each row records both the CURRENT reading at ping time (cpu/mem/iowait_percent)
-- and the PEAK observed by the worker's 1 Hz sampler since the previous ping
-- (peak_*). The current values match today's live gauges; the peaks are what
-- the summary endpoint aggregates for "what did this workflow actually need?".
--
-- step_id is a snapshot of what the worker was serving at ping time; it
-- lets a workflow-scoped query resolve which samples belong to which
-- workflow via a single JOIN to `step`, without walking a separate
-- worker-assignment log.
--
-- No indexes on the raw current columns — the only queries planned are:
--   * (worker_id, sampled_at) range → the PK covers it
--   * (step_id, sampled_at) range for workflow-scoped queries
--   * retention DELETE on sampled_at alone
CREATE TABLE IF NOT EXISTS worker_stats_history (
    worker_id                INT       NOT NULL REFERENCES worker(worker_id) ON DELETE CASCADE,
    sampled_at               TIMESTAMP NOT NULL DEFAULT NOW(),
    step_id                  INT       NULL REFERENCES step(step_id) ON DELETE SET NULL,
    cpu_percent              REAL      NULL,
    mem_percent              REAL      NULL,
    iowait_percent           REAL      NULL,
    peak_cpu_percent         REAL      NULL,
    peak_mem_percent         REAL      NULL,
    peak_iowait_percent      REAL      NULL,
    effective_concurrency    INT       NULL,
    running_tasks            INT       NULL,
    last_throttle_at         TIMESTAMP NULL,
    PRIMARY KEY (worker_id, sampled_at)
);

CREATE INDEX IF NOT EXISTS idx_worker_stats_history_sampled_at
    ON worker_stats_history (sampled_at);

CREATE INDEX IF NOT EXISTS idx_worker_stats_history_step
    ON worker_stats_history (step_id, sampled_at);
