ALTER TABLE task
  DROP COLUMN IF EXISTS min_mem_shared,
  DROP COLUMN IF EXISTS min_disk_shared,
  DROP COLUMN IF EXISTS mem_shared_curve,
  DROP COLUMN IF EXISTS disk_shared_curve;

ALTER TABLE recruiter
  DROP COLUMN IF EXISTS memory_shared_per_task,
  DROP COLUMN IF EXISTS disk_shared_per_task;
