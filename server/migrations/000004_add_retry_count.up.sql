-- Add a denormalized retry counter on each task.
ALTER TABLE task
ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE task 
ADD COLUMN hidden BOOLEAN NOT NULL DEFAULT FALSE;

-- Optional: index if you expect to filter/sort by this frequently.
CREATE INDEX IF NOT EXISTS idx_task_retry_count ON task(retry_count);
CREATE INDEX IF NOT EXISTS idx_task_hidden ON task(hidden);