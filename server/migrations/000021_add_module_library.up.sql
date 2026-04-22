-- Module library: a single versioned store for YAML modules, shared between
-- bundled (shipped with scitq) and user-uploaded entries. See
-- specs/module_library.md.

CREATE TABLE module (
    module_id    SERIAL PRIMARY KEY,
    path         TEXT NOT NULL,
    version      TEXT NOT NULL,
    content      BYTEA NOT NULL,
    content_sha  TEXT NOT NULL,
    -- Origin:
    --   'B' bundled — seeded from the scitq2_modules package, unmodified.
    --   'L' local   — uploaded by a user, no bundled counterpart.
    --   'F' forked  — originated from a bundled row then edited locally.
    origin       CHAR(1) NOT NULL CHECK (origin IN ('B','L','F')),
    bundled_sha  TEXT,
    uploaded_at  TIMESTAMP NOT NULL DEFAULT NOW(),
    uploaded_by  INTEGER REFERENCES scitq_user(user_id) ON DELETE SET NULL,
    description  TEXT,
    UNIQUE (path, version)
);

CREATE INDEX module_path_idx ON module (path);

-- Pins record the concrete (path, version) actually resolved for each
-- `import:` in a given template_run, so a replay can use the same module
-- content even after a later `scitq module upgrade` ships a newer version.
ALTER TABLE template_run ADD COLUMN module_pins JSONB;
