# Chaining workflows (template-to-template sequencing)

## Motivation

A scitq workflow enumerates its iteration space **once, at submission time**:
the `iterate:` block lists URIs / ENA accessions / a fixed list, the task DAG
is materialised up front, and from then on the topology is fixed. This is a
deliberate design — it makes the workflow inspectable, restartable, and
extendable — but it leaves one shape of pipeline awkward:

> Step A produces an unknown number of outputs; step B must fan out over those
> outputs (one task per output).

The canonical example is the post-megahit / GPU-binning pipeline: a neural
binner emits N MAGs, where N is data-dependent and only known after the binner
runs; downstream QC (CheckM2, GTDB-Tk, dRep) wants to fan out over those bins.
There is no way today to declare an iterator whose source is the *output of a
prior step*.

Chaining solves this **without adding dynamic task materialisation to the
workflow engine**. The trick: between two workflows, the moment the child is
submitted, the parent's outputs already exist on storage. So the child's
iterator does its normal compile-time enumeration over those URIs — the
"data-dependent fan-out" problem is reduced to an ordinary directory listing
performed *after* the parent finishes.

The only new machinery is a workflow-completion trigger that submits the next
template with parameters mapped from the parent's state.

## Model

A template declares one or more chained children at the top level:

```yaml
format: 2
name: binning
version: 1.0.0
description: Cross-sample contig catalog + GPU binning

params: { ... }
iterate: { ... }
worker_pool: { ... }
steps:
  - import: assembly/megahit
  - import: binning/semibin2
    publish: "{RESOURCE_ROOT}/bins/{params.project}/"

chain:
  - template: bin_qc                              # child template name (optionally @version)
    params:
      bins_dir: "{parent.publish.semibin2}"       # ← resolved at parent-completion time
      project:  "{parent.params.project}"
      location: "{parent.params.location}"
```

The child template is otherwise an ordinary scitq YAML template. The chain
block only governs **when** it is submitted and **what** params it receives.

## Trigger semantics

A chain entry has **two orthogonal filters**, both of which must hold for the
child to fire. They mirror the same vocabulary used everywhere else in the
schema:

- **`when:` — truthy condition (default `true`).** Same semantics as
  step-level `when:`: skip the entry if the value is falsy. Used to make a
  chain entry depend on a parameter, a workflow var, or any other
  interpolated value.

  ```yaml
  chain:
    - template: bin_qc
      when: "{params.run_qc}"          # only fire if user opted in
  ```

- **`on:` — parent-status filter (default `succeeded`).** A closed enum
  controlling which terminal state of the parent triggers evaluation:
  - `succeeded` (default) — fire only when the parent transitions to `S`.
  - `always` — fire on `S` or `F` (cleanup / archival chains).
  - `failed` — fire only when the parent ends `F` (failure-handler chains).

  ```yaml
  chain:
    - template: notify_failure
      on: failed
  ```

Other rules:

- **Granularity: full parent-workflow completion.** A child is considered for
  firing only when the parent transitions to a terminal status (`S`/`F`).
  Step-level triggers may be added later if a real use case requires it; the
  spec does not preclude this, but v1 ships with workflow-level only.
- **Pause / debug / suspended parents**: a child is never evaluated while the
  parent is `R`/`P`/`D`/`Z`. Only terminal transitions trigger evaluation.
- **Chain entries are independent.** `chain:` is a list; each entry is
  evaluated and submitted on its own. A `when:` that resolves to false skips
  that entry silently; a failed child-submission does not block the others.

## Param mapping surface

The child sees a narrow, fully-resolved view of the parent — strictly the
subset already materialised in the parent's database state at completion time.
No new resolver is introduced.

### v1 (shipped)

| Reference                          | Resolves to                                                            |
|------------------------------------|------------------------------------------------------------------------|
| `{parent.params.<name>}`           | The parent's submitted `param_values[<name>]` (post-defaulting).        |
| `{parent.workflow_id}`             | `workflow.workflow_id` (int).                                           |
| `{parent.workflow_name}`           | `workflow.workflow_name`.                                              |
| `{parent.run_by}`                  | The parent's `run_by` user id (int).                                    |
| `{parent.run_by_username}`         | The parent's `run_by` username string.                                  |

### Planned (v2 — needs schema additions)

| Reference                          | Status                                                                  |
|------------------------------------|-------------------------------------------------------------------------|
| `{parent.publish.<step_name>}`     | Needs a `step.publish` column (or task-derivation logic).               |
| `{parent.workspace_root}`          | Needs the workflow row to record its `provider:region`.                 |
| `{parent.tag}`                     | Needs a `workflow.tag` column.                                          |

