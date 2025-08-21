CREATE TABLE worker_event (
  event_id     BIGSERIAL PRIMARY KEY,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  worker_id    BIGINT NULL REFERENCES worker(worker_id) ON DELETE SET NULL,
  worker_name  TEXT,
  level        CHAR(1) NOT NULL DEFAULT 'I',   -- D:Debug, I:Info, W:Warning, E:Error
  event_class  TEXT NOT NULL,                  -- e.g. 'install', 'bootstrap', 'runtime', 'network', 'auth', 'filesystem', 'config'
  message      TEXT NOT NULL,                  -- human description
  details_json JSONB                           -- structured context (stderr, exit_code, env, os/arch, versions…)
);

-- Useful filters
CREATE INDEX idx_worker_event_created ON worker_event (created_at DESC);
CREATE INDEX idx_worker_event_worker  ON worker_event (worker_id);
CREATE INDEX idx_worker_event_level   ON worker_event (level);
CREATE INDEX idx_worker_event_class   ON worker_event (event_class);
