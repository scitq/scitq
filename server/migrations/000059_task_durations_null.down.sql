UPDATE task SET
  download_duration = COALESCE(download_duration, 0),
  run_duration = COALESCE(run_duration, 0),
  upload_duration = COALESCE(upload_duration, 0)
WHERE download_duration IS NULL OR run_duration IS NULL OR upload_duration IS NULL;

ALTER TABLE task
  ALTER COLUMN download_duration SET DEFAULT 0,
  ALTER COLUMN run_duration SET DEFAULT 0,
  ALTER COLUMN upload_duration SET DEFAULT 0;
