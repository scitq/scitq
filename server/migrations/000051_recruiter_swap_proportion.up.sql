-- Per-recruiter swap sizing override.
--
-- Default (NULL) = use the server-wide scitq.swap_proportion config
-- (0.10 unless overridden), i.e. today's behaviour. A non-NULL value
-- overrides for every worker this recruiter spawns:
--   swap_proportion = 0     → disable swap entirely
--   swap_proportion = 0.20  → 20% of /scratch dedicated to the swapfile
--
-- Rationale: on huge-RAM nodes where /scratch is tight, dedicating 10%
-- to a swapfile that will never page is pure waste. The step author
-- declares the override once in the recruiter YAML and every worker
-- the recruiter deploys skips (or resizes) its swapfile at install.
ALTER TABLE recruiter
  ADD COLUMN swap_proportion REAL;

-- Workers remember the swap_proportion they were deployed with, so the
-- recycling eligibility check can reject a worker whose swap sizing
-- doesn't match the requesting recruiter. NULL = deployed with the
-- server config default (matches a recruiter whose swap_proportion is
-- also NULL — the recycler compares resolved effective values, not raw
-- NULLs; see recruitment.recycleWorkers).
ALTER TABLE worker
  ADD COLUMN swap_proportion REAL;
