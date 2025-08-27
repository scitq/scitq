-- 001_fix_flavor_region_pk.down.sql
BEGIN;

-- 1) Drop the composite PK and FKs
ALTER TABLE flavor_region
  DROP CONSTRAINT IF EXISTS flavor_region_pk,
  DROP CONSTRAINT IF EXISTS flavor_region_flavor_fk,
  DROP CONSTRAINT IF EXISTS flavor_region_region_fk;

-- 2) Recreate a sequence if needed and restore SERIAL-like behavior
DO $$
BEGIN
   IF NOT EXISTS (
     SELECT 1 FROM pg_class WHERE relkind = 'S' AND relname = 'flavor_region_flavor_id_seq'
   ) THEN
     CREATE SEQUENCE flavor_region_flavor_id_seq;
   END IF;
END$$;

-- Ensure sequence is at least the current max
SELECT setval('flavor_region_flavor_id_seq',
              COALESCE((SELECT MAX(flavor_id) FROM flavor_region), 0));

-- 3) Restore the (flawed) single-column PK on flavor_id
ALTER TABLE flavor_region
  ALTER COLUMN flavor_id SET DEFAULT nextval('flavor_region_flavor_id_seq'::regclass);

ALTER TABLE flavor_region
  ADD CONSTRAINT flavor_region_pkey PRIMARY KEY (flavor_id);

COMMIT;