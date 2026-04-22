# Thread-wise workflow execution (run_strategy: T)

## Motivation

The current execution model is **batch-wise** (run_strategy: B): all tasks of step 1 run first, then all tasks of step 2, etc. Workers are specialized per step via recruiter assignment. This is optimal when steps have different resource needs (e.g., GPU for step 2) because workers can be right-sized per step.

However, batch-wise execution is suboptimal when **logistics dominate**: when network transfers (download inputs, upload outputs) cost more time and money than the compute itself. In a 3-step pipeline (fastp → humanfilter → seqtk), batch-wise means:

1. Worker downloads sample FASTQ, runs fastp, uploads output
2. Worker downloads fastp output, runs humanfilter, uploads output
3. Worker downloads humanfilter output, runs seqtk, uploads output

Each step re-downloads what the previous step just produced. For a sample with 10GB of FASTQ data and 3 steps, that's 6 unnecessary transfers (3 uploads + 3 downloads of intermediate data).

**Thread-wise execution** processes each sample through all steps on the same worker, reusing local data:

1. Worker downloads sample FASTQ, runs fastp → output stays local
2. Same worker runs humanfilter on local fastp output → output stays local
3. Same worker runs seqtk on local humanfilter output → uploads final result

Transfers drop from 6 to 2 (1 download + 1 upload). For large-scale workflows with hundreds of samples, this saves hours and significant egress costs.

## Concept: sticky task assignment

When a worker completes a task for sample X at step N, the server **preferentially assigns** the next task for sample X at step N+1 to the same worker. Tasks become "sticky" — processing a sample creates an affinity between the worker and subsequent tasks for that sample.

This is a **soft preference**, not a hard constraint. If the preferred worker is busy or offline, the task can go to any available worker (falling back to normal assignment). The sticky assignment is an optimization, not a correctness requirement — the task still works on any worker, just with an extra download phase.

### Naming

The analogy is threading in computing: each sample's pipeline is a "thread" of execution flowing through the steps. Multiple threads run in parallel across workers, but each thread stays on one worker as much as possible.

| Strategy | Model | Optimization target |
|----------|-------|-------------------|
| B (Batch) | All tasks of step N, then step N+1 | Worker specialization, GPU steps |
| T (Thread) | Each sample flows through all steps | Data locality, minimize transfers |

## Design

### Workflow creation

```yaml
# YAML
run_strategy: T

# or DSL
workflow = Workflow(name="my_pipeline", run_strategy="T")

# or CLI
scitq workflow create --name my_pipeline --run-strategy T
```

### Task assignment changes

When `run_strategy = T`, the task assignment algorithm changes:

1. **Build affinity map**: for each pending task, find its prerequisites (via `task_dependencies`). If a prerequisite was executed by a specific worker, that worker has affinity for this task.

2. **Prefer affinity**: when selecting which worker gets a task, prefer workers with affinity. Among equally available workers, the one that ran the predecessor task wins.

3. **Fallback**: if the affinity worker has no capacity (all slots full), assign to any available worker as usual.

```sql
-- Find affinity: which worker ran the predecessor of this pending task?
SELECT DISTINCT t_prev.worker_id
FROM task_dependencies d
JOIN task t_prev ON d.prerequisite_task_id = t_prev.task_id
WHERE d.dependent_task_id = $1    -- the pending task
  AND t_prev.worker_id IS NOT NULL
```

### Local data reuse (Phase 1)

The key optimization: when a task runs on the same worker that produced its input, **skip the download phase** for inputs that are already local.

The worker's task directory structure:
```
/var/lib/scitq/tasks/{task_id}/input/   ← downloaded inputs
/var/lib/scitq/tasks/{task_id}/output/  ← task output
```

Today, after a task completes, the output is uploaded and the local directory is cleaned up. For thread-wise execution:

1. **Defer cleanup**: don't delete the output directory immediately. Keep it until the dependent task has started (or a timeout expires).
2. **Local input shortcut**: when the worker is assigned a task whose input URI matches a local output directory from a previous task, symlink or move the files instead of downloading.

The server already knows the predecessor task's output path. When assigning a task to the same worker, it can include a hint: "input X is available locally at task {prev_task_id}'s output directory."

Implementation approach:
- **Server side**: when assigning a task to a worker with affinity, include `local_input_hints` in the `TaskListAndOther` response: a map of `input_uri → local_task_id` for inputs available from predecessor tasks on this worker.
- **Client side**: before downloading an input, check if it's in `local_input_hints`. If so, symlink/move from the predecessor's output directory instead of downloading.

### Upload elimination (Phase 2 — future)

A more aggressive optimization: if the next task for a sample will run on the same worker, **skip the upload entirely** for intermediate steps. The output stays local and becomes the next task's input directly.

