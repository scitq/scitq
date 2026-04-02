-- Revert FK cascades back to SET NULL
ALTER TABLE worker_event DROP CONSTRAINT worker_event_worker_id_fkey;
ALTER TABLE worker_event ADD CONSTRAINT worker_event_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE SET NULL;

ALTER TABLE job DROP CONSTRAINT job_worker_id_fkey;
ALTER TABLE job ADD CONSTRAINT job_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE SET NULL;

-- Drop flavor_stats
DROP TABLE IF EXISTS flavor_stats;

-- Drop partial index and restore unconditional unique constraint
DROP INDEX IF EXISTS idx_worker_deleted;
DROP INDEX IF EXISTS worker_name_active;
ALTER TABLE worker ADD CONSTRAINT worker_worker_name_key UNIQUE (worker_name);

-- Drop columns
ALTER TABLE worker DROP COLUMN IF EXISTS aggregated_at;
ALTER TABLE worker DROP COLUMN IF EXISTS deleted_at;
