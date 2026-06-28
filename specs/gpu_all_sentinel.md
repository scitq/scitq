# `task_spec.gpu: "all"` — exclusive GPU access

## Problem

The GPU plumbing (migrations 40–42) gives each task `min_gpu` devices via
`CUDA_VISIBLE_DEVICES` partitioning. The allocator hands out exactly
`min_gpu` indices per task, the rest stay invisible. This is correct
for the common case (one task per GPU on a multi-GPU host) but leaves
a gap:

> "Run one task per worker and let it see every GPU on the host."

Workloads that need this: multi-GPU training (PyTorch DDP, DeepSpeed),
in-process model sharding, any code that calls `torch.cuda.device_count()`
to size its work.

Today the only way to express this is `task_spec.gpu: 4` — which
requires the workflow author to know `flavor.gpu_count` at edit time
and re-edit the workflow when targeting a different SKU. CPU has no
equivalent gap because Docker doesn't cpuset-pin by default: a single
task on the host already sees every core. GPUs differ because the
allocator actively partitions.

## Solution

Accept the string sentinel `"all"` wherever `task_spec.gpu` accepts an
integer. The sentinel propagates through the stack as a distinct
signal — never silently collapsed to an integer at the workflow
boundary — and is resolved to `flavor.gpu_count` exactly once: at task
assignment, when the worker (and therefore the flavor) is known.

### Semantics

`task_spec.gpu: "all"` means:

1. **Recruiter sizing:** `gpu_per_task` is left null in the recruiter
   row; `concurrency` is forced to 1 for the GPU-bound step. (The
   workflow author still uses `worker_pool` filters to choose a
   GPU-bearing flavor.)
2. **Fit predicate:** the task fits any worker whose
   `flavor.gpu_count >= 1`. There is no floor beyond "must have a
   GPU" — the sentinel commits to whatever the chosen worker has.
3. **Assignment:** when `assigntask.go` matches the task to a worker,
   it materializes the task's `min_gpu` to `worker.flavor.gpu_count`
   on the row. From that point the task looks identical to one that
   was originally submitted with that integer.
4. **Allocation:** the client's `GPUAllocator` partitions devices
   exactly as today — but now `min_gpu == flavor.gpu_count`, so the
   single in-flight task gets every index. `CUDA_VISIBLE_DEVICES`
   ends up listing every device on the host.
5. **Concurrency:** with `concurrency=1` and `min_gpu=gpu_count`,
   only one task can run at a time per worker. This is the intended
   exclusive-access guarantee.

### Why resolve at assignment, not at recruitment

Resolving at assignment keeps the workflow portable across flavors —
a 4-GPU host and an 8-GPU host both work without re-editing. It also
keeps the rule local to one site (the assignment SQL) instead of
threading the sentinel through every consumer of `task_spec.gpu`.

The recruiter still needs to know "this step wants exclusive GPU
access" so it sizes `concurrency=1`, but it doesn't need the
resolved integer — only the assignment loop does.

## Wire format

### Python (DSL)

```python
TaskSpec(gpu="all", ...)  # new
TaskSpec(gpu=1,     ...)  # existing — per-task partition
TaskSpec(gpu=True,  ...)  # existing — sugar for gpu=1
```

`_parse_gpu()` adds a branch for `"all"` (case-insensitive) and stores
`self.gpu = "all"` (string). Every consumer of `task_spec.gpu` must
handle both `int` and `str` paths — see "Call sites" below.

### YAML

```yaml
task_spec:
  gpu: "all"
```

No runner-level change required beyond the `_parse_gpu` update: the
YAML runner already passes `task_spec` keys through to `TaskSpec(...)`
verbatim.

### Proto

`TaskRequest.min_gpu` and `Task.min_gpu` stay `int32`. The sentinel is
**not** encoded on the wire as a special value. Two paths flow
through the proto:

- **Submission:** when the workflow compiler sees `task_spec.gpu == "all"`,
  it sets `TaskRequest.min_gpu = null` AND sets a new
  `TaskRequest.gpu_all = true` proto bool. The server stores
  `task.min_gpu = NULL` and `task.gpu_all = TRUE` in the row.
- **Assignment:** the assignment SQL detects `task.gpu_all = TRUE` and
  populates `min_gpu = worker.flavor.gpu_count` atomically as part of
  the assignment UPDATE.

