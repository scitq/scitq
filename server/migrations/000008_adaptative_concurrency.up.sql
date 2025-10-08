-- Add adaptive concurrency fields to recruiter
ALTER TABLE recruiter
  ADD COLUMN cpu_per_task   integer,
  ADD COLUMN memory_per_task float,
  ADD COLUMN disk_per_task   float,
  ADD COLUMN prefetch_percent  integer,
  ADD COLUMN concurrency_min integer,
  ADD COLUMN concurrency_max integer;