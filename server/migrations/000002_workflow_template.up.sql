CREATE TABLE workflow_template (
    workflow_template_id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    description TEXT,
    script_path TEXT NOT NULL,
    params_schema JSONB NOT NULL,
    uploaded_at TIMESTAMP NOT NULL DEFAULT NOW(),
    uploaded_by INTEGER REFERENCES scitq_user(user_id) ON DELETE SET NULL,
    UNIQUE(name, version)
);

CREATE TABLE template_run (
    template_run_id SERIAL PRIMARY KEY,
    workflow_template_id INTEGER NOT NULL REFERENCES workflow_template(workflow_template_id) ON DELETE CASCADE,
    param_values JSONB NOT NULL,
    workflow_id INTEGER REFERENCES workflow(workflow_id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    status CHAR NOT NULL DEFAULT 'P', -- (P: Pending, S: Success, F: Failed)
    run_by INTEGER REFERENCES scitq_user(user_id) ON DELETE SET NULL,
    error_message TEXT
);

ALTER TABLE task ADD COLUMN task_name TEXT;

ALTER TABLE provider ADD CONSTRAINT provider_unique_name_config UNIQUE (provider_name, config_name);
ALTER TABLE region ADD CONSTRAINT region_unique_provider_region_name UNIQUE (provider_id, region_name);

-- fix recruiter cascade
ALTER TABLE recruiter
DROP CONSTRAINT recruiter_step_id_fkey;
ALTER TABLE recruiter
ADD CONSTRAINT recruiter_step_id_fkey
FOREIGN KEY (step_id) REFERENCES step(step_id) ON DELETE CASCADE;

-- fix task cascade
ALTER TABLE task DROP CONSTRAINT IF EXISTS task_step_id_fkey;
ALTER TABLE task
ADD CONSTRAINT task_step_id_fkey
FOREIGN KEY (step_id) REFERENCES step(step_id) ON DELETE CASCADE;

CREATE INDEX idx_task_dependencies_by_prerequisite
ON task_dependencies (prerequisite_task_id);