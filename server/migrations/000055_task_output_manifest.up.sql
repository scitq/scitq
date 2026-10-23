-- Per-task output manifest, reported by the worker with the terminal status.
-- produced_files / produced_bytes: what the program left in /output.
-- uploaded_files: how many of those files reached every upload target with
-- a matching size. NULL = not reported (task never reached the upload phase,
-- or ran on a worker predating this column).
ALTER TABLE task
    ADD COLUMN produced_files   INT    NULL,
    ADD COLUMN produced_bytes   BIGINT NULL,
    ADD COLUMN uploaded_files INT    NULL;
