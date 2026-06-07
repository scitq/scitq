DROP INDEX IF EXISTS workflow_created_by_idx;
ALTER TABLE workflow DROP COLUMN IF EXISTS created_by;