Using any planned reference in v1 returns a clear error pointing at the
workaround: pass the URI explicitly via parent.params. The runner makes this
easy because the parent's published paths can be derived from its own
params at template-write time:

```yaml
# Parent template:
params:
  project: {type: string}
  location: {type: provider_region}
publish_root: "azure://results/{params.project}"
steps:
  - import: binning/semibin2
    publish: true                                # → azure://results/{project}/binning/semibin2/

chain:
  - template: bin_qc
    params:
      # v1: derive the child's URI from the parent's own params + the known
      # publish-path convention. Once {parent.publish.semibin2} ships, this
      # collapses to a single reference.
      bins_dir: "azure://results/{parent.params.project}/binning/semibin2/"
      project:  "{parent.params.project}"
      location: "{parent.params.location}"
```

Explicitly **not** exposed (to keep the interface narrow):

- Parent `vars:` values. (If you need them downstream, surface them as params.)
- Per-task outputs or per-task `publish` paths.
- Parent step internals (recruiters, task_spec, etc.).

The mapping uses the existing `{...}` interpolation engine, so filters
(`|name`, `|dir`, `|format=...`, etc.) compose normally:

```yaml
chain:
  - template: bin_qc
    params:
      bins_dir: "{parent.publish.semibin2}"
      project:  "{parent.params.project|lower}"
```

## Rerun / idempotency

A re-run of the parent (`--continue` or `--extend`) that reaches a terminal
status again re-evaluates its chain entries. To avoid scattering
half-children:

- **Each `fired` entry re-fires its child as an implicit `--continue`**,
  scoped to the parent's `run_by` and the mapped param values. The existing
  child workflow is reconciled (same `continue_last` rules as the manual
  feature). The entry's status stays `fired`; a `last_fired_at` timestamp is
  bumped.
- **Each `pending` entry fires normally** (same as the first parent
  completion).
- **`suspended` / `cancelled` / `skipped` / `failed` entries are not
  re-evaluated** — they are terminal or paused by user intent.
- **Opt-out**: `always_new: true` on the entry forces a new child each time
  instead of reconciling. Useful when each parent run should produce a
  distinct child (e.g. dated archival workflows).

This composes cleanly with the existing extend/continue surface — chaining is
the workflow-to-workflow rung; extend is the within-one-workflow rung;
continue is the resolve-by-template+params rung.

## Failure semantics

- Default (`on: succeeded`): a child fires only when the parent reaches `S`.
  A parent that ends `F` does not fire any chain entry with default `on`.
- `on: always`: the child fires regardless of parent terminal status
  (`S` or `F`). The child receives parent state as usual; downstream cleanup
  templates use this.
- `on: failed`: the child fires only when the parent ends `F`. Useful for
  notification / triage chains.
- A failure **inside the chain firing itself** (template lookup miss, param
  resolution error, malformed mapping) marks that chain entry's status as
  `failed` with `error_message` populated. It does not change the parent's
  terminal status. Other chain entries continue to fire independently.

## User / workspace / region inheritance

- The child runs as the parent's `run_by`. (No impersonation.)
- The child's workspace defaults to the parent's `workspace`; pass an explicit
  `params.location` in the mapping if you need to override.
- The child's provider/region follows the same rule. The standard cross-region
  output-publish constraint applies (the same constraint that already governs
  workflow extension).

## Multiple chained children

```yaml
chain:
  - template: bin_qc
    when: "{params.run_qc}"             # optional QC, gated on a param
    params: { ... }
  - template: bin_taxonomy
    params: { ... }
  - template: notify_done
    on: always                           # fires on parent S or F
    params: { project: "{parent.params.project}" }
  - template: triage_failure
    on: failed                           # only fires on parent F
    params: { workflow_id: "{parent.workflow_id}" }
```

Each entry fires independently on parent completion. Their submission order
follows their order in the list, but they are not blocked on each other; they
race to the scheduler.

## Chain entries as first-class objects

Chain entries are **rows in a dedicated table**, not opaque JSON buried on the
parent workflow. This makes them enumerable, individually addressable,
mutable, suspendable, and cancellable from the CLI before they fire.

### `workflow_chain_entry` table

One row per planned chain entry, created when the parent workflow is
submitted. The row snapshots the parsed `chain:` block — template edits or
deletions after submission do not change armed entries.

