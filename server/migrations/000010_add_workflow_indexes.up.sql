-- Add missing foreign-key indexes to accelerate cascading deletes
-- and all workflow/step/task lookups.

-- STEP → WORKFLOW
CREATE INDEX IF NOT EXISTS idx_step_workflow_id
    ON step(workflow_id);

-- TASK → STEP
CREATE INDEX IF NOT EXISTS idx_task_step_id
    ON task(step_id);

-- TASK_DEPENDENCIES.prerequisite_task_id
CREATE INDEX IF NOT EXISTS idx_taskdep_prereq
    ON task_dependencies(prerequisite_task_id);

-- WORKER → STEP (optional but improves cleanup)
CREATE INDEX IF NOT EXISTS idx_worker_step_id
    ON worker(step_id);

-- TASK.previous_task_id (optional)
CREATE INDEX IF NOT EXISTS idx_task_previous
    ON task(previous_task_id);