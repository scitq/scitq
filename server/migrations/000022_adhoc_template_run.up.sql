-- Local Python runs (direct `python my_script.py`, no template uploaded)
-- now register a lightweight template_run row for traceability. The row has
-- a NULL workflow_template_id and records the launching script's name and
-- content SHA so you can later answer "what produced this workflow?" with a
-- single lookup.
ALTER TABLE template_run ALTER COLUMN workflow_template_id DROP NOT NULL;
ALTER TABLE template_run ADD COLUMN script_name TEXT;
ALTER TABLE template_run ADD COLUMN script_sha256 CHAR(64);