| Column                       | Purpose                                                                       |
|------------------------------|-------------------------------------------------------------------------------|
| `chain_entry_id` (PK)        | identity                                                                      |
| `parent_workflow_id` (FK)    | which parent the entry is armed against                                       |
| `idx`                        | position in the parent's `chain:` list (stable ordering for display)          |
| `template_name`              | snapshotted target template name                                              |
| `template_version`           | optional pinned version (nullable; resolves to latest at fire time if null)   |
| `params_template` (JSONB)    | unresolved `params:` mapping; interpolation deferred to fire time             |
| `when_expr`                  | the truthy condition expression (default `"true"`)                            |
| `on_status`                  | enum: `succeeded` / `always` / `failed`                                       |
| `always_new`                 | bool; if true, fire a fresh child instead of reconciling                      |
| `status`                     | `pending` / `suspended` / `skipped` / `fired` / `failed` / `cancelled`        |
| `child_workflow_id` (FK)     | populated when `status=fired` (or last child if re-fired on parent extend)    |
| `error_message`              | populated when `status=failed`                                                |
| `created_at`                 | row creation time (parent-submit time)                                        |
| `fired_at` / `last_fired_at` | first / most recent firing timestamps                                         |

The backward lineage link on `workflow.parent_workflow_id` is kept as a cheap
parent-of-child pointer, populated when an entry fires (FK to `workflow`).
The forward view ("what's queued behind this workflow") becomes a query
against `workflow_chain_entry`.

### Lifecycle and state machine

```
                  parent submit
                       │
                       ▼
                  ┌─────────┐    user            ┌───────────┐
                  │ pending │──── suspend ─────▶ │ suspended │
                  │         │◀── resume ──────── │           │
                  └─────────┘                    └───────────┘
                       │                              │
                       │ parent terminal              │ user cancel
                       ▼                              │
        ┌──────┬───────┴───────┬──────┐               ▼
        ▼      ▼               ▼      ▼          ┌───────────┐
   ┌─────────┐ ┌───────┐ ┌───────┐ ┌─────────┐   │ cancelled │
   │ skipped │ │ fired │ │ failed│ │cancelled│   └───────────┘
   └─────────┘ └───────┘ └───────┘ └─────────┘
                  │
                  │ parent re-completes via --extend
                  └────── implicit --continue ───────▶ stays `fired`,
                                                       bumps last_fired_at
```

States:

- **`pending`** — armed. Will be evaluated when the parent reaches a
  terminal status.
- **`suspended`** — paused by the user. Does not fire when the parent
  terminates. The user may edit the entry's mutable fields before resuming.
  See "Suspend / resume" below.
- **`skipped`** — parent reached terminal status but `when:` was falsy or
  `on:` didn't match. Permanent.
- **`fired`** — child `template_run` submitted; `child_workflow_id` recorded.
  Re-fired on parent re-extend (implicit `--continue` against the same
  child); the row's status stays `fired` and `last_fired_at` is bumped.
- **`failed`** — chain firing itself failed (template not found, param
  resolution error, malformed mapping). `error_message` records why. Other
  entries continue to fire.
- **`cancelled`** — explicitly cancelled by the user before the parent
  reached terminal status, or from `suspended`. Terminal.

### Suspend / resume

The intended use case: the parent is running, the operator realises the
chained child template needs a fix before it should fire. They suspend the
chain entry, edit the child template (or the entry's params), then resume.

```sh
scitq workflow chain suspend --id M       # pending → suspended
# ... edit the child template, or:
scitq workflow chain edit --id M --param bins_dir="azure://override/path"
scitq workflow chain resume --id M        # suspended → pending (or fires immediately, see below)
```

Semantics:

- Suspend is only valid from `pending`. Calling suspend on a terminal state
  is an error.
- While `suspended`, the user may edit **mutable fields** (`template_name`,
  `template_version`, `params_template`, `when_expr`, `on_status`,
  `always_new`). Identity, lifecycle, and `child_workflow_id` columns are
  not editable.
- Resume from `suspended`:
  - If the parent has not yet reached a terminal status → entry returns to
    `pending` and waits for the parent.
  - If the parent has **already reached** a terminal status while the entry
    was suspended → the entry is re-evaluated immediately against the actual
    parent status: it fires, is skipped, or fails as usual. This is the path
    that handles "I suspended, fixed the child template, parent finished
    while I was fixing, now resume → fire with the fix".
- Cancel is allowed from `pending` or `suspended` (→ `cancelled`).

### CLI surface

```sh
# List entries for a parent workflow (or globally by status)
scitq workflow chain list --workflow-id N
scitq workflow chain list --status pending           # everything still armed
scitq workflow chain list --status suspended         # paused, awaiting user

# Inspect a single entry, incl. resolved params at fire time
scitq workflow chain show --id M

# Lifecycle controls
scitq workflow chain suspend --id M
scitq workflow chain resume  --id M
scitq workflow chain cancel  --id M

# Edit mutable fields while suspended
scitq workflow chain edit --id M --template foo --on always \
                                  --param key=value --param-clear stale_key
```

The lineage shows up on existing workflow commands too: `scitq workflow show
--id N` displays the parent (if any) and lists the chain entries armed
behind this workflow, with each entry's status and child workflow id (when
fired).

