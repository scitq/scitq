-- Drop indexes first
DROP INDEX IF EXISTS idx_worker_event_created;
DROP INDEX IF EXISTS idx_worker_event_worker;
DROP INDEX IF EXISTS idx_worker_event_level;
DROP INDEX IF EXISTS idx_worker_event_class;

-- Drop the table
DROP TABLE IF EXISTS worker_event;