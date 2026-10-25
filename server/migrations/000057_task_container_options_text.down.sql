ALTER TABLE task ALTER COLUMN container_options TYPE TEXT[]
    USING CASE WHEN container_options IS NULL THEN NULL
               ELSE string_to_array(container_options, ' ') END;
ALTER TABLE task ALTER COLUMN container_options SET DEFAULT '{}';
