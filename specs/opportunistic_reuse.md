# Opportunistic reuse

## Motivation

Many workflows are re-run on overlapping data batches. Today, scitq re-executes every task even when the exact same computation on the exact same data was already performed successfully in a prior workflow. This wastes compute, time, and money.

**Opportunistic reuse** lets scitq recognize that a task has already been done and reuse its output, **across workflows**. Unlike `skip_if_exists` (which checks a single output path), reuse is content-addressed: it matches on *what the task does* and *what it processes*, not on *where the output lives*.

## Design principles

1. **Opportunistic, not intrinsic.** Whether to reuse is not a property of the task. It's a decision by the user for *this particular run*. The same pipeline may be run with reuse enabled (production batch processing) or disabled (validation, reproducibility audit). A task is not "cacheable" or "not cacheable" — it's the *context* that decides.
2. **No data hashing.** Hashing large input files is prohibitively expensive. We rely on URI identity for external inputs and transitive trust for internal inputs.
3. **Resources are immutable.** A resource at a given URI does not change over time. Standard practice in production (versioned reference databases, indexed genomes).
4. **Cross-workflow.** A reuse hit in workflow B can skip execution and point to output from workflow A.

## Opt-in model

Reuse is controlled at **workflow level**, not step level:

```python
def my_pipeline(workflow):
    step_a = workflow.Step(name="preprocess", ...)
    step_b = workflow.Step(name="align", ...)
    step_c = workflow.Step(name="count", ...)

run(my_pipeline, opportunistic=True)
```

This says: "for this execution, try to reuse results from previous runs wherever possible."

### Untrusted steps

Optionally, the user can declare specific steps as **untrusted** for this run. An untrusted step always runs, and since it breaks the trust chain, its downstream steps cannot be reused either:

```python
run(my_pipeline, opportunistic=True, untrusted=["preprocess"])
```

This is useful when:
- A step's behavior changed (new container version, updated tool) but the command string hasn't.
- The user wants to re-validate one specific step while skipping the rest.
- A resource was updated in-place (violating the immutability assumption for that step only).

Since breaking trust at one step breaks the entire downstream chain, typically there's at most one untrusted step per run. Listing multiple is possible but means most of the workflow will re-execute.

### Why not per-step opt-in

A per-step `cache=True` flag implies the step itself is "reproducible" — an intrinsic property. But reproducibility is context-dependent:

- A step with random sampling is not reproducible... unless "close enough" is acceptable for your use case.
- A perfectly deterministic step may need to be re-run to *prove* it's deterministic.
- A step reading an external API is not reproducible... unless you know the API hasn't changed.

The workflow-level flag with an untrusted list captures all these scenarios without labeling steps as inherently cacheable or not.

## Concepts

### Task fingerprint

A task is defined by what it *does*:

| Field | Included in fingerprint |
|---|---|
| `command` | yes |
| `shell` | yes |
| `container` | yes (including tag/digest) |
| `container_options` | yes |
| `resource` | yes (sorted) |

The **task fingerprint** is a SHA-256 hash of these fields in canonical form. Two tasks with the same fingerprint perform the same computation.

The container field includes the full image reference with tag or digest (e.g. `hermes:2.1.3`, `hermes@sha256:abc...`). A tag change like `hermes:2.1.3` → `hermes:2.2.0` produces a different fingerprint — different tool version, different computation. A mutable tag like `hermes:latest` is included as-is: if the user runs twice with `hermes:latest` and the underlying image changed, the fingerprint is the same and the reuse hit is "wrong" — but this is the user's choice (and exactly the kind of situation `untrusted` is for).

Fields **excluded**: `input`, `output`, `publish`, `step_id`, `workflow_id`, `task_name`, `retry`, timeouts, `weight`, `skip_if_exists`, `dependency`. These are context-dependent.

### Input identity

Each input has an **identity** — a value representing the data behind it:

- **External input** (URI not in any workflow workspace): the URI itself. Same URI = same data.
- **Internal input** (output of a previous step): the **reuse key** of the producing task. If the producing task was itself reuse-eligible and its key is known, its output is identical to any other task with the same key.

### Reuse key

The **reuse key** ties together what a task does and what it processes:

```
reuse_key = SHA-256(task_fingerprint || sorted(input_identities))
```

Two tasks with the same reuse key produce the same output (under our assumptions).

### Trust chain

Internal inputs create a **transitive trust chain**:

```
External data ──→ Step A ──→ Step B ──→ Step C
                    │           │
                    └─ key_A    └─ key_B
                       (B's input identity)   (C's input identity)
```

- Step A's inputs are all external → identity = URIs → `key_A` computable.
- Step B's inputs come from Step A → identity = `key_A` → `key_B` computable.
- Step C's inputs come from Step B → identity = `key_B` → `key_C` computable.

