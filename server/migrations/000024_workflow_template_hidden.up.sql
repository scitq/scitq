-- 000024_workflow_template_hidden.up.sql
--
-- Adds a `hidden` flag to workflow_template so the operator can keep old
-- or experimental templates in the system (for archival / re-use of past
-- runs) without cluttering `scitq template list` and the UI's templates
-- view. By default templates are visible (hidden = FALSE); setting
-- hidden = TRUE removes them from default listings while preserving the
-- workflow_template row, the linked template_runs, and any historical
-- data they reference.
ALTER TABLE workflow_template
    ADD COLUMN hidden BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_workflow_template_hidden_name
    ON workflow_template (hidden, name);
