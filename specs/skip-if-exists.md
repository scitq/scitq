# Skip-if-exists — DAG-style task elision

## Goal

Allow tasks to be skipped when their output already exists. This enables:

1. **Preparation tasks**: build a resource (e.g. a large database) only if it's missing
2. **Resumable workflows**: re-run a failed workflow and only execute what's missing
3. **Snakemake-style DAG**: the foundation for "only run what's needed"

## Design

### Principle

A task with `skip_if_exists=True` and a defined output path is checked before execution. If the output path already contains files, the task is immediately marked as succeeded (`S`) without being assigned to a worker.

The check is a single `fetch_list` call on the task's output path — the same infrastructure already used by rclone integration.

### What doesn't change

- The dependency system: downstream tasks see a skipped task as `S` and proceed normally
- The DAG: it's already encoded in task dependencies, no solver needed
- The worker: skipped tasks never reach a worker

## Changes

### 1. Proto (`proto/taskqueue.proto`)

Add `skip_if_exists` to the Task message and TaskRequest:

```protobuf
message Task {
    // ... existing fields ...
    bool skip_if_exists = N;  // next available field number
}

message TaskRequest {
    // ... existing fields ...
    bool skip_if_exists = N;
}
```

### 2. Database (`server/migrations/`)

New migration adding the column:

```sql
ALTER TABLE task ADD COLUMN skip_if_exists BOOLEAN NOT NULL DEFAULT FALSE;
```

### 3. Server — task assignment (`server/server.go`)

In the assignment loop (where tasks transition from P → A), add a check before assignment:

```go
// Before assigning a task with skip_if_exists:
if task.SkipIfExists && task.Output != "" {
    files, err := fetchList(task.Output)
    if err == nil && len(files) > 0 {
        // Output exists — skip to S
        updateTaskStatus(task.TaskId, "S")
        continue
    }
}
```

This runs server-side, so no worker is needed. The `fetchList` call uses the existing rclone infrastructure.

The check should happen:
- After a task becomes `P` (pending) — either at creation time or when dependencies are satisfied
- Before assignment to a worker

Best location: in the `assignTasks()` function, after selecting pending tasks and before assigning them to workers.

### 4. Server — dependency promotion

When a waiting task's dependencies all succeed, it transitions W → P. At this point, if the task has `skip_if_exists=True`, the check should run immediately rather than waiting for assignment.

In `recomputeDependencies` (or wherever W → P happens), add the skip check right after the status change.

### 5. Python DSL (`python/src/scitq2/workflow.py`)

Add `skip_if_exists` parameter to `Workflow` (as default) and `Step`:

```python
class Workflow:
    def __init__(self, ..., skip_if_exists: bool = False):
        self.skip_if_exists = skip_if_exists

    def Step(self, ..., skip_if_exists: Optional[bool] = None):
        # Falls back to workflow default
        effective = skip_if_exists if skip_if_exists is not None else self.skip_if_exists
```

Pass through to `Task` and then to `client.submit_task()`.

### 6. Python gRPC client (`python/src/scitq2/grpc_client.py`)

Add `skip_if_exists` parameter to `submit_task()`.

### 7. CLI display

In `task list`, show a marker for skipped tasks (e.g. status `S` with a note "skipped").
The task duration would be 0 for skipped tasks, which is already a natural indicator.

## DSL usage

### Preparation task (immediate use case)

```python
kraken_db = workflow.Step(
    name="prepare_kraken2_gtdb",
    command=fr"""
    kraken2-build --download-library bacteria --db /output/gtdb_r226
    tar czf /output/gtdb_r226.tgz /output/gtdb_r226/
    """,
    container="staphb/kraken2:2.1.3",
    outputs=Outputs(db="gtdb_r226.tgz", publish="azure://ref/kraken2/gtdb_r226/"),
    skip_if_exists=True,
    task_spec=TaskSpec(cpu=16, mem=120),
)

# Downstream: uses the prepared resource
for sample in samples:
    classify = workflow.Step(
        name="classify",
        tag=sample.sample_accession,
        command=fr"kraken2 --db /resource/gtdb_r226 ...",
        resources=[kraken_db.output("db")],
        inputs=sample.fastqs,
        ...
    )
```

First run: the preparation task runs, builds the database, publishes it.
Second run: the output exists → task skipped → downstream proceeds immediately.

### Resumable workflow

```python
workflow = Workflow(
    name="biomscope",
    ...,
    skip_if_exists=True,  # all steps check outputs
)
```

If the workflow crashes at step 3 of 5, re-running it skips steps 1-2 (outputs exist) and resumes from step 3.

### Snakemake-style conversion

When converting from Snakemake, the converter can set `skip_if_exists=True` at workflow level, matching Snakemake's default "only run if output missing" behavior.

## Edge cases

### Race condition: output partially written
If a previous run crashed mid-upload, the output folder may have partial content. The check sees files and skips. This is the same behavior as Snakemake (`--rerun-incomplete` is opt-in there too).

Mitigation: the check could look for a sentinel file (e.g. `.done`) written at the end of upload, but this adds complexity. Start simple (any files = exists), add sentinel later if needed.

### Empty output
Some tasks produce no output files (side-effect only, e.g. sending a notification). These tasks would never be skipped since the output check finds nothing. This is correct behavior.

### Published vs workspace output
The check should use the **published** path if available (permanent storage), falling back to the workspace path. Published outputs survive workflow deletion; workspace outputs may not.

## Implementation order

1. Add DB column + proto field (migration + proto regen)
2. Server-side skip check in assignment loop
3. DSL parameter passthrough
4. Test with an integration test
5. Document in DSL docs
