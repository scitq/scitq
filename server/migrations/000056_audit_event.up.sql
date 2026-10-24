-- Audit trail of decisions: who or what changed a worker, task, workflow,
-- recruiter or template, through which channel. Task status transitions
-- reported by workers are not recorded here (task timestamps and worker
-- events cover them).
CREATE TABLE audit_event (
    audit_id    BIGSERIAL PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    actor_kind  TEXT NOT NULL,        -- 'user' or 'scheduler'
    actor_name  TEXT NOT NULL,        -- username, or scheduler component (e.g. 'recruiter step 75230 rank 2')
    channel     TEXT NOT NULL,        -- 'cli', 'ui', 'mcp' or 'scheduler'
    client      TEXT NULL,            -- MCP client name when known (e.g. 'claude-code')
    action      TEXT NOT NULL,        -- e.g. 'worker.update', 'task.retry'
    object_kind TEXT NOT NULL,        -- 'worker', 'task', 'step', 'workflow', 'recruiter', 'template'
    object_id   INT  NOT NULL,
    details     JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX idx_audit_event_object ON audit_event (object_kind, object_id, created_at);
CREATE INDEX idx_audit_event_created ON audit_event (created_at);
