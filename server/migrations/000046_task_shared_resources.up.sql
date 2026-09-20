-- Shared memory / disk overhead per worker.
--
-- Some tools (our in-house hermes aligner, bowtie2 with large indexes,
-- kraken2 with big databases) load a large read-only reference once
-- per host and then handle multiple queries against it in parallel.
-- The reference dominates the memory footprint but does NOT scale with
-- concurrency — mmap-ing the same file from N containers costs one page
-- cache on the host, not N. Same story for a decompressed reference on
-- disk.
--
-- Reading B semantics (chosen 2026-09-19):
--   * min_mem       = per-task INCREMENTAL memory
--   * min_mem_shared = additional shared overhead once per worker
--   * total on worker = min_mem_shared + concurrency * min_mem
--   * concurrency   = floor((worker.mem - min_mem_shared) / min_mem)
--
-- Same relations for disk. CPU has no shared column — CPU sharing is
-- rare and modelled poorly (hyperthreading, cache, memory bandwidth
-- all confuse the story); adding it here would be premature.
--
-- Curves: mirror mem_curve / disk_curve. When a retry escalates
-- min_mem along mem_curve, min_mem_shared shifts along
-- mem_shared_curve at the same retry_count index. Either curve can be
-- NULL independently (e.g. per-task mem grows on retry but the shared
-- index stays the same size).
--
-- NULL semantics: NULL is equivalent to 0 — today's linear model.
-- The assignment / recruiter code coalesces NULL → 0, so a task /
-- recruiter submitted before this feature keeps behaving exactly as
-- before with no migration work on the callers.
--
-- This is a USER-DECLARED INVARIANT, not a hard guarantee: nothing on
-- the worker verifies that the tool actually mmaps a shared file.
-- Setting min_mem_shared for a tool that loads its own copy per task
-- will over-commit the worker and OOM. Documented loudly on the DSL
-- side.
ALTER TABLE task
  ADD COLUMN min_mem_shared     FLOAT NULL,
  ADD COLUMN min_disk_shared    FLOAT NULL,
  ADD COLUMN mem_shared_curve   REAL[] NULL,
  ADD COLUMN disk_shared_curve  REAL[] NULL;

ALTER TABLE recruiter
  ADD COLUMN memory_shared_per_task FLOAT NULL,
  ADD COLUMN disk_shared_per_task   FLOAT NULL;
