BEGIN;

CREATE INDEX IF NOT EXISTS idx_workflow_status
  ON workflow (status);

CREATE INDEX IF NOT EXISTS idx_task_step_status_hidden
  ON task (step_id, status, hidden);

WITH per_wf AS (
  SELECT
    s.workflow_id,
    COUNT(*) AS total,
    COUNT(*) FILTER (WHERE t.status = 'S') AS succeeded,
    COUNT(*) FILTER (WHERE t.status = 'F') AS failed,
    COUNT(*) FILTER (WHERE t.status IN ('S','F','W')) AS terminal_or_wait
  FROM step s
  JOIN task t ON t.step_id = s.step_id
  WHERE NOT t.hidden
  GROUP BY s.workflow_id
),
desired AS (
  SELECT
    workflow_id,
    CASE
      WHEN total > 0 AND succeeded = total THEN 'S'
      WHEN total > 0 AND terminal_or_wait = total AND failed > 0 THEN 'F'
      ELSE NULL
    END AS new_status
  FROM per_wf
)
UPDATE workflow w
SET status = d.new_status
FROM desired d
WHERE w.workflow_id = d.workflow_id
  AND d.new_status IS NOT NULL;

COMMIT;
