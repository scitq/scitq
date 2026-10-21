-- Per-template-name folder path, "/"-separated, empty = root.
-- The folder is scoped to the template NAME, not to each (name, version)
-- row: a move affects every version. The service layer enforces this
-- invariant on upload (inherit from existing versions) and on move
-- (update every row sharing the name).

ALTER TABLE workflow_template
    ADD COLUMN folder TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_workflow_template_folder
    ON workflow_template (folder);
