
-- Workflow Table
CREATE TABLE IF NOT EXISTS workflow (
    workflow_id SERIAL PRIMARY KEY,
    workflow_name TEXT NOT NULL UNIQUE,
    run_strategy CHAR(1) NOT NULL DEFAULT 'B',  -- (B: Batch wise execution, T: Thread wise execution - follow thread logic, D: Debug execution, Z: suspended execution)
    maximum_workers INT DEFAULT 0
);

-- Step Table
CREATE TABLE IF NOT EXISTS step (
    step_id SERIAL PRIMARY KEY,
    step_name TEXT NOT NULL,
    workflow_id INT REFERENCES workflow(workflow_id) ON DELETE CASCADE,
    stats JSONB DEFAULT '{}',
    CONSTRAINT step_name_unique UNIQUE (step_name, workflow_id)
);



-- Provider Table
CREATE TABLE IF NOT EXISTS provider (
    provider_id SERIAL PRIMARY KEY,
    provider_name TEXT NOT NULL,
    config_name TEXT NOT NULL
);

-- Region Table
CREATE TABLE IF NOT EXISTS region (
    region_id SERIAL PRIMARY KEY,
    provider_id INT REFERENCES provider(provider_id) ON DELETE CASCADE,
    region_name TEXT NOT NULL,
    is_default BOOLEAN DEFAULT FALSE
);

-- Flavor Table
CREATE TABLE IF NOT EXISTS flavor (
    flavor_id SERIAL PRIMARY KEY,
    provider_id INT REFERENCES provider(provider_id) ON DELETE CASCADE,
    flavor_name TEXT NOT NULL,
    cpu INT NOT NULL,
    mem FLOAT NOT NULL,
    disk FLOAT NOT NULL,
    bandwidth INT DEFAULT 0,
    gpu TEXT DEFAULT '',
    gpumem INT DEFAULT 0,
    has_gpu BOOLEAN DEFAULT FALSE,
    has_quick_disks BOOLEAN DEFAULT FALSE
);

-- Flavor Region table 
CREATE TABLE IF NOT EXISTS flavor_region (
    flavor_id SERIAL PRIMARY KEY,
    region_id INT REFERENCES region(region_id) ON DELETE CASCADE,
    eviction FLOAT DEFAULT 0, -- the risk of an instance to be reclaimed, the name come from Azure Spot
    cost FLOAT DEFAULT 0
);


-- Worker Table
CREATE TABLE IF NOT EXISTS worker (
    worker_id SERIAL PRIMARY KEY,
    worker_name TEXT UNIQUE NOT NULL,
    step_id INT NULL REFERENCES step(step_id) ON DELETE SET NULL,
    concurrency INT NOT NULL DEFAULT 1,
    prefetch INT NOT NULL DEFAULT 0,
    status CHAR(1) NOT NULL DEFAULT 'I', -- (O: Offline, I: Installing, R: Ready, P: Paused, F: Failing)
    stats JSONB DEFAULT '{}',
    last_ping TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    task_properties JSONB DEFAULT '{}',
    install_strategy JSONB DEFAULT '{}',
    flavor_id INT REFERENCES flavor(flavor_id) ON DELETE SET NULL,
    region_id INT REFERENCES region(region_id) ON DELETE SET NULL,
    is_permanent BOOLEAN DEFAULT TRUE,
    hostname TEXT DEFAULT '',
    ipv4 inet,
    ipv6 inet,
    recyclable_scope CHAR(1) NOT NULL DEFAULT 'W' -- (G: Global, W: Workflow-only, T: Temporarily blocked, N: Never recyclable)
);


