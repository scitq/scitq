BEGIN;

-- 1) Add the new duration columns to tasks
ALTER TABLE task
  DROP COLUMN IF EXISTS download_duration,
  DROP COLUMN IF EXISTS run_duration,
  DROP COLUMN IF EXISTS upload_duration,
  DROP COLUMN IF EXISTS run_started_at;

-- 2) Add workflow status
ALTER TABLE workflow
  DROP COLUMN IF EXISTS status;

COMMIT;