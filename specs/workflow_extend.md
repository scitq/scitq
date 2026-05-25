# Extending an existing workflow (declarative reconcile)

## Motivation

Running a template always creates a *new* workflow. Sometimes you want to feed
more work into, or reconcile, an **existing** workflow instead:

- **(A)** Re-run the same template with more inputs and have the new samples
  land in the *same* steps of the existing workflow (shared workers/recruiters,
  one place to watch).
- **(B)** Attach a different template's steps onto an existing workflow bucket.
- **(C)** Re-run a template whose command generation changed (e.g. a fixed
  command) and have only the *drifted* tasks re-run — the manual
  `edit_step_command` dance, but driven declaratively by the template.

This is opt-in. The default behavior is unchanged: every run creates a new
workflow.

## Model: idempotent-by-identity at each level

Extending reconciles the template's *desired* state against what already
exists, keyed on identity at each level:

| Level    | Identity                     | Reconcile action |
|----------|------------------------------|------------------|
| Workflow | the explicit `extend_workflow_id` | reuse it (no `CreateWorkflow`) |
| Step     | `(workflow_id, step_name)`   | find-or-create (`step_name` is unique per workflow) |
| Task     | `(step_id, task_name)`       | find-or-reference, with command check (see below) |

`task_name` already encodes the tag (e.g. `qc_host.SAMEA14101335`), so "same
tag in this step" == "same `task_name`".

Crucially, scitq's dependency wiring is keyed on each task object's `task_id`,
resolved *after* the task is created during `compile`. It doesn't care whether
that id came from a fresh `submit_task` or was looked up on an existing task. So
"find-or-reference" composes with the dependency graph (including grouped /
fan-in steps that mix pre-existing and new tasks) for free — a referenced task's
existing id simply flows into downstream edges.

## Per-task reconcile (level C)

For each task the template wants to create in an extended workflow, look it up
by `(step_id, task_name)` among **non-hidden** tasks of the target step:

| Existing task                         | default (cascade reconcile)                          | `retry_failed_only` |
|---------------------------------------|------------------------------------------------------|---------------------|
| none (new tag)                        | create (`submit_task`)                               | create |
| same command, no changed prerequisite | reference existing `task_id`, skip submit            | reference, skip |
| command differs **OR** a prerequisite changed this pass | edit-and-retry (`EditAndRetryTask`); new clone's id flows downstream | edit-and-retry **only if** existing status is `F` |
| status `F` (failed)                   | edit-and-retry                                       | edit-and-retry |
| status `S`/`R`/`P`, command differs   | edit-and-retry (re-run / kill+retry)                 | **leave untouched** |

"Reference" means: set the in-memory `Task.task_id` to the existing id and do
not submit — downstream dependency edges then resolve to that existing task.

### Cascade (default) and why

If an upstream task is re-run because its command changed, its dependents'
*inputs* change too. A dependent whose own command is unchanged must therefore
also re-run, else it would reference output built from the stale upstream. So
the precise default rule is **"command differs OR any prerequisite was re-run
this pass."** This is cheap: `compile` already walks steps in dependency order
and each task knows whether it was reused vs (re)created, so a `changed` boolean
propagates along the edges within the existing loop.

### `retry_failed_only` opt-in and why

In cases like the `_para zcat` command fix, the tasks that *succeeded* under the
old command are genuinely healthy (the bug only affected multi-input samples
that failed); their dependents never ran for the failed ones. So a conservative
mode re-runs **only the existing tasks currently in `F`**, leaving `S`/`R`/`P`
alone, and needs **no cascade** (a failed task's dependents are blocked in `W`
and unblock naturally when the retry succeeds). It also doesn't need command
comparison — it just edit-and-retries failed tasks with whatever command the
template now generates. Strictly simpler than the default; it generalizes the
manual `edit_step_command`.

New inputs (tasks whose tag isn't present) are always created in every mode —
that's the "extend with more samples" purpose.

## Workspace consistency

Task output paths are `{workspace_root}/{workflow_full_name}/{task_full_name}/`.
`workspace_root` is per provider/region (not per workflow). For extend:

- The workflow's `full_name` is taken from the **existing** workflow (so outputs
  land alongside the existing tasks), not recomputed from the template name/tag.
- `workspace_root` is computed from the extending run's `provider`/`region` as
  usual. The run must therefore target the same provider/region the workflow's
  existing tasks used; otherwise new tasks publish to a different workspace than
  the rest. (Future: store provider/region on the workflow row and refuse on
  mismatch. For now this is the operator's responsibility, documented.)

Workflow-level fields (`run_strategy`, `maximum_workers`, `live`, status) are
**not** touched on extend — the existing workflow keeps its settings. The
recruiter auto-bump on recruiter-create still raises `maximum_workers` if a new
step needs more workers.

## Implementation layers

1. **DSL (`workflow.py`)** — the reconcile logic. `Workflow.compile` gains
   `extend_workflow_id` and `retry_failed_only`. When extending it skips
   `create_workflow`, adopts the id + existing name, prefetches the step-name →
   step_id map and the per-step `{task_name: (task_id, command, status)}` map,
   and threads an `ExtendContext` through `Step.compile` / `Task.compile`.
   `Step.compile` becomes find-or-create; `Task.compile` becomes
   find-or-reference/edit with the `changed`-flag cascade.
2. **Client (`grpc_client.py`)** — `list_steps`, `edit_and_retry_task` (existing
   RPCs `ListSteps` / `EditAndRetryTask`).
3. **Runner (`runner.py`)** — `--extend-workflow <id>` and `--retry-failed-only`
   flags, passed to `run()`.
4. **Server (`workflow_templates.go`) + proto + CLI + UI** — plumb the two
   options through `RunTemplateRequest` → `scriptRunner` args so the feature is
   reachable from `scitq template run` and the UI run modal. (Mechanical; see
   "Plumbing" below.)

### Plumbing (server/CLI/UI)

- `RunTemplateRequest`: add `optional int32 extend_workflow_id` and
  `optional bool retry_failed_only`.
- `scriptRunner`: when set, append `--extend-workflow <id>` /
  `--retry-failed-only` to the run/yaml_run arg list (next to `--no-recruiters`).
- CLI `scitq template run`: `--extend-workflow` / `--retry-failed-only` flags.
- UI run modal: an "Extend existing workflow" opt-in (workflow picker, reuse the
  searchable selector) + a "retry failed only" checkbox. Default off.

## Non-goals / caveats

- No cross-provider workspace reconciliation (documented operator responsibility).
- Editing a drifted `S`/`R` task in the default mode **re-runs / kills+retries**
  it — that is the intended reconcile semantics but means an extend can disturb
  finished/in-flight work; it is the opt-in's documented behavior.
- Reusing/referencing an existing task is by identity only; it never compares
  inputs/resources beyond the command. If a task's *inputs* changed but its
  command and tag did not, it is treated as converged. (Tags are expected to be
  derived from the inputs in practice, so this is rarely an issue.)
