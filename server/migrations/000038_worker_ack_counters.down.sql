DROP INDEX IF EXISTS idx_worker_event_unacked_warnings;
ALTER TABLE worker_event DROP COLUMN IF EXISTS acknowledged_at;
ALTER TABLE worker DROP COLUMN IF EXISTS failures_cleared_at;
