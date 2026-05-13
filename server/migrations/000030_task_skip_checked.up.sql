-- skip_checked: one-shot marker for skipExistingTasks. Tasks with
-- skip_if_exists=true used to be re-listed via rclone on every assign
-- tick (every ~5s) for the entire run, because the only thing pulling
-- them out of the loop was being skipped (P→S). Now the runner stamps
-- this flag after each rclone check, so a task that *didn't* skip is
-- left alone for the rest of the run. The check is logically "did the
-- output exist when this task became pending?" — a one-shot question,
-- not a poll.
ALTER TABLE task
    ADD COLUMN skip_checked BOOLEAN NOT NULL DEFAULT FALSE;
