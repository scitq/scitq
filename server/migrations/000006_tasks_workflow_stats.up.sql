BEGIN;

-- 1) Add the new duration columns to tasks
ALTER TABLE task
  ADD COLUMN download_duration INT DEFAULT 0,
  ADD COLUMN run_duration INT DEFAULT 0,
  ADD COLUMN upload_duration INT DEFAULT 0,
  ADD COLUMN run_started_at TIMESTAMPTZ;

-- 2) Add workflow status
ALTER TABLE workflow
  ADD COLUMN status CHAR(1) DEFAULT 'P';  -- (P: Pending, R: Running, S: Succeeded, F: Failed)

COMMIT;