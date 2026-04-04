DROP TABLE IF EXISTS task_reuse;
DROP INDEX IF EXISTS idx_task_reuse_key;
ALTER TABLE task DROP COLUMN IF EXISTS reuse_original_output;
ALTER TABLE task DROP COLUMN IF EXISTS reuse_hit;
ALTER TABLE task DROP COLUMN IF EXISTS reuse_key;
