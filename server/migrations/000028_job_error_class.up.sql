-- Surface provider-error categories on the job queue so operators can
-- spot persistent failures (especially auth) within seconds in the UI
-- instead of having to grep journalctl for hours.
--
-- error_class    one of: 'auth' (credentials invalid), 'quota'
--                (subscription/region cap), 'capacity' (zonal stockout),
--                'unsupported_flavor' (Azure rejected the SKU outright),
--                'transient' (timeout / 5xx — retry sane), 'unknown'.
-- error_message  raw provider error text, kept for tooltips + tickets.
ALTER TABLE job
    ADD COLUMN error_class   TEXT,
    ADD COLUMN error_message TEXT;

-- Index for the dashboard banner query: "any auth failure in last hour?"
-- Partial index on F-status rows only — failed jobs are always a small
-- fraction of the table.
CREATE INDEX idx_job_error_class_recent
    ON job (error_class, modified_at)
 WHERE status = 'F' AND error_class IS NOT NULL;