If **any** step in the chain is untrusted or its reuse key is unknown, the chain breaks: downstream steps lose their input identity and cannot be reused.

### Reuse-eligible vs reuse-hit

A task is **reuse-eligible** when:
1. The workflow has `opportunistic=True`.
2. The task's step is not in the `untrusted` list.
3. All its inputs have a known identity (external URI or upstream reuse key).

A reuse-eligible task gets a **hit** if its reuse key exists in the store with a successful output. Otherwise it's a **miss** — it runs normally, and on success its output is stored for future reuse.

## Data model

### Reuse store

```sql
CREATE TABLE task_reuse (
    reuse_key    CHAR(64) PRIMARY KEY,   -- SHA-256 hex
    output_path  TEXT NOT NULL,           -- URI of reusable output (publish or output path)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    task_id      INT NOT NULL,           -- the task that produced this entry
    step_name    TEXT,                    -- for display/debugging
    workflow_id  INT                      -- for provenance
);

CREATE INDEX idx_task_reuse_created ON task_reuse (created_at);
```

### Reuse key on task

```sql
ALTER TABLE task ADD COLUMN reuse_key CHAR(64) NULL;
CREATE INDEX idx_task_reuse_key ON task (reuse_key) WHERE reuse_key IS NOT NULL;
```

When a task is reuse-eligible, its `reuse_key` is computed at submission time and stored. The index is critical for two reasons:
1. **Reuse lookup**: the server checks `task_reuse` (PK lookup, already fast) then updates the task.
2. **Input identity resolution**: downstream tasks need to look up the `reuse_key` of their producing tasks. While this is typically done DSL-side from in-memory objects during the same run, the index also enables server-side resolution and cross-workflow queries (e.g. "find all tasks with this reuse key").

### No workflow-level flag

Both `opportunistic` and `untrusted` are **run-dependent**, not workflow properties. They are template parameters passed at `run()` time and influence reuse key computation during task submission. They are not stored in the database — the `reuse_key` column on task is the only persistent trace of the decision.

## Workflow

### At task submission (DSL → server)

When `opportunistic=True`:

1. For each task, the DSL computes the **task fingerprint** from command/shell/container/resources.
2. If the task's step is in the `untrusted` list → submit without `reuse_key`. Done.
3. Resolve **input identities**:
   - For each input:
     - If literal URI (external): identity = URI string.
     - If step output reference: identity = `reuse_key` of the producing task (from the in-memory task object, submitted earlier in the DAG).
   - If any input identity is unknown (producing task has no `reuse_key`, because it was untrusted or its own upstream was untrusted): submit without `reuse_key`. **No error** — just no reuse for this task. The chain broke silently.
4. Compute `reuse_key = SHA-256(task_fingerprint || sorted(input_identities))`.
5. Submit task with `reuse_key` set.

### At task scheduling (server-side)

When a task with a `reuse_key` reaches the pending state and is about to be assigned:

1. Look up `reuse_key` in `task_reuse`.
2. **Hit**:
   - Update task: `status = 'S'`, `output = cached_output_path`.
   - Promote dependent tasks as usual.
   - Log: `"♻️ reuse hit for task {id}, reusing output from task {original_id}"`.
3. **Miss**: proceed with normal worker assignment.

### After task success

When a task with a `reuse_key` completes successfully (status S, not from a reuse hit):

