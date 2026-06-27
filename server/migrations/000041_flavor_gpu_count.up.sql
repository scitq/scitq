-- Per-flavor GPU count. Until now we modeled GPU presence as a
-- boolean (flavor.has_gpu); that's correct as a binary "has at least
-- one GPU" gate but leaks the multi-GPU dimension out of every
-- downstream calculation. Per task_spec.gpu semantics (a task asks
-- for N GPUs), the fit predicate, concurrency derivation, and the
-- worker-side per-task device allocator all need the count.
--
-- Backfilled from has_gpu (1 if true, 0 otherwise) so existing
-- deployments stay correct on the single-GPU SKUs that are the
-- common case. The next flavor-sync pass overwrites the value from
-- the per-SKU table (updater/azure/resource/gpu.tsv: "4xTesla K80",
-- "2xH100", …) so multi-GPU SKUs get the right count without
-- operator intervention.
ALTER TABLE flavor
  ADD COLUMN gpu_count INT NOT NULL DEFAULT 0;

UPDATE flavor SET gpu_count = 1 WHERE has_gpu = TRUE;
