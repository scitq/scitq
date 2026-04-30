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
