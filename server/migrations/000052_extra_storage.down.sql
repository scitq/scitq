ALTER TABLE worker
  DROP COLUMN extra_storage_volume_id,
  DROP COLUMN extra_storage_type,
  DROP COLUMN extra_storage_gb;

ALTER TABLE recruiter
  DROP COLUMN extra_storage_type,
  DROP COLUMN extra_storage_gb;