This requires:
- **Certainty of local assignment**: the server must guarantee (not just prefer) that the next task runs on the same worker. This is stronger than sticky assignment.
- **Failure recovery**: if the worker dies mid-pipeline, the intermediate data is lost. The entire sample's pipeline must be restarted from the last uploaded checkpoint.

Phase 2 is significantly more complex and risky. Phase 1 (sticky assignment + local reuse) captures most of the benefit with much less complexity.

## Interaction with existing features

### Worker recycling

In batch-wise mode, workers are assigned to steps and recycled between steps (`recyclable_scope`). In thread-wise mode, workers aren't step-bound — they process whatever task is next for their assigned samples. Worker recycling is implicit: the worker naturally moves between steps as its samples progress.

A thread-wise worker still needs to be assigned to a step for resource management (Docker image, resources). The assignment changes with each task. The recruiter deploys workers for the workflow, not for individual steps.

### Prefetch

Prefetch works differently in thread-wise mode. Instead of prefetching the "next task of the same step", the worker prefetches the "next task for the same sample" (which is typically at the next step). The prefetch priority should be:

1. Dependent tasks of completed tasks on this worker (thread continuation)
2. New tasks from the first step (new threads)

### task_spec and concurrency

Multiple sample threads can run concurrently on a large worker. With `task_spec: cpu: 8` on a 32-CPU worker, 4 samples can flow through the pipeline simultaneously. Each sample's tasks are sequential (step 1 → step 2 → step 3), but different samples overlap.

### Opportunistic reuse

Reuse works the same way. If a task at step 1 for sample X has a reuse hit, the server skips execution and immediately makes step 2 for sample X available. The sticky assignment still applies for step 2 → step 3.

### Quality scoring

No change — quality is per-task regardless of execution strategy.

## DSL and YAML examples

### YAML

```yaml
format: 2
name: quality_filter
run_strategy: T    # thread-wise execution

params:
  bioproject: { type: string, required: true }
  location: { type: provider_region, required: true }

iterate:
  name: sample
  source: ena
  identifier: "{params.bioproject}"

worker_pool:
  provider: "{params.location}"
  cpu: "== 32"
  mem: ">= 120"
  max_recruited: 10
  prefetch: 1

workspace: "{params.location}"

steps:
  - import: genomics/fastp
    inputs: sample.fastqs

  - import: metagenomics/bowtie2_host_removal
    inputs: fastp.fastqs

  - import: genomics/seqtk_sample
    inputs: humanfilter.fastqs
```

No per-step `worker_pool` needed — all steps share the same workers. The server handles sticky assignment automatically.

### DSL

```python
from scitq2 import *

def pipeline(workflow):
    for sample in samples:
        fastp = workflow.Step(name="fastp", ...)
        fastp.task(input=sample.fastqs, ...)

        humanfilter = workflow.Step(name="humanfilter", ...)
        humanfilter.task(input=fastp.output("fastqs"), ...)

        seqtk = workflow.Step(name="seqtk", ...)
        seqtk.task(input=humanfilter.output("fastqs"), ...)

run(pipeline, run_strategy="T")
```

## Implementation order

1. **run_strategy field**: add 'T' as a valid value for workflow.run_strategy. No behavior change yet — T behaves like B.

2. **Sticky assignment**: in `assignPendingTasks`, when the workflow has run_strategy='T', build the affinity map and prefer affinity workers. This is a server-only change — the client and DSL don't need to know.

3. **Deferred cleanup**: the client delays cleanup of task output directories for thread-wise workflows. The server signals this via a flag in the task assignment response.

4. **Local input hints**: the server includes local_input_hints in task assignment for affinity-matched tasks. The client checks hints before downloading.

5. **Prefetch priority**: adjust prefetch ordering for thread-wise mode (thread continuation over new threads).

Steps 1-2 provide the core benefit (fewer re-downloads via S3 cache hits). Steps 3-4 eliminate downloads entirely for affinity-matched tasks. Step 5 is a performance refinement.

## Open questions

1. **Mixed strategies**: can a workflow have some steps batch-wise and others thread-wise? Probably not in V1 — the strategy is workflow-level.

2. **Worker failure mid-thread**: if a worker dies while processing sample X at step 2, and step 1's output was only local (phase 2), the sample needs to restart from step 1. In phase 1, this is a non-issue since outputs are always uploaded.

3. **Step-specific resources**: if step 2 needs a different Docker image and large reference database, switching steps on the same worker incurs warm-up cost. Thread-wise is best when all steps use similar resources (or the resource download is cheaper than the data re-download).

4. **Recruiter behavior**: in batch-wise mode, recruiters are per-step. In thread-wise mode, workers serve all steps. The recruiter should deploy based on total workflow demand, not per-step demand. The `worker_pool` at workflow level (not step level) is the natural fit.
