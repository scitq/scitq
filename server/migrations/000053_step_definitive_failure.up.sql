-- Per-step "definitive failure" policy: signals the worker can emit
-- so the server knows a failed task shouldn't burn its retry budget.
-- Both fields opt-in; NULL / empty = today's behaviour (all failures
-- retryable, which is what task.retry was always doing).
--
-- definitive_exit_codes: when the task's docker/bare exit code is a
-- member of this list, the client stamps task.failure_class='definitive'
-- and the server retry gate (UpdateTaskStatus) skips the clone.
--
-- definitive_pattern: regex matched against the tail of stderr. Same
-- outcome. Useful when the underlying tool exits 1 regardless of
-- cause and only the message tells "input-corrupt, retry is useless"
-- apart from "transient failure, retry might succeed".
--
-- Reuses the existing task.failure_class TEXT column (migration 33).
-- 'definitive' joins 'eviction' / 'oom' / 'timeout' / etc. as another
-- recognised value.
ALTER TABLE step
  ADD COLUMN definitive_exit_codes INT[] NULL,
  ADD COLUMN definitive_pattern    TEXT  NULL;
