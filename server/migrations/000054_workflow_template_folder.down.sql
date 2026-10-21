DROP INDEX IF EXISTS idx_workflow_template_folder;

ALTER TABLE workflow_template
    DROP COLUMN IF EXISTS folder;
