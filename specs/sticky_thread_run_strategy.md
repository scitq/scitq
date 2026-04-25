# Sticky / Thread run_strategy

## Status

- DB column `workflow.run_strategy` exists (since `000001_init_schema.up.sql`) and accepts `B`/`T`/`D`/`Z`.
- gRPC `WorkflowRequest.run_strategy` exists.
- CLI `scitq workflow create --run-strategy T` exists.
- **Python DSL plumbing now wires it through** (this change): `Workflow(run_strategy='thread')` and YAML `run_strategy: thread` set the field on `CreateWorkflow`.
- **What's missing**: the server-side scheduling and the worker-side I/O-skip logic that would make `T` actually behave differently from `B`. Today `T` is stored, listed, and otherwise ignored.

## Motivation

A grouped fan-in step (e.g. `metagenomics/simka` consuming all per-sample fastqs) currently forces a workspace round-trip that exists purely to satisfy task isolation: each per-sample upstream task uploads its outputs to azure/S3 workspace, the grouped task downloads them all back. For metagenomics-scale data (10s of GB per workflow), that's measurable wall-time even on free intra-region bandwidth. The `T`-strategy escape hatch is to pin a *thread* of related tasks to the same worker and skip the round-trip — outputs of upstream tasks stay on local disk and the next task in the thread reads them directly.

For the scitq-simka use case specifically: 11 per-sample stage tasks + 1 grouped simka task all on one worker, no upload, no re-download.

## Sticky-thread semantics — proposed

A "thread" in `run_strategy=T` is a set of tasks that must run on the same worker, in dependency order. The sticky surface comes from two rules:

1. **Pinning**: once a worker accepts the first task in a thread, the scheduler reserves that worker for every other task in the same thread. Other workers will not get those tasks.
2. **Local-handoff I/O**: tasks in the same thread that consume each other's outputs read them directly from local disk on the worker, skipping the workspace upload from the producer task and the workspace download for the consumer task.

A workflow with `run_strategy=T` is thread-mode. The scheduler partitions its tasks into threads. The natural partitioning is **by `tag` on per-sample steps + a dependency edge into a downstream grouped step**:

- All tasks tagged `tag=sample_X` form a per-sample thread (fastp.sample_X → humanfilter.sample_X → seqtk.sample_X → …).
- A grouped step that fans in from those per-sample tasks is its own one-task thread, but it **must** be co-located with at least one of the upstream threads. The simplest choice: pin the grouped task to the same worker that ran the *last* per-sample task feeding it. The other upstream threads' outputs still need to land on that worker — for now, fall back to the workspace round-trip for those, which is already today's behavior. Net win: no round-trip for the *one* sample's outputs that already live on the simka worker, so for an 11-sample fan-in we save 1/11 of the I/O. Not huge, but a real saving with no extra coordination cost.
- A more aggressive variant: pin **all** per-sample threads to the *same* worker. The simka grouped task then sees every sample's outputs on local disk and the round-trip vanishes entirely. This is what most users would call "thread-wise execution." Cost: one worker takes the whole load, no parallelism; suitable for small-N fan-ins (n=11 trumatrix), unsuitable for large N. A `cluster_size: 1` variant of the worker pool definition is probably the cleanest expression.

## Components needed for the actual implementation

### 1. Server scheduler

- Read `wf.run_strategy='T'` and treat task assignment differently:
  - When dispatching the first task of a thread to a worker, mark that worker as "owning" the thread (a new `worker.owned_thread_ids` array column or a `task_thread_assignment` join table).
  - When dispatching subsequent tasks of the same thread, only consider the owner worker (or fail if it's gone).
- Define what a "thread" *is* for assignment purposes. Likely a `thread_id` column on `task` populated at workflow compile time. For per-sample threads, `thread_id = hash(workflow_id, tag)`. For a grouped step, `thread_id = thread_id_of(one_of_its_upstream_tasks)`.
- Recruitment loop must respect thread ownership: when a worker is owning a thread, the recruiter should not retire it until all tasks in the thread are done.

### 2. Worker / client I/O skip

- When the client receives a task whose previous task in the thread ran on the same worker:
  - Skip the workspace download for inputs that map to the previous task's `/output`. Instead, point the new task's `/input` at the previous task's `/output` directory (read-only mount or hardlink-tree).
  - The producer task's `/output` upload must be deferred or skipped if the consumer is also local. Simpler: keep the upload (it provides crash recovery and is async with the consumer's start), but allow the consumer to short-circuit the download.
- Garbage collection: previously-task-run `/output` directories on a worker need to be cleaned up after the last consumer task in their thread completes, to bound disk usage.

### 3. Compile-time thread assignment

- In `Workflow.compile()` (or where tasks are first persisted), when `run_strategy=T` is set:
  - For each tag (e.g. each iterated sample), assign all per-sample tasks the same `thread_id`.
  - For each grouped step, assign it the `thread_id` of the upstream task on the chosen co-location strategy (last sample, designated sample, or all-on-one with same `thread_id` for everything).

### 4. Failure modes / recovery

- **Owner worker dies mid-thread.** Today, scitq retries failed tasks; in thread mode, all incomplete tasks of the dead thread must be re-assigned to a new worker, and outputs already in workspace can be re-downloaded. The sticky optimisation degrades gracefully into the regular round-trip path.
- **Thread fan-in across multiple owners.** If we go with "pin all per-sample threads to the same worker" semantics, the worker is a single point of failure. The "pin grouped to one upstream's worker" variant has only the simka worker as a SPOF, which is symmetric with today's `max_recruited: 1` simka step.

## Today's workaround

Until the above ships, a `grouped: true` step that needs to fan in from per-sample iterator outputs uses a no-op `stage` step that does `mv /input/*.fastq.gz /output/`. This satisfies the resolver and gives the grouped step an upstream Step output to consume. The cost is one workspace upload + one workspace download cycle for the staged data — on Azure intra-region this is free bandwidth but observable wall-time (~10 min for 22 GB at 11 samples).

The companion fix — `grouped` step accepting `inputs: <itervar>.<group>` directly so the `stage` step is no longer required — is implemented in `python/src/scitq2/yaml_runner.py:_resolve_inputs`. That removes the need for the `stage` step *under the workspace round-trip semantics*. It does not save the round-trip itself; that requires the sticky scheduler.

## Suggested first milestone

The narrowest useful slice that gives observable benefit:

1. Compile-time `thread_id` on tasks, populated only when `run_strategy=T`.
2. Server task-assignment respects thread ownership (don't dispatch a task to a different worker than the thread owner).
3. Worker-side input short-circuit: if input URI is `azswed://.../<some_workspace_path>` and the previous task in the same thread produced that exact path on this worker, mount the local `/output` of that task as the new task's `/input` and skip the download.
4. Smoke test: a 2-step thread workflow (echo "hi" > /output/x → cat /input/x) on a single worker that never touches the workspace.

Once that round-trips a single thread cleanly, generalise to grouped fan-ins by pinning grouped tasks to one of their upstream threads' owner.
