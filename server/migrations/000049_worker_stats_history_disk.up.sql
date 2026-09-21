-- Add per-sample disk peak to worker_stats_history.
--
-- The sampler already sees every disk's usage_percent when it reads
-- WorkerStats (client/workerstats reports the `disks` slice). This
-- column stores the MAX across disks × MAX over the sampler interval
-- since the previous ping — the same "worst-case in the window" shape
-- as peak_cpu_percent / peak_mem_percent / peak_iowait_percent.
--
-- Nullable so rows written before the client-side sampler learned to
-- track disk stay valid.
ALTER TABLE worker_stats_history
    ADD COLUMN peak_disk_percent REAL NULL;
