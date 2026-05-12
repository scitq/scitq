-- scitq_auth: per-task opt-in flag instructing the worker to inject
-- SCITQ_SERVER + SCITQ_TOKEN env vars into the task and bind-mount the
-- worker's scitq CLI into the container. See proto Task.scitq_auth.
ALTER TABLE task
    ADD COLUMN scitq_auth BOOLEAN NOT NULL DEFAULT FALSE;
