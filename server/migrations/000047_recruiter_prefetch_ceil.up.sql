-- Round-up option for the dynamic prefetch percentage.
--
-- Default is floor: `worker_prefetch = floor(percent * concurrency / 100)`.
-- On a small worker (concurrency=2) with prefetch_percent=25, floor gives
-- 0 — usually not what the author wants, because a worker with 0 prefetch
-- waits for a download between every task.
--
-- When prefetch_percent_ceil=TRUE, the same three call sites (recruit,
-- recycle, reassign) compute `ceil(percent * concurrency / 100)` instead.
-- A positive percent then always yields at least 1 prefetch slot.
--
-- Opt-in per recruiter. FALSE (default) preserves today's behaviour for
-- every existing row.
ALTER TABLE recruiter
  ADD COLUMN prefetch_percent_ceil BOOLEAN NOT NULL DEFAULT FALSE;
