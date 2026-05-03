ALTER TABLE worker
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS build_arch;
