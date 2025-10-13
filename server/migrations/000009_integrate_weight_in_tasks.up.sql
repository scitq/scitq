-- 1. Add a weight column directly to the task table.
ALTER TABLE task
ADD COLUMN weight FLOAT DEFAULT 1.0;

-- 2. Add comment for documentation.
COMMENT ON COLUMN task.weight IS
'Fraction of the assigned worker''s concurrency consumed by this task (default 1.0).';

-- 3. Drop the weight_memory table if it exists, as it's no longer needed.
ALTER TABLE worker DROP COLUMN IF EXISTS task_properties;

-- Unrelated cleanup
ALTER TABLE recruiter ALTER COLUMN maximum_workers DROP DEFAULT;
ALTER TABLE workflow ALTER COLUMN maximum_workers DROP DEFAULT;