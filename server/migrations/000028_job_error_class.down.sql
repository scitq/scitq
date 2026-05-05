DROP INDEX IF EXISTS idx_job_error_class_recent;
ALTER TABLE job
    DROP COLUMN IF EXISTS error_class,
    DROP COLUMN IF EXISTS error_message;
