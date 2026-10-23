ALTER TABLE task
    DROP COLUMN IF EXISTS produced_files,
    DROP COLUMN IF EXISTS produced_bytes,
    DROP COLUMN IF EXISTS uploaded_files;
