-- Operator-acknowledgeable counters on workers.
--
-- Background: a worker can accumulate two kinds of "needs attention"
-- state — task failures (recent_failures counted by ListWorkers from
-- F-status tasks since last success) and warning-level worker_events
-- (today: no-fit, future: anything emitted at level='W'). Both
-- conditions persist indefinitely once raised: the failure count
-- only drops when a fresh S task lands, the warning events sit in
-- worker_event forever.  Operators have no way to say "I saw this,
-- clear it" without writing SQL.
--
-- These two columns add that affordance:
--   worker.failures_cleared_at: gates the recent_failures subquery so
--     resetting just bumps the lower bound. F-tasks before that
--     timestamp are no longer counted, but stay on the row for audit.
--   worker_event.acknowledged_at: same idea per-event. The UI's
--     pending_warnings count filters on `acknowledged_at IS NULL`,
--     so acking flips the badge back to ⓘ without losing the event.

ALTER TABLE worker
  ADD COLUMN failures_cleared_at TIMESTAMPTZ DEFAULT NULL;

ALTER TABLE worker_event
  ADD COLUMN acknowledged_at TIMESTAMPTZ DEFAULT NULL;

-- Index supports the per-worker pending-warnings count surfaced in
-- ListWorkers. Partial index keeps it small — the long tail of
-- acked events is irrelevant here.
CREATE INDEX idx_worker_event_unacked_warnings
  ON worker_event (worker_id)
  WHERE level = 'W' AND acknowledged_at IS NULL;
