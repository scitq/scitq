-- Worker Table
CREATE TABLE IF NOT EXISTS worker (
    worker_id SERIAL PRIMARY KEY,
    worker_name TEXT UNIQUE NOT NULL,
    concurrency INT NOT NULL DEFAULT 1,
    last_ping TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Task Table
CREATE TABLE IF NOT EXISTS task (
    task_id SERIAL PRIMARY KEY,
    command TEXT NOT NULL,
    container TEXT NOT NULL,
    status CHAR(1) NOT NULL DEFAULT 'P',  -- (P: Pending, A: Assigned, C: Accepted, R: Running, S: Succeeded, F: Failed)
    worker_id INT REFERENCES worker(worker_id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    output_log CHAR(1) DEFAULT NULL,  -- (P: Present, Z: Compressed, NULL: Absent)
    error_log CHAR(1) DEFAULT NULL
);

-- Indexes for Fast Queries
CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_worker ON task(worker_id);
