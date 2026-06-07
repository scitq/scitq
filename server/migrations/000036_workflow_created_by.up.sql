-- Per-workflow ownership column. Previously workflows could only be
-- attributed to a user via template_run.run_by, which left workflows
-- created outside the template flow (CLI submit, direct DSL run,
-- internal helpers) with no user link at all. That made ListWorkflows'
-- @me filter silently incomplete: it returned only the
-- template-launched subset of a user's workflows, not all of them.
ALTER TABLE workflow
    ADD COLUMN created_by INTEGER REFERENCES scitq_user(user_id) ON DELETE SET NULL;

-- Backfill from template_run where the link exists. A workflow can
-- have multiple template_run rows (re-runs, chains, ad-hoc re-submits)
-- with potentially different run_by users — we pick the MOST RECENT
-- one as the canonical owner (DISTINCT ON + ORDER BY template_run_id
-- DESC). Workflows without any template_run row stay NULL (we
-- genuinely don't have the data; a separate sweep can claim them if a
-- heuristic ever becomes available).
UPDATE workflow w
SET created_by = sub.run_by
FROM (
    SELECT DISTINCT ON (workflow_id) workflow_id, run_by
    FROM template_run
    WHERE run_by IS NOT NULL
    ORDER BY workflow_id, template_run_id DESC
) sub
WHERE sub.workflow_id = w.workflow_id
  AND w.created_by IS NULL;

-- Index supports the ListWorkflows @me / per-user filter without
-- forcing a sequential scan of the workflow table.
CREATE INDEX IF NOT EXISTS workflow_created_by_idx ON workflow(created_by);
