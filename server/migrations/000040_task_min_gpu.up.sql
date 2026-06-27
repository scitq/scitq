-- Per-task GPU requirement, the fourth dimension of the fit predicate
-- (joining min_cpu / min_mem / min_disk introduced in earlier
-- migrations). Default 0 = no GPU needed, so legacy tasks are
-- unaffected; non-zero means the assignment loop and worker pickup
-- logic must check the worker's flavor.has_gpu.
--
-- Integer rather than bool so a future flavor.gpu_count column can be
-- compared meaningfully without another migration. Until then the
-- server treats any has_gpu=true flavor as supplying "1+" GPUs.
-- NULL-able (no NOT NULL), matching the cpu/mem/disk pattern: SubmitTask
-- inserts a Go *int32 that is nil when the YAML/DSL didn't set
-- task_spec.gpu, and the fit predicate uses COALESCE(min_gpu,0) so NULL
-- and 0 are equivalent at scheduling time.
ALTER TABLE task
  ADD COLUMN min_gpu INT DEFAULT 0;