## Implementation layers

1. **DSL / YAML schema (`yaml_runner.py`, `workflow.py`)** — parse the
   top-level `chain:` block. Each entry: `template`, optional `@version`,
   `params` map, optional `when` (truthy expression; default `true`),
   optional `on` (`succeeded` | `always` | `failed`; default `succeeded`),
   optional `always_new` bool. Reject unknown keys.
2. **Server: materialise rows at parent-submit time.** For each entry in the
   parsed `chain:`, insert one `workflow_chain_entry` row with
   `status=pending`, snapshotting all fields. This guarantees chain
   semantics survive template edits/deletes.
3. **Server: parent-completion hook.** When a workflow transitions to a
   terminal status (`S`/`F`), select its `pending` entries, evaluate `on:`
   against the actual status and `when:` against the parent's resolved
   state, then resolve each surviving entry's `params_template` and submit
   a child `RunTemplateRequest` (with `continue_last: true` by default;
   respecting `always_new: true`). Transition each entry to its terminal
   state (`fired` / `skipped` / `failed`).
4. **Server: backward lineage.** Add `workflow.parent_workflow_id` (nullable
   FK) populated when a child is fired. `template_run.parent_template_run_id`
   mirrors it at the run-record layer for audit.
5. **Server: lifecycle RPCs.** Add `SuspendChainEntry`, `ResumeChainEntry`,
   `CancelChainEntry`, `EditChainEntry`, `ListChainEntries`, `GetChainEntry`.
   Resume re-checks parent status and may immediately invoke the
   completion-hook evaluator for that single entry.
6. **CLI** — `scitq workflow chain {list,show,suspend,resume,cancel,edit}`
   per the section above. `scitq workflow show --id N` is augmented to print
   the chain.
7. **UI** — out of scope for v1. Once the CLI surface is stable, a future
   pass can surface the chain on the workflow page (parent lineage + armed
   entries with controls). Tracking this as a follow-up rather than a v1
   item.
8. **Tests (`tests/integration/chain_workflow_test.go`)** — E2E parent runs,
   entries materialise as `pending`; parent succeeds, entries transition to
   `fired` with mapped params; re-extend reconciles via implicit
   `--continue`; `always_new: true` creates a new child; `on: always` fires
   on parent `F`; `on: failed` fires only on parent `F`; `when:` falsy
   transitions the entry to `skipped`; chain entry referencing a missing
   template transitions to `failed` with `error_message` set; suspend before
   parent completion holds the entry; resume after parent has terminated
   fires immediately; cancel from `pending` or `suspended` is terminal;
   editing a suspended entry's params is reflected at fire time.

## Non-goals / caveats

- **No step-level triggers in v1.** The chain fires on the parent's workflow
  completion, not on a specific step. (Add later if a real case asks for it.)
- **No backpressure from child onto parent.** The parent's terminal status is
  fixed by its own tasks; a child failure does not retroactively mark the
  parent failed. The chain is a *forward* trigger only.
- **No automatic input-flow from child back to parent.** Chaining is a DAG,
  not a cycle.
- **Bounded depth.** v1 allows arbitrary chain depth (child can itself
  declare `chain:`). A loop guard ensures the same template can't appear
  twice in a single chain ancestry (caught at child-submit time, with the
  ancestry path surfaced in the error).
- **Param mapping uses canonical JSON for the implicit `--continue` match**,
  matching the existing `continue` semantics. Formatting drift in the mapped
  values (whitespace, key order) does not cause a miss.
- **Cross-provider concerns** are inherited from the existing
  workflow-extension rules; if the child specifies a different
  provider/region than what was used to write the parent's published outputs,
  it is the operator's responsibility (documented), pending the future
  `workflow.provider/region` enforcement.
