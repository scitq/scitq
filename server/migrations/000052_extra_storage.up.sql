-- Per-recruiter extra block storage (OVH / OpenStack only for now;
-- Azure returns a clear error if requested).
--
-- extra_storage_gb = NULL or 0 → no extra volume (today's behaviour).
-- extra_storage_gb > 0           → provision a Cinder volume of that
--                                   size, attach to the worker, mount
--                                   at /scratch BEFORE scitq-client
--                                   installs. Useful when /scratch
--                                   needs to be larger than the flavor's
--                                   root disk — e.g. metagenomics tasks
--                                   on a small-flavor huge-RAM node.
--
-- extra_storage_type is the provider's volume class name (OVH:
-- "classic" / "high-speed" / "high-speed-gen2"). NULL → provider
-- default. Passed through verbatim to the OpenStack API; validation
-- happens there.
ALTER TABLE recruiter
  ADD COLUMN extra_storage_gb   INT  NULL,
  ADD COLUMN extra_storage_type TEXT NULL;

-- The worker row remembers what it was deployed with. Two reasons:
--   1. Recycling eligibility — a worker born with 500 GB extra can't
--      be reused by a recruiter that asked for 1 000 GB extra (block
--      volumes can't be reshaped mid-flight). Same shape as the
--      swap_proportion match in recruitment.selectWorkersForRecruiter.
--   2. Delete lifecycle — the volume handle lives here so the delete
--      path (and the orphan-volume janitor as a fallback) can detach
--      and destroy it even if the server row is already gone.
ALTER TABLE worker
  ADD COLUMN extra_storage_gb        INT  NULL,
  ADD COLUMN extra_storage_type      TEXT NULL,
  ADD COLUMN extra_storage_volume_id TEXT NULL;
