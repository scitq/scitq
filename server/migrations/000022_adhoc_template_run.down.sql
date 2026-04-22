-- Revert: drop ad-hoc columns and restore NOT NULL on workflow_template_id.
-- Delete any ad-hoc rows first (they have no template to point at).
DELETE FROM template_run WHERE workflow_template_id IS NULL;
ALTER TABLE template_run DROP COLUMN IF EXISTS script_sha256;
ALTER TABLE template_run DROP COLUMN IF EXISTS script_name;
ALTER TABLE template_run ALTER COLUMN workflow_template_id SET NOT NULL;
