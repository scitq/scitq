-- 1. Drop the new column if it exists
ALTER TABLE task DROP COLUMN IF EXISTS weight;
ALTER TABLE worker ADD COLUMN IF NOT EXISTS task_properties JSONB DEFAULT '{}';