-- A duration is recorded when its phase happens: download on O, run on U/V,
-- upload on S/F. Until then it is NULL, so a phase that took under a second
-- (0) is told apart from one that never happened (a task skipped or reused,
-- still pending, or failed before running).
ALTER TABLE task
  ALTER COLUMN download_duration DROP DEFAULT,
  ALTER COLUMN run_duration DROP DEFAULT,
  ALTER COLUMN upload_duration DROP DEFAULT;

-- Rows written under the old default 0: clear the phases that did not happen.
UPDATE task SET
  download_duration = CASE
    WHEN download_duration = 0 AND (status IN ('I','W','P','A','C','D') OR (run_started_at IS NULL AND status <> 'O'))
    THEN NULL ELSE download_duration END,
  run_duration = CASE
    WHEN run_duration = 0 AND (run_started_at IS NULL OR status = 'R')
    THEN NULL ELSE run_duration END,
  upload_duration = CASE
    WHEN upload_duration = 0 AND (run_started_at IS NULL OR status NOT IN ('S','F'))
    THEN NULL ELSE upload_duration END
WHERE (download_duration = 0 OR run_duration = 0 OR upload_duration = 0)
  AND (run_started_at IS NULL OR status IN ('I','W','P','A','C','D','R','O','U','V'));