Why not encode "all" as a magic integer (e.g. `-1`)? Because every
downstream consumer that compares `min_gpu` to `flavor.gpu_count`
would need to know about the sentinel. Carrying intent in a separate
bool keeps each call site honest: integer comparisons work, and
the one place that resolves the sentinel (the assignment SQL) is the
only place that reads the bool.

### Schema

Migration 43 (`task_gpu_all.up.sql`):

```sql
ALTER TABLE task ADD COLUMN gpu_all BOOLEAN NOT NULL DEFAULT FALSE;
```

NOT NULL with `DEFAULT FALSE` because every existing task is
non-sentinel by definition; no backfill needed.

## Call sites

The following sites today read `task_spec.gpu` or `task.min_gpu`. Each
needs a one-line audit:

| Site | File | Action |
|------|------|--------|
| TaskSpec parser | `python/src/scitq2/workflow.py:_parse_gpu` | Accept `"all"`, store as string |
| Recruiter options builder | `python/src/scitq2/recruit.py:build_recruiter` | If `task_spec.gpu == "all"`, set `concurrency=1` instead of `gpu_per_task` |
| Workflow compile | `python/src/scitq2/workflow.py` (around line 466) | If `task_spec.gpu == "all"`, set `request.gpu_all = True`, leave `min_gpu` unset |
| SubmitTask SQL | `server/server.go` (around line 639) | Project `req.GpuAll` into `task.gpu_all` |
| Task fit predicate | `server/assigntask.go` (gpu_count comparison) | When `gpu_all=TRUE`, treat fit as `worker.gpu_count >= 1` (any GPU) |
| Assignment UPDATE | `server/assigntask.go` | When `gpu_all=TRUE`, materialize `min_gpu = flavor.gpu_count` in the assignment SQL |
| Client (executeTask) | `client/client.go` | No change — reads `task.min_gpu` post-resolution |
| YAML runner WORKER_POOL_KEYS | `python/src/scitq2/yaml_runner.py:1412` | No change — `worker_pool.gpu` is independent |

## What "all" does NOT mean

- It does NOT mean "any number of GPUs is fine." `task_spec.gpu: 0`
  (or omitting `gpu`) is still the way to say "GPU optional."
- It does NOT change the concurrency formula for any other step.
  Only the step that declares `gpu: "all"` gets `concurrency=1`.
- It does NOT bypass the fit predicate — a CPU-only flavor still
  fails to match the step's tasks.
- It does NOT cooperate with `concurrency: N > 1`. The combination is
  semantically incoherent (`concurrency=2` + "all GPUs each" on a
  4-GPU host means each task grabs 4 → 8 GPU-slots from a 4-GPU host).
  The TaskSpec constructor must reject the combination at parse time.

## Test plan

Unit tests:
- `TaskSpec(gpu="all")` parses; `TaskSpec(gpu="ALL")` parses;
  `TaskSpec(gpu="all", concurrency=2)` raises.
- `build_recruiter(TaskSpec(gpu="all"))` returns `concurrency=1`,
  no `gpu_per_task`.

Integration test (recruitment_gpu_test.go-style):
- A step with `gpu: "all"` against a 4-GPU flavor → assignment
  produces a task with `min_gpu=4`; allocator gives indices 0,1,2,3;
  `CUDA_VISIBLE_DEVICES=0,1,2,3`.
- A step with `gpu: "all"` against a 1-GPU flavor → same path,
  `min_gpu=1`, `CUDA_VISIBLE_DEVICES=0`.

End-to-end (alpha2 smoke, post-merge):
- Adapt `gpu_smoke.yaml` to use `gpu: "all"` on a `Standard_NC24ads_A100_v4`
  (or any multi-GPU NC family), assert the container sees every device.

## Open questions

1. **DSL parity for "every CPU"?** No. As established above, CPU has no
   gap — `concurrency=1` already gives the lone task all cores.
2. **Negative integer sentinel instead of string?** Considered and
   rejected — see "Wire format" above. The proto bool is cheaper and
   keeps integer comparisons honest at every other call site.
3. **Should "all" survive a retry that lands on a different flavor?**
   Yes — the sentinel re-resolves at each assignment. That's why
   `task.gpu_all` is the stored truth, not `min_gpu`.
