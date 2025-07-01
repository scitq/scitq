DROP TABLE IF EXISTS template_run;
DROP TABLE IF EXISTS workflow_template;
ALTER TABLE task DROP COLUMN task_name;

DROP INDEX IF EXISTS idx_task_dependencies_by_prerequisite;