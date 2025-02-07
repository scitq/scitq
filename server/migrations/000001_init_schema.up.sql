
-- Workflow Table
CREATE TABLE IF NOT EXISTS workflow (
    workflow_id SERIAL PRIMARY KEY,
    workflow_name TEXT NOT NULL,
    run_strategy CHAR(1) NOT NULL DEFAULT 'B',  -- (B: Batch wise execution, T: Thread wise execution - follow thread logic, D: Debug execution, Z: suspended execution)
    maximum_worker INT
);

-- Step Table
CREATE TABLE IF NOT EXISTS step (
    step_id SERIAL PRIMARY KEY,
    step_name TEXT NOT NULL,
    workflow_id INT REFERENCES workflow(workflow_id) ON DELETE CASCADE,
    stats JSONB
);


-- Provider Table
CREATE TABLE IF NOT EXISTS provider (
    provider_id SERIAL PRIMARY KEY,
    provider_name TEXT
);

-- Region Table
CREATE TABLE IF NOT EXISTS region (
    region_id SERIAL PRIMARY KEY,
    provider_id INT REFERENCES provider(provider_id) ON DELETE CASCADE,
    region_name TEXT
);

-- Flavor Table
CREATE TABLE IF NOT EXISTS flavor (
    flavor_id SERIAL PRIMARY KEY,
    provider_id INT REFERENCES provider(provider_id) ON DELETE CASCADE,
    flavor_name TEXT NOT NULL,
    cpu INT NOT NULL,
    mem INT NOT NULL,
    disk INT NOT NULL,
    bandwidth INT,
    gpu TEXT,
    gpumem INT,
    has_gpu BOOLEAN,
    has_quick_disks BOOLEAN
);

-- Flavor Region table 
CREATE TABLE IF NOT EXISTS flavor_region (
    flavor_id SERIAL PRIMARY KEY,
    region_id INT REFERENCES region(region_id) ON DELETE CASCADE,
    eviction FLOAT, -- the risk of an instance to be reclaimed, the name come from Azure Spot
    cost FLOAT
);


-- Worker Table
CREATE TABLE IF NOT EXISTS worker (
    worker_id SERIAL PRIMARY KEY,
    worker_name TEXT UNIQUE NOT NULL,
    step_id INT REFERENCES step(step_id) ON DELETE SET NULL,
    concurrency INT NOT NULL DEFAULT 1,
    status CHAR(1) NOT NULL DEFAULT 'O', -- (O: Offline, I: Installing, R: Ready, P: Paused, F: Failing)
    stats JSONB,
    last_ping TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    task_properties JSONB,
    install_strategy JSONB,
    flavor_id INT,
    region_id INT,
    permanent BOOLEAN DEFAULT TRUE,
    hostname TEXT,
    ipv4 inet,
    ipv6 inet
);


-- Task Table
CREATE TABLE IF NOT EXISTS task (
    task_id SERIAL PRIMARY KEY,
    step_id INT REFERENCES step(step_id) ON DELETE SET NULL,
    command TEXT NOT NULL,
    container TEXT NOT NULL,
    container_options TEXT[],
    status CHAR(1) NOT NULL DEFAULT 'P',  -- (P: Pending, A: Assigned, C: Accepted, D: Downloading, R: Running, U: Uploading, S: Succeeded, F: Failed, Z: suspended, X: canceled, W: waiting)
    worker_id INT REFERENCES worker(worker_id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMP NOT NULL DEFAULT NOW(),
    output_log CHAR(1) DEFAULT NULL,  -- (P: Present, Z: Compressed, NULL: Absent)
    error_log CHAR(1) DEFAULT NULL,
    previous_task_id INT REFERENCES task(task_id) ON DELETE SET NULL,
    input TEXT[],
    resource TEXT[],
    output TEXT,
    output_files TEXT[],
    output_is_compressed BOOLEAN,
    retry INT DEFAULT 0,
    is_final BOOLEAN DEFAULT FALSE,
    cache BOOLEAN DEFAULT FALSE,
    download_timeout FLOAT,
    running_timeout FLOAT,
    upload_timeout FLOAT,
    input_hash UUID
);

-- Indexes for Fast Queries
CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_worker ON task(worker_id);





-- Recruiter Table
CREATE TABLE IF NOT EXISTS recruiter (
    step_id INT REFERENCES step(step_id),
    rank INT DEFAULT 1,
    step_name TEXT,
    timeout INT,
    is_recycling BOOLEAN DEFAULT FALSE,
    worker_flavor TEXT,
    worker_provider TEXT,
    worker_region TEXT,
    worker_concurrency INT,
    worker_prefetch INT,
    maximum_worker INT,
    rounds INT,
    is_active BOOLEAN DEFAULT TRUE,
    PRIMARY KEY (step_id, rank)
);




-- Task dependencies table
CREATE TABLE task_dependencies (
    dependent_task_id INTEGER NOT NULL,
    prerequisite_task_id INTEGER NOT NULL,
    PRIMARY KEY (dependent_task_id, prerequisite_task_id),
    FOREIGN KEY (dependent_task_id) REFERENCES task(task_id) ON DELETE CASCADE,
    FOREIGN KEY (prerequisite_task_id) REFERENCES task(task_id) ON DELETE CASCADE,
    CHECK (dependent_task_id <> prerequisite_task_id)
);

