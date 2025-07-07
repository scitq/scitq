DROP TABLE IF EXISTS template_run;
DROP TABLE IF EXISTS workflow_template;
ALTER TABLE task DROP COLUMN task_name;

-- unfix recruiter cascade
ALTER TABLE recruiter
DROP CONSTRAINT recruiter_step_id_fkey;
ALTER TABLE recruiter
ADD CONSTRAINT recruiter_step_id_fkey
FOREIGN KEY (step_id) REFERENCES step(step_id);

ALTER TABLE provider DROP CONSTRAINT provider_unique_name_config;
ALTER TABLE region DROP CONSTRAINT region_unique_provider_region_name;

-- unfix task cascade
ALTER TABLE task DROP CONSTRAINT IF EXISTS task_step_id_fkey;
ALTER TABLE task
ADD CONSTRAINT task_step_id_fkey
FOREIGN KEY (step_id) REFERENCES step(step_id) ON DELETE SET NULL;

DROP INDEX IF EXISTS idx_task_dependencies_by_prerequisite;