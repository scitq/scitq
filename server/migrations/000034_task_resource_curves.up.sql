-- Per-task resource curves: the full list the workflow author declared in
-- `task_spec.cpu/mem/disk: [v0, v1, ...]`. NULL or empty array = no curve
-- (scalar resource, today's behaviour). The retry-decision path reads the
-- curve and shifts task.min_cpu/min_mem/min_disk to curve[attempt] on
-- retry-clone creation (advance is gated on failure_class — see
-- migration 33 and addition_from_nextflow.md A).
ALTER TABLE task
    ADD COLUMN cpu_curve  DOUBLE PRECISION[] NULL,
    ADD COLUMN mem_curve  DOUBLE PRECISION[] NULL,
    ADD COLUMN disk_curve DOUBLE PRECISION[] NULL;
