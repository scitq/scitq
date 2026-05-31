DROP INDEX IF EXISTS idx_workflow_parent;
ALTER TABLE template_run DROP COLUMN IF EXISTS parent_template_run_id;
ALTER TABLE workflow     DROP COLUMN IF EXISTS parent_workflow_id;
DROP TABLE IF EXISTS workflow_chain_entry;
