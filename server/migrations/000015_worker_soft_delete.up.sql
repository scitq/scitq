-- Worker soft-delete: add deleted_at and aggregated_at columns
ALTER TABLE worker ADD COLUMN deleted_at TIMESTAMPTZ NULL;
ALTER TABLE worker ADD COLUMN aggregated_at TIMESTAMPTZ NULL;

-- Replace unconditional unique on worker_name with partial unique index
-- (allows re-use of names after soft-delete, though in practice names are unique per worker_id)
ALTER TABLE worker DROP CONSTRAINT worker_worker_name_key;
CREATE UNIQUE INDEX worker_name_active ON worker (worker_name) WHERE deleted_at IS NULL;

-- Index for aggregation/prune queries on soft-deleted workers
CREATE INDEX idx_worker_deleted ON worker (deleted_at) WHERE deleted_at IS NOT NULL;

-- Flavor stats table: per flavor x region x hour-of-week aggregated statistics
CREATE TABLE flavor_stats (
    flavor_id       INT NOT NULL REFERENCES flavor(flavor_id),
    region_id       INT NOT NULL REFERENCES region(region_id),
    hour_of_week    SMALLINT NOT NULL,  -- 0-167 (hour 0 = Monday 00:00 UTC)
    worker_hours    FLOAT NOT NULL DEFAULT 0,
    evictions       INT NOT NULL DEFAULT 0,
    deploy_attempts INT NOT NULL DEFAULT 0,
    deploy_failures INT NOT NULL DEFAULT 0,
    PRIMARY KEY (flavor_id, region_id, hour_of_week)
);

-- Change FK cascades: hard-delete of old soft-deleted workers cascades to job and worker_event
ALTER TABLE job DROP CONSTRAINT job_worker_id_fkey;
ALTER TABLE job ADD CONSTRAINT job_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE CASCADE;

ALTER TABLE worker_event DROP CONSTRAINT worker_event_worker_id_fkey;
ALTER TABLE worker_event ADD CONSTRAINT worker_event_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker(worker_id) ON DELETE CASCADE;