-- Task Table
CREATE TABLE IF NOT EXISTS task (
    task_id SERIAL PRIMARY KEY,
    step_id INT REFERENCES step(step_id) ON DELETE SET NULL,
    command TEXT NOT NULL,
    shell TEXT,
    container TEXT NOT NULL,
    container_options TEXT[] DEFAULT '{}',
    status CHAR(1) NOT NULL DEFAULT 'P',  -- (P: Pending, A: Assigned, C: Accepted, D: Downloading, R: Running, U: Uploading (after success), V: Uploading (after failure), S: Succeeded, F: Failed, Z: suspended, X: canceled, W: waiting)
    worker_id INT REFERENCES worker(worker_id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMP NOT NULL DEFAULT NOW(),
    output_log CHAR(1) DEFAULT '',  -- (P: Present, Z: Compressed, NULL: Absent)
    error_log CHAR(1) DEFAULT '',
    previous_task_id INT REFERENCES task(task_id) ON DELETE SET NULL,
    input TEXT[] DEFAULT '{}',
    resource TEXT[] DEFAULT '{}',
    output TEXT DEFAULT '',
    output_files TEXT[] DEFAULT '{}',
    output_is_compressed BOOLEAN DEFAULT FALSE,
    retry INT DEFAULT 0,
    is_final BOOLEAN DEFAULT FALSE,
    uses_cache BOOLEAN DEFAULT FALSE,
    download_timeout FLOAT DEFAULT 0,
    running_timeout FLOAT DEFAULT 0,
    upload_timeout FLOAT DEFAULT 0,
    pid INT,
    input_hash UUID
);

-- Indexes for Fast Queries
CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_worker ON task(worker_id);





-- Recruiter Table
CREATE TABLE IF NOT EXISTS recruiter (
    step_id INT REFERENCES step(step_id),
    rank INT DEFAULT 1,
    timeout INT DEFAULT 0,
    worker_flavor TEXT NOT NULL,
    worker_provider TEXT DEFAULT '',
    worker_region TEXT DEFAULT '',
    worker_concurrency INT DEFAULT 1,
    worker_prefetch INT DEFAULT 0,
    maximum_workers INT DEFAULT 0,
    rounds INT DEFAULT 1,
    PRIMARY KEY (step_id, rank)
);




-- Task dependencies table
CREATE TABLE IF NOT EXISTS task_dependencies (
    dependent_task_id INTEGER NOT NULL,
    prerequisite_task_id INTEGER NOT NULL,
    PRIMARY KEY (dependent_task_id, prerequisite_task_id),
    FOREIGN KEY (dependent_task_id) REFERENCES task(task_id) ON DELETE CASCADE,
    FOREIGN KEY (prerequisite_task_id) REFERENCES task(task_id) ON DELETE CASCADE,
    CHECK (dependent_task_id <> prerequisite_task_id)
);


-- Job Table (contrarily to Task, Job are server internal tasks) - Jobs should stay in memory now?
CREATE TABLE IF NOT EXISTS job (
    job_id SERIAL PRIMARY KEY,
    worker_id INT REFERENCES worker(worker_id) ON DELETE SET NULL,
    action CHAR(1) NOT NULL DEFAULT 'C',  -- (C: Create (worker), D: Delete (worker), R: restart (worker), U: Update (worker))
    flavor_id INT REFERENCES flavor(flavor_id) ON DELETE SET NULL,
    region_id INT REFERENCES region(region_id) ON DELETE SET NULL,
    retry INT DEFAULT 0,
    status CHAR(1) NOT NULL DEFAULT 'P',  -- (P: Pending, R: Running, S: Succeeded, F: Failed, X: canceled)
    log TEXT DEFAULT '', 
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    modified_at TIMESTAMP NOT NULL DEFAULT NOW(),
    progression SMALLINT DEFAULT 0
    );

CREATE TABLE scitq_user (
    user_id     SERIAL PRIMARY KEY,
    username    TEXT UNIQUE NOT NULL,
    password    TEXT NOT NULL, -- This will be a bcrypt hash
    email       TEXT,
    is_admin    BOOLEAN DEFAULT FALSE,
    created_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE scitq_user_session (
    session_id TEXT PRIMARY KEY,
    user_id    INTEGER REFERENCES scitq_user(user_id),
    created_at TIMESTAMP DEFAULT NOW(),
    expires_at TIMESTAMP,
    last_used  TIMESTAMP
);