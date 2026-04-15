# Optimization Guide

This guide covers practical techniques for making scitq workflows faster and cheaper. Two main levers: **worker efficiency** (getting more work done per compute-hour) and **transfer costs** (avoiding unnecessary cross-region data movement).

## Worker efficiency

### Large workers with task_spec

The single most impactful optimization: use large workers (32 CPU, 120GB+ RAM) and constrain tasks with `task_spec` so multiple tasks run per worker.

```yaml
worker_pool:
  cpu: "== 32"
  mem: ">= 120"
  disk: ">= 400"
  max_recruited: 10

steps:
  - name: align
    task_spec:
      cpu: 8
      mem: 30
```

This gives 4 task slots per worker (32 / 8 = 4). Advantages:

- **Less overhead**: one VM running 4 tasks costs less than 4 VMs running 1 each (boot time, base OS cost, Docker image pull amortized once).
- **Prefetch works**: while 4 tasks run, the next task's inputs are already downloading. When a slot frees up, the task starts instantly with no download wait.
- **Resource sharing**: large reference databases (hermes indexes, bowtie2 indexes) are downloaded once per worker and memory-mapped. With 4 concurrent tasks, the mmap page cache is shared — 4× the benefit for 1× the memory cost.

Without `task_spec`, each task gets the full worker (concurrency = 1). There's no overlap, no prefetch benefit, and each worker pays the full resource download cost.

### Prefetch

`prefetch` controls how many tasks a worker downloads in advance while executing its current tasks.

```yaml
worker_pool:
  prefetch: 1
```

With `prefetch: 1`, the worker always has one task ready to go. The moment a slot frees up, the next task begins execution immediately — zero download wait. This is especially important for tasks with large inputs (e.g., FASTQ files from cloud storage).

Guidelines:
- **`prefetch: 1`** is almost always the right choice for bioinformatics workflows.
- **`prefetch: 0`** (default) means sequential download-then-execute: the worker is idle while fetching the next task's inputs.
- **Higher values** (`prefetch: 2-3`) help for very fast tasks (< 1 minute) with large inputs, where even one prefetch isn't enough to hide the download latency.
- **Disk constraint**: each prefetched task's inputs consume disk space. With 4 concurrent tasks + 1 prefetch on a worker with limited disk, ensure `disk` in the `worker_pool` is large enough.

Prefetch only shines with the large-worker pattern (multiple task slots). With a single-task worker, there's no concurrency to overlap with.

### Warm-up and memory-mapped resources

Tools like hermes, bowtie2, and kraken2 use memory-mapped indexes. The first task on a fresh worker is slow because the OS loads the index into the page cache. Subsequent tasks on the same worker are fast because the pages are already cached.

This means:
- **Don't panic at slow first tasks** — it's warm-up, not a bug.
- **Larger workers amplify the benefit** — more concurrent tasks sharing the same cached pages.
- **Worker recycling** (`recyclable_scope: W`) lets workers move between steps within the same workflow, but a step change means different resources and a new warm-up. Recycling across workflows (`recyclable_scope: G`) can help if workflows use the same indexes.

### Eviction and task_batches

When a recruiter deploys workers, `task_batches` controls how many "rounds" of tasks to target:

```yaml
worker_pool:
  task_batches: 2
```

With `task_batches: 2` and 100 pending tasks on workers with 4 slots each, the recruiter deploys enough workers for 2 batches: `ceil(100 / 4) / 2 = 13 workers`. This prevents over-provisioning — workers aren't left idle at the end.

For short-lived tasks with fast turnaround, `task_batches: 2` is a good default. For long tasks (hours), `task_batches: 1` is fine — you want all tasks running ASAP.

Workers are automatically deleted by the watchdog after `idle_timeout` seconds with no work (default 300s). Permanent workers are exempt from idle deletion — use the UI toggle or `scitq worker update --is-permanent` to protect critical workers.

## Transfer costs

Cloud providers charge for data egress (data leaving a region or crossing provider boundaries). scitq has several features to minimize these costs.

### Workspace locality

The `workspace:` field should match your `worker_pool` location so task outputs stay in the same region as the workers:

```yaml
worker_pool:
  provider: "{params.location}"

workspace: "{params.location}"
```

Behind the scenes, the server resolves the `provider:region` pair into a storage URI via `local_workspaces` in `scitq.yaml`. By keeping workspace and workers in the same region, inter-step data transfer is free (intra-region transfers are typically free on all providers).

### Resource locality with {RESOURCE_ROOT}

Large reference databases (hermes indexes, bowtie2 indexes) should be copied to each provider's local storage rather than downloaded cross-region for every worker:

```yaml
resource: "{RESOURCE_ROOT}/igc2.herm"
```

When the provider has a `local_resources` entry in `scitq.yaml`, `{RESOURCE_ROOT}` resolves to the local path. Workers download from their nearby storage instead of crossing provider boundaries.

**Setup**: copy your reference data to each provider's storage once (e.g., `rclone copy azure://rnd/resource/igc2.herm s3://rnd/resource/igc2.herm`), then configure `local_resources` for each provider. The per-worker download cost drops to zero egress fees.

Without `{RESOURCE_ROOT}`, all workers download from the same source (e.g., Azure), paying egress fees even when running on OVH.

### Input data location

For workflows ingesting external data (ENA, SRA, private URIs):
- **SRA with `sra-aws`**: data is fetched from AWS S3 (free egress to AWS workers, cheap to other clouds).
- **ENA with `ena-ftp`/`ena-aspera`**: data is fetched from European FTP/Aspera servers (cheaper for European providers like OVH).
- **Private URIs**: place input data in the same provider/region as your workers.

The `download_method` parameter in YAML templates controls which source is used. Pick the one closest to your workers.

### Publish vs workspace

`publish:` is for final results — data you want to keep long-term. `workspace:` is for intermediate data between steps.

```yaml
workspace: "{params.location}"     # intermediate data, same region as workers
publish_root: "azure://results/"   # final results, wherever you want them
```

Only the last step (or a compile/aggregate step) should use `publish: true`. Intermediate steps use the workspace automatically. This way, only final results cross regions — intermediate data stays local.

## Quick checklist

| What | Recommendation |
|------|---------------|
| Worker size | Large (32 CPU, 120GB+ RAM) with `task_spec` for slot splitting |
| Prefetch | `prefetch: 1` (always) |
| task_batches | `2` for short tasks, `1` for long tasks |
| Workspace | Same `provider:region` as workers |
| Resources | Use `{RESOURCE_ROOT}`, copy to each provider once |
| Publish | Only on final step, not intermediates |
| Input data | Choose `download_method` closest to workers |
