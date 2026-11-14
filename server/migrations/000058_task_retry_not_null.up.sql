-- A task without a retry budget has retry 0. The dependency release reads
-- `retry = 0` as "this failure is final", which NULL never satisfied.
UPDATE task SET retry = 0 WHERE retry IS NULL;
ALTER TABLE task ALTER COLUMN retry SET DEFAULT 0;
ALTER TABLE task ALTER COLUMN retry SET NOT NULL;
