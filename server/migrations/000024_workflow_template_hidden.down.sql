-- 000024_workflow_template_hidden.down.sql
DROP INDEX IF EXISTS idx_workflow_template_hidden_name;
ALTER TABLE workflow_template DROP COLUMN IF EXISTS hidden;
