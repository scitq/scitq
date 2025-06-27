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
    error_message TEXT
);

CREATE INDEX idx_task_dependencies_by_prerequisite
ON task_dependencies (prerequisite_task_id);