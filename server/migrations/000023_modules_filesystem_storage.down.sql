-- Revert the nullable-content relaxation. Only safe if every row still
-- has a non-NULL content (i.e. the startup hook hasn't run, or hasn't
-- finished dumping to disk and clearing). In practice once the startup
-- hook has cleared content columns, the down migration will fail —
-- which is fine: we never expect to go back. The file tree remains
-- intact regardless.
ALTER TABLE module ALTER COLUMN content SET NOT NULL;
