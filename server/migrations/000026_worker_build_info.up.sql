-- Phase I of the worker version-awareness feature: persist the build
-- identity each worker reports on registration so the server can flag
-- workers running stale binaries. See specs/worker_autoupgrade.md.
--
-- All three columns are nullable. Pre-upgrade workers (running a binary
-- built before this commit) won't send these fields; their rows simply
-- get NULL and the upgrade-status helper returns "unknown" for them
-- until they restart on a binary that does report.
ALTER TABLE worker
    ADD COLUMN version    TEXT,
    ADD COLUMN commit_sha TEXT,
    ADD COLUMN build_arch TEXT;