1. Determine stable output path: `publish` if set, else `output`.
2. Store for future reuse:
   ```sql
   INSERT INTO task_reuse (reuse_key, output_path, task_id, step_name, workflow_id)
   VALUES ($1, $2, $3, $4, $5)
   ON CONFLICT (reuse_key) DO UPDATE SET
       output_path = EXCLUDED.output_path,
       task_id = EXCLUDED.task_id,
       step_name = EXCLUDED.step_name,
       workflow_id = EXCLUDED.workflow_id,
       created_at = now()
   ```
   Latest writer wins — the most recent output is the freshest and least likely to be stale (important for lazy verification: if an old entry's output is deleted, the next successful run overwrites it with a valid path).

### DSL integration

```python
from scitq2 import *

def biomscope(workflow):
    step_qc = workflow.Step(name="qc", ...)
    step_align = workflow.Step(name="align", inputs=step_qc.output(), ...)
    step_count = workflow.Step(name="count", inputs=step_align.output("counts"), ...)

# Normal run — everything executes
run(biomscope)

# Reuse run — skip tasks already done on identical inputs
run(biomscope, opportunistic=True)

# Reuse, but force QC to re-run (container uses :latest which was updated)
# Since QC is untrusted, align and count also re-run (chain broken)
run(biomscope, opportunistic=True, untrusted=["qc"])

# Reuse, but force count to re-run (e.g. validating counting logic)
# QC and align still reused; only count and downstream re-execute
run(biomscope, opportunistic=True, untrusted=["count"])
```

### YAML template integration

`opportunistic` and `untrusted` are workflow-level keys in the YAML template, alongside `workspace` and `worker_pool`. They can reference template params so the user decides at run time:

```yaml
name: biomscope
version: 2.0.0
description: Metagenomics pipeline

params:
  input_dir:
    type: string
    required: true
    help: Input FASTQ directory
  location:
    type: provider_region
    required: true
  opportunistic:
    type: boolean
    default: false
    help: Reuse results from previous identical runs
  untrusted:
    type: string
    default: ""
    help: Comma-separated step names to force re-execute

iterate:
  name: sample
  source: uri
  uri: "{params.input_dir}"
  group_by: folder
  filter: "*.f*q.gz"

worker_pool:
  provider: "{params.location}"
  cpu: ">= 8"
  mem: ">= 60"

workspace: "{params.location}"

opportunistic: "{params.opportunistic}"
untrusted: "{params.untrusted}"

steps:
  - name: qc
    command: "hermes --preprocess --in1 {R1} --in2 {R2}"
    container: "hermes:latest"

  - name: align
    inputs: qc
    command: "hermes --index ${{REF}}"
    container: "hermes:latest"
    resource: "/ref/hermes/igc2_v5.herm"

  - name: count
    inputs: align.counts
    command: "hermes --count"
    container: "hermes:latest"
```

Running the template:

```bash
# Normal run — everything executes
scitq template run biomscope input_dir=azure://data/batch42 location=azure.swedencentral

# With reuse — skip tasks already done on identical inputs
scitq template run biomscope input_dir=azure://data/batch42 location=azure.swedencentral opportunistic=true

# Reuse, but force QC to re-run (container uses :latest which was updated)
scitq template run biomscope input_dir=azure://data/batch42 location=azure.swedencentral opportunistic=true untrusted=qc
```

## Edge cases

### Mixed inputs (external + internal)

A task with both external inputs and internal step outputs is reuse-eligible as long as all internal inputs trace back to tasks with known reuse keys. The key incorporates both URI identities and upstream reuse keys.

### Grouped steps (fan-in)

A grouped step output resolves to multiple task outputs. The input identity for a grouped input is the sorted list of reuse keys from all producing tasks. If any producing task lacks a reuse key, the downstream task cannot be reused.

### Retry

A retried task gets the same `reuse_key` (same fingerprint + inputs). If the original failed, the retry runs normally. If another workflow produced the result in the meantime, the retry gets a hit.

### Task editing (edit-and-retry)

Editing a task's command changes its fingerprint → new reuse key → miss. Correct behavior.

### Output path rewriting on hit

On a reuse hit, the task's `output` field is set to the stored `output_path`. Downstream tasks resolve inputs from this path — no data copying needed. The reused output lives wherever it was originally produced.

### Workflow deletion

Deleting a workflow does **not** invalidate reuse entries. The stored output paths may become inaccessible if storage is cleaned up — pruning handles stale entries.

### Reuse hit + skip_if_exists

Both can be active. The reuse check runs first (it's a DB lookup, no I/O). If it hits, the task is marked S. If it misses, `skip_if_exists` can still skip it based on output path presence. They are complementary, not conflicting.

## Reuse management

### Invalidation

```
scitq reuse invalidate --key <sha256>
scitq reuse invalidate --step <name> [--workflow <name>]
scitq reuse clear
```

### Pruning

```sql
DELETE FROM task_reuse WHERE created_at < now() - $1::interval;
```

Configurable via `reuse_retention` in `scitq.yaml` (default: 90 days).

Stale entries (output path no longer accessible) can be detected and cleaned during pruning by checking output existence.

### Stats

Expose per-workflow counters:
- Tasks submitted with `reuse_key`
- Reuse hits
- Reuse misses
- Entries stored

## Implementation order

1. **Schema**: migration for `task_reuse` table, `reuse_key` on task.
2. **DSL**: `opportunistic` and `untrusted` parameters on `run()`. Fingerprint + reuse key computation in `Task.compile()`.
3. **Server-side check**: In `assignPendingTasks()`, check `task_reuse` before worker assignment for tasks with a `reuse_key`.
4. **Server-side store**: After task success, insert into `task_reuse`.
5. **YAML templates**: `opportunistic` and `untrusted` template parameters.
6. **CLI/MCP**: Reuse inspection and invalidation tools.
7. **Pruning**: Periodic cleanup job.

## Design decisions

1. **Lazy output verification.** Reuse hits are DB-only (instant). If the cached output turns out to be gone (deleted workspace, cleaned up storage), the downstream task will fail to download — at that point the stale entry is invalidated and the task is re-run. No upfront `fetch_list` per hit.
2. **DSL-side reuse key computation.** The DSL has the full task graph in memory and knows which inputs are external vs internal. The server stores and matches keys but does not compute them.
3. **Per-server scope.** Reuse is per-database (same server). Cross-server reuse would require a shared store — future work.
