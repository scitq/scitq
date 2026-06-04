-- publish_mode: "move" (default, today's behaviour — successful task uploads
-- to the publish URI *instead of* the workspace) or "copy" (uploads to *both*,
-- so a downstream consumer in the same workflow can still fetch from the
-- workspace while the published copy lands in the results bucket).
--
-- NULL = "move" (existing tasks behave as today). Spec:
-- addition_from_nextflow.md B.
ALTER TABLE task
    ADD COLUMN publish_mode TEXT NULL;
