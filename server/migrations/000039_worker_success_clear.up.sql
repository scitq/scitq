-- Sibling of failures_cleared_at (migration 38), for the dashboard's
-- per-worker "Success" column. The two timestamps gate the per-worker
-- F and S counts independently in GetTaskStatusCounts, so the operator
-- can dismiss historical task tallies on a worker without losing the
-- underlying task rows. Same "ack, don't delete" pattern.
ALTER TABLE worker
  ADD COLUMN successes_cleared_at TIMESTAMPTZ DEFAULT NULL;
