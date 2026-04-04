-- Add reuse columns to task table for opportunistic reuse
ALTER TABLE task ADD COLUMN reuse_key CHAR(64) NULL;
ALTER TABLE task ADD COLUMN reuse_hit BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE task ADD COLUMN reuse_original_output TEXT NULL;
CREATE INDEX idx_task_reuse_key ON task (reuse_key) WHERE reuse_key IS NOT NULL;

-- Reuse store: maps reuse_key -> output path of successful task
CREATE TABLE task_reuse (
    reuse_key    CHAR(64) PRIMARY KEY,
    output_path  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    task_id      INT NOT NULL,
    step_name    TEXT,
    workflow_id  INT
);

CREATE INDEX idx_task_reuse_created ON task_reuse (created_at);
