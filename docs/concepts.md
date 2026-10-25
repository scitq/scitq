# Concepts

How scitq moves work along: the life of a task, what happens when it fails,
when a workflow is finished, and where workers come from. The pages under
Usage say how to write and run workflows; this one says what scitq does with
them and what its statuses mean.

## Tasks, steps, workflows, workers

- A **task** is one command, run in a Docker container (or directly on the
  worker, for a `bare` task). It declares its inputs, which are downloaded
  before it runs; its resources, shared read-only data; and its output
  destination, where whatever it writes to `/output` is uploaded.
- A **step** groups tasks that share a container and resource needs. Workers
  are attached to steps, not to tasks.
- A **workflow** groups steps. Tasks of a step can depend on tasks of other
  steps; scitq runs a task once its prerequisites are done.
- A **worker** is a machine running the scitq client: a cloud instance
  deployed by scitq, or a machine you installed it on yourself. It runs up
  to `concurrency` tasks at once, and downloads up to `prefetch` more ahead.

## The life of a task

A task moves through these statuses, in this order:

| Status | Meaning | Moved there by |
|---|---|---|
| **W** waiting | Some prerequisite is not done yet. | submission |
| **P** pending | Ready to run, waiting for a worker. | submission, or its last prerequisite finishing |
| **A** assigned | Given to a worker. | the server |
| **C** accepted | The worker took it. | the worker |
| **D** downloading | Inputs and resources are being fetched. | the worker |
| **O** on hold | Downloaded ahead (prefetch), waiting for a free slot. | the worker |
| **R** running | The command runs. | the worker |
| **U** uploading | The command succeeded; outputs are being uploaded. | the worker |
| **V** uploading | The command failed; outputs are being uploaded anyway. | the worker |
| **S** succeeded | Done. | the worker |
| **F** failed | Done, unsuccessfully. | the worker or the server |

Only tasks of a **running** workflow (status R) are assigned to workers.

**What S guarantees.** A task is S when its command exited 0 and every file
it left in `/output` reached its destination with the right size. The
numbers of files produced and uploaded are recorded with the task
(`scitq task list --long`, the UI, MCP `list_tasks`). An upload that falls
short makes the task F, with failure class `network`, like any other
failure that a retry can fix.

**What F tells you.** Each failure carries a **failure class**:

| Class | Cause |
|---|---|
| `oom` | The kernel killed the task for exceeding memory (Docker's `OOMKilled`). |
| `timeout` | The download, run, or upload took longer than the task's timeout. |
| `network` | A transfer failed, or the uploaded outputs don't match what was produced. |
| `eviction` | The worker disappeared or was deleted under the task. |
| `definitive` | The step declared this failure permanent (see below). |
| `other` | Anything else: a non-zero exit, a crash, a kill from outside. |

A container killed with exit code 137 is `oom` only when Docker reports
`OOMKilled`. A Docker daemon restart or an operator's `docker kill` also exit
137, and those are `other`.

## Retries

A task has a retry budget, `retry`. When it fails with budget left, scitq
creates a **new task** — a new id, same command, budget minus one — and
hides the failed attempt. The failed attempt stays in the database, linked
to its retry (`previous_task_id`), and `scitq task list --show-hidden` shows
it.

- **Resource curves.** A step can give `cpu`, `mem` or `disk` a list, one
  value per attempt (`mem: [40, 80, 160]`). Each retry moves one step along
  the curve.
- **Evictions are free.** A task whose worker disappeared did not get a fair
  attempt: its retry consumes no budget and does not advance the curve.
- **Definitive failures are final.** A step can list exit codes
  (`definitive_exit_codes`) or a stderr pattern (`definitive_pattern`) that
  mean "retrying cannot help". Such a failure is not retried, and its
  budget is set to 0 at once, so steps waiting on it with
  `accept_failure` proceed immediately.
- **Manual retries.** `scitq task retry`, the UI, or MCP `retry_task` retry
  a task whatever its budget, as a fresh start: the new task gets the
  original retry budget back, and resource curves restart at their first
  value. `edit_and_retry_task` changes the command first. Retrying a task
  of a finished workflow sets the workflow back to running.

The YAML reference describes these settings:
[definitive failures](usage/yaml-templates.md#definitive-failures-skip-retry-when-the-error-cant-recover)
and [retry curves](usage/yaml-templates.md#per-attempt-resource-escalation-retry-curves).

## Dependencies

A task with prerequisites starts in W, and becomes P when every prerequisite
is S. A step declared with `accept_failure: true` also accepts a
prerequisite that failed for good — failed with no retry left. A
prerequisite that will still be retried keeps its dependents waiting.

To run a waiting task without its prerequisites (to get partial results),
force it: MCP `force_run_task` moves it from W to P.

## When a workflow is finished

A workflow is **running** (R) while it has work to do. You can also set it
**paused** (P) or **debug** (D); scitq assigns no task and recruits no
worker for a workflow that is not running.

A running workflow finishes on its own once each of its tasks is S, F, or W
that can never start:

- **S, succeeded,** when every task of every *leaf* step is S. A leaf step is
  one no other task depends on — typically the final collection step.
- **F, failed (stuck),** otherwise: some task failed, and the leaves did not
  all succeed.

So a workflow whose final step ran with `accept_failure` and succeeded is
S, even if some upstream samples failed: the final step accepted those
failures. If the final step itself fails, the workflow is F.

A workflow created with `--live` (for optimization loops that keep adding
tasks) never finishes on its own; set it to S yourself when done.

## Where workers come from

A step gets workers in two ways:

- **Recruiters** (declared by the template's `worker_pool`) deploy cloud
  workers for the step, or **recycle** idle workers from other steps, up to
  the step's limits.
- You attach workers by hand: `scitq worker update --step-id`, the UI, or
  MCP `update_worker`.

A recruiter aims at enough workers to run the step's pending tasks in
`rounds` rounds, never above the step's `max_workers`. Every rule is in
[Recruitment](reference/recruitment.md). A worker that has finished with its
workflow is free to be recycled by others.

Each of these decisions — a deploy, a recycle, a manual move — is recorded
with who made it, and `scitq audit list` shows them.
