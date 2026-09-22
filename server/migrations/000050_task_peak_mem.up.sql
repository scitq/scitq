-- Per-attempt peak memory, in megabytes.
--
-- Answers "which task hit that memory" — the per-task companion to
-- the per-worker MAX in worker_stats_history. The per-worker peak
-- reports the union of what every co-running task on that worker did;
-- with N concurrent tasks the operator can't tell which task drove
-- the peak. peak_mem_mb on the task row settles that directly.
--
-- Populated by the worker at task terminal (S / F) from the kernel-
-- tracked peak counter (cgroup memory.peak on v2, VmHWM on bare
-- tasks). One-shot at task end, no per-second sampling — the kernel
-- has been tracking the peak all along.
--
-- NULL when the worker didn't report (older client, missing cgroup
-- file, task that never actually ran). Not carried across retries:
-- each attempt has its own peak. The retry-clone SQL leaves this
-- column at DEFAULT NULL for the clone.
ALTER TABLE task
    ADD COLUMN peak_mem_mb INT NULL;
