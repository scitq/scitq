-- Workflow chaining — template-to-template sequencing.
-- See specs/workflow_chain.md for the full design.
--
-- Chain entries are first-class rows (not opaque JSON on the parent), so
-- they can be enumerated, individually suspended/resumed/edited/cancelled
-- from the CLI before they fire, and their lifecycle is auditable.

CREATE TABLE workflow_chain_entry (
    chain_entry_id      SERIAL PRIMARY KEY,
    parent_workflow_id  INTEGER NOT NULL REFERENCES workflow(workflow_id) ON DELETE CASCADE,
    -- Position in the parent's `chain:` list, for stable ordering / display.
    idx                 INTEGER NOT NULL,
    template_name       TEXT    NOT NULL,
    -- Pinned version; NULL means "latest" at fire time.
    template_version    TEXT,
    -- JSON object mapping child param name → unresolved value expression
    -- (may contain {parent.params.X}, {parent.publish.<step>}, etc.).
    params_template     JSONB   NOT NULL DEFAULT '{}'::jsonb,
    -- Truthy condition expression. Default 'true' = always satisfied.
    when_expr           TEXT    NOT NULL DEFAULT 'true',
    -- Parent-status filter.
    on_status           TEXT    NOT NULL DEFAULT 'succeeded'
        CHECK (on_status IN ('succeeded', 'always', 'failed')),
    -- If true, fire a fresh child each time instead of reconciling via --continue.
    always_new          BOOLEAN NOT NULL DEFAULT false,
    -- Lifecycle. Transitions are documented in specs/workflow_chain.md.
    status              TEXT    NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'suspended', 'skipped', 'fired', 'failed', 'cancelled')),
    -- Populated when status=fired (the most recent child if re-fired on parent extend).
    child_workflow_id   INTEGER REFERENCES workflow(workflow_id) ON DELETE SET NULL,
    -- Populated when status=failed (chain firing itself failed).
    error_message       TEXT,
    created_at          TIMESTAMP NOT NULL DEFAULT NOW(),
    fired_at            TIMESTAMP,
    last_fired_at       TIMESTAMP,
    -- Stable position within a given parent's chain list.
    UNIQUE (parent_workflow_id, idx)
);

-- Quick lookup of all entries for a parent (the natural per-workflow query).
CREATE INDEX idx_chain_entry_parent ON workflow_chain_entry (parent_workflow_id);

-- Operator queries: "what's still armed across the system?" — only matches
-- the small subset of entries still awaiting/paused, so partial.
CREATE INDEX idx_chain_entry_armed ON workflow_chain_entry (status)
    WHERE status IN ('pending', 'suspended');

-- Backward lineage on the child workflow row — populated when a chain entry
-- fires. UI/CLI uses this to render parent links and to forbid cycles
-- without a second table lookup.
ALTER TABLE workflow
    ADD COLUMN parent_workflow_id INTEGER REFERENCES workflow(workflow_id) ON DELETE SET NULL;

-- Mirror at the template_run level so audit trails of "what launched this
-- run" stay symmetric. Nullable: NULL means "not a chained run".
ALTER TABLE template_run
    ADD COLUMN parent_template_run_id INTEGER REFERENCES template_run(template_run_id) ON DELETE SET NULL;

CREATE INDEX idx_workflow_parent ON workflow (parent_workflow_id)
    WHERE parent_workflow_id IS NOT NULL;
