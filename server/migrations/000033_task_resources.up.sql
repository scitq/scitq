-- Per-task minimum resource requirements + failure classification.
--
-- Three resource columns mirror TaskRequest.min_cpu / min_mem / min_disk
-- and let the step's `task_spec` declare a list curve (e.g. mem: [40,80,160]).
-- The initial submit stores curve[0]; on retry the values shift to
-- curve[attempt] so the assignment & recruitment paths see the heavier
-- requirement. NULL = inherit from the step's task_spec (today's behaviour).
--
-- failure_class is set when the task transitions to status='F':
--   "oom"      out-of-memory kill (docker exit 137 / OOMKilled)
--   "timeout"  exceeded running_timeout / upload_timeout / download_timeout
--   "eviction" server-declared (spot preempt, manual worker delete, watchdog)
--   "network"  transient connectivity failure (uploads, downloads)
--   "other"    anything else (script exit non-zero, segfault, …)
-- The worker reports oom/timeout/network/other; eviction is set server-side
-- when reaping orphaned tasks of a deleted/disappeared worker. The
-- retry-decision logic consults this when deciding to advance the resource
-- curve — `eviction` (and unset) never advance.
--
-- Spec: addition_from_nextflow.md (A — Retry with resource escalation).
ALTER TABLE task
    ADD COLUMN min_cpu       DOUBLE PRECISION NULL,
    ADD COLUMN min_mem       DOUBLE PRECISION NULL,
    ADD COLUMN min_disk      DOUBLE PRECISION NULL,
    ADD COLUMN failure_class TEXT             NULL;
