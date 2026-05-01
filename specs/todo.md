# TODO — Next items

## UI: Force run button for waiting tasks

Add a button in the task page UI to force a waiting (W) task to pending (P), bypassing dependency checks. Useful when some dependencies failed but you want partial results.

The server already supports this via `UpdateTaskStatus` (and the MCP `force_run_task` tool wraps it). The UI just needs the button — likely in `ui/src/components/TaskList.svelte`, gated to tasks with `status === 'W'`.

## Decouple workspace from worker_pool.provider

Currently `workspace` silently overrides `worker_pool.provider` in the recruiter — see `python/src/scitq2/workflow.py:compile()` around the `client.get_workspace_root(...)` call. This is a footgun: setting `worker_pool.provider: "local.local"` while keeping `workspace: "azure.primary:swedencentral"` produces an Azure recruiter, not a local one (observed on workflow 2209).

**Change** (in priority order):

1. When `worker_pool.provider` is explicitly set, it WINS — recruiter protofilter matches it regardless of workspace.
2. When `worker_pool.provider` is unset, fall back to `workspace`'s provider/region (today's behavior, with a deprecation note in docs).
3. When both are set and disagree, emit a compile-time warning explaining what was chosen and why (e.g. cross-region I/O implications).
4. Long-term inversion: make workspace default to `auto` and resolve from `worker_pool.provider`. Most users pick where compute runs first; storage follows. Today's default is the inverse.

Reason: the natural mental model is *worker location = where compute runs, workspace = where storage lives*. They are obviously related, but coupling them by silently rewriting the worker provider based on workspace makes common scenarios impossible — e.g. "compute on bioit2 (free local), storage on Azure (already-uploaded data)."

## Iterator-level pair-aware fastq filtering

The fastp 1.1.0 fix (`genomics/fastp.yaml`) detects R1/R2 at runtime via `_find_pairs` so single-end mode against a paired source uses only R1. That works but the worker still has to download both R1 and R2, then discard R2. For known-paired sources (URI listings, ENA's `library_layout`), the iterator can do better: only emit R1 in the file group, halving download bandwidth for 1x runs against paired data.

**Sketch**:

```yaml
iterate:
  name: sample
  uri:
    source: uri
    uri: "..."
    group_by: folder
    fastqs: "*.f*q.gz"          # current behaviour
    pairs:                       # NEW: pair-aware exposure
      r1: "*[._]1.f*q.gz"
      r2: "*[._]2.f*q.gz"
# Workflow then declares intent:
- import: genomics/fastp@1.1.0
  inputs: sample.r1              # only R1 is fetched
```

The iterator builds `sample.fastqs`, `sample.r1`, `sample.r2` (and possibly `sample.unpaired`) groups. Workflow author picks which group's URIs get fetched.

Layered with the worker-side fallback (already in `genomics/fastp@1.1.0`): if the workflow doesn't use the pair groups (e.g. SRA where layout varies per sample, or older templates that only know `sample.fastqs`), `_find_pairs` still does the right thing on the worker. Both layers co-exist.

Files: `python/src/scitq2/yaml_runner.py` (iterator parsing) and the URI/ENA/SRA source builders.
