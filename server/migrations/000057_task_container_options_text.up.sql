-- task.container_options was created as TEXT[] but has always been read and
-- written as a single string of `docker run` options (proto, server, worker,
-- YAML `container_options`). Any non-empty value was rejected by PostgreSQL
-- ("malformed array literal"). Store it as the string it is.
ALTER TABLE task ALTER COLUMN container_options DROP DEFAULT;
ALTER TABLE task ALTER COLUMN container_options TYPE TEXT
    USING array_to_string(container_options, ' ');
