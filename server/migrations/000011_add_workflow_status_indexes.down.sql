BEGIN;

DROP INDEX IF EXISTS idx_task_step_status_hidden;
DROP INDEX IF EXISTS idx_workflow_status;

UPDATE workflow
SET status = 'R';

ALTER TABLE workflow
  ALTER COLUMN status SET DEFAULT 'R';

COMMIT;
