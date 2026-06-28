-- Per-task GPU declaration on the recruiter, completing the
-- cpu_per_task / memory_per_task / disk_per_task family added in
-- migration 8.
--
-- The recruiter's role here mirrors the CPU/mem case: when the
-- workflow author wrote `task_spec.gpu: N` on a step, the YAML/DSL
-- builder lifts that into the recruiter's gpu_per_task. At recruit
-- time, computeConcurrencyForRecruiterWorker then sizes the worker's
-- concurrency to floor(flavor.gpu_count / gpu_per_task) — capping
-- in-flight GPU tasks to what the host actually has devices for,
-- rather than letting cpu/mem ratios oversubscribe.
--
-- Nullable so today's CPU-only recruiters stay correct without a
-- backfill. NULL on a static recruiter (worker_concurrency set) is
-- also ignored, matching the cpu_per_task convention.
ALTER TABLE recruiter
  ADD COLUMN gpu_per_task INTEGER NULL;
