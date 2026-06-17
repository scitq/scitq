-- One-shot fix for legacy workflow_template.params_schema rows whose
-- bool/int/float defaults (and choices) were stringified by the
-- pre-3a35e8c version of python/src/scitq2/param.py — that encoder
-- did `str(param.default)` and `[str(c) for c in param.choices]`,
-- turning a YAML `default: false` into the JSON string "False". The
-- UI's `<input type="checkbox" bind:checked={value}>` then read that
-- non-empty string as truthy and rendered the box CHECKED, so an
-- explicit `default: false` showed up flipped to true. Same trap for
-- `default: 0` on int/float (the string "0" is truthy in JS).
--
-- 3a35e8c fixed the encoder for new uploads. This migration rewrites
-- the params_schema JSON for templates that were already in the DB at
-- that point, so we don't have to carry a defensive coerce-on-read
-- layer in every consumer of param_json forever.
--
-- Idempotent: rows whose defaults are already native bool/int/float
-- (jsonb_typeof != 'string') skip the cast branches; the UPDATE then
-- short-circuits via IS DISTINCT FROM. Safe to re-run.

DO $$
DECLARE
    rec RECORD;
    new_schema JSONB;
    p JSONB;
    new_p JSONB;
    new_choices JSONB;
    c JSONB;
    s TEXT;
BEGIN
    FOR rec IN
        SELECT workflow_template_id, params_schema
        FROM workflow_template
        WHERE jsonb_typeof(params_schema) = 'array'
    LOOP
        new_schema := '[]'::jsonb;
        FOR p IN SELECT value FROM jsonb_array_elements(rec.params_schema)
        LOOP
            new_p := p;

            -- default: coerce stringified scalar to native type
            IF jsonb_typeof(new_p->'default') = 'string' THEN
                s := new_p->>'default';
                IF new_p->>'type' = 'bool' THEN
                    new_p := jsonb_set(
                        new_p, '{default}',
                        CASE WHEN lower(s) IN ('true','1','yes','on')
                             THEN 'true'::jsonb ELSE 'false'::jsonb END);
                ELSIF new_p->>'type' = 'int' AND s ~ '^-?[0-9]+$' THEN
                    new_p := jsonb_set(new_p, '{default}', to_jsonb(s::bigint));
                ELSIF new_p->>'type' = 'float'
                      AND s ~ '^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$' THEN
                    new_p := jsonb_set(new_p, '{default}', to_jsonb(s::double precision));
                END IF;
            END IF;

            -- choices: same coercion applied element-by-element. Only
            -- bool/int/float typed params can carry stringified
            -- numerics; for str/path/text/etc. the values were already
            -- strings pre-fix (str(str) is a no-op) and stay strings.
            IF jsonb_typeof(new_p->'choices') = 'array'
               AND new_p->>'type' IN ('bool', 'int', 'float') THEN
                new_choices := '[]'::jsonb;
                FOR c IN SELECT value FROM jsonb_array_elements(new_p->'choices')
                LOOP
                    IF jsonb_typeof(c) = 'string' THEN
                        s := c #>> '{}';
                        IF new_p->>'type' = 'bool' THEN
                            new_choices := new_choices ||
                                CASE WHEN lower(s) IN ('true','1','yes','on')
                                     THEN 'true'::jsonb ELSE 'false'::jsonb END;
                        ELSIF new_p->>'type' = 'int' AND s ~ '^-?[0-9]+$' THEN
                            new_choices := new_choices || to_jsonb(s::bigint);
                        ELSIF new_p->>'type' = 'float'
                              AND s ~ '^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$' THEN
                            new_choices := new_choices || to_jsonb(s::double precision);
                        ELSE
                            -- Couldn't parse — leave as-is rather than
                            -- silently coerce to a wrong value.
                            new_choices := new_choices || c;
                        END IF;
                    ELSE
                        new_choices := new_choices || c;
                    END IF;
                END LOOP;
                new_p := jsonb_set(new_p, '{choices}', new_choices);
            END IF;

            new_schema := new_schema || jsonb_build_array(new_p);
        END LOOP;

        IF new_schema IS DISTINCT FROM rec.params_schema THEN
            UPDATE workflow_template
            SET params_schema = new_schema
            WHERE workflow_template_id = rec.workflow_template_id;
        END IF;
    END LOOP;
END $$;
