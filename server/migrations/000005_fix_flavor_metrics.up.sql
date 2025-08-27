-- 001_fix_flavor_region_pk.up.sql
BEGIN;

-- 1) Drop the flavor_region matching non-existing flavors
DELETE FROM flavor_region
WHERE flavor_id NOT IN (SELECT flavor_id FROM flavor);

-- 2) Drop the old (wrong) PK on flavor_id if present
ALTER TABLE flavor_region
  DROP CONSTRAINT IF EXISTS flavor_region_pkey;

-- 3) If SERIAL created a default from a sequence, drop it (we'll keep the sequence around unused)
ALTER TABLE flavor_region
  ALTER COLUMN flavor_id DROP DEFAULT;

-- 4) Add the correct foreign keys
ALTER TABLE flavor_region
  ADD CONSTRAINT flavor_region_flavor_fk
    FOREIGN KEY (flavor_id) REFERENCES flavor(flavor_id) ON DELETE CASCADE,
  ADD CONSTRAINT flavor_region_region_fk
    FOREIGN KEY (region_id) REFERENCES region(region_id) ON DELETE CASCADE;

-- 5) Add the proper composite primary key
ALTER TABLE flavor_region
  ADD CONSTRAINT flavor_region_pk PRIMARY KEY (flavor_id, region_id);

COMMIT;