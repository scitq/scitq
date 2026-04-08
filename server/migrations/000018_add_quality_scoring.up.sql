-- Quality scoring: step-level definition, task-level results
ALTER TABLE step ADD COLUMN quality_definition JSONB NULL;
ALTER TABLE task ADD COLUMN quality_score FLOAT NULL;
ALTER TABLE task ADD COLUMN quality_vars JSONB NULL;
