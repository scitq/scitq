ALTER TABLE recruiter
  DROP COLUMN IF EXISTS cpu_per_task,
  DROP COLUMN IF EXISTS memory_per_task,
  DROP COLUMN IF EXISTS disk_per_task,
  DROP COLUMN IF EXISTS prefetch_percent,
  DROP COLUMN IF EXISTS concurrency_min,
  DROP COLUMN IF EXISTS concurrency_max;