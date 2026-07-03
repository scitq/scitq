# Task Status Transitions — Single Choke-Point + Snapshot Dispatch

## Problem

A task's status is the primary vehicle for observable state in scitq — it drives the assignment loop, the UI, the CLI, quality scoring, retry policy, and every counter the operator looks at during an incident. It is written by many actors from many places:

- Worker heartbeats reporting `S`/`F`
- The assign loop marking `P → A`
- The scheduler promoting `W → P` when dependencies clear
- Opportunistic-reuse shortcuts `P → S`
- Operator retries (`retryTaskInternal`, `EditAndRetryTask`, `edit_step_command`)
- `force_run_task` bypassing dependencies
- Template runners submitting a batch
- Workflow deletion cascading

Today each site is on the hook for **three writes**:

1. `UPDATE task SET status = 'X' ...` in the DB.
2. `s.stats.Adjust(wf, step, func(agg *StepAgg) { agg.OldBucket--; agg.NewBucket++ })` on the in-memory aggregator.
3. `ws.EmitWS("step-stats", wfID, "delta", {oldStatus, newStatus, ...})` for the UI's live counters (plus a sibling `ws.EmitWS("task", …)` for per-task rows).

Nothing enforces that all three run, that they agree on the status letters, or that they see the same "old" value. Every mismatch is a silent drift that persists until the next 5-minute Reconcile rewrites the aggregator from SQL. Bugs we've hit under this pattern:

- Hardcoded `OldStatus:"F", NewStatus:"P"` in the retry emit (`server.go:2196–2210`) sent the wrong old status to the UI for every retry whose parent was not `F`.
- Aggregator's unconditional `stepAgg.Pending++` (`server.go:2159`) counted a demoted `P→W` clone as `P`, drifting Pending upward.
- Reconcile SQL omitted `'A'` from the `accepted` bucket (`stepstats.go:108`), so tasks in `A` were silently un-counted every 5 min.
- `promoteWaitingTasks` (`assigntask.go:706`), skip-if-exists W→P (`assigntask.go:650`), reuse W→P (`assigntask.go:896`), skip-if-exists P→S (`assigntask.go:590`) all UPDATE status with **no aggregator adjust and no WS emit** — every one of these transitions is drift-until-Reconcile.

Seven known drift surfaces in a system with ~15 distinct task-status write sites (grep `SET status` under `server/`). The root cause is a **shape mismatch**: the wire protocol carries transitions (`{oldStatus, newStatus, retried}`) but the UI actually wants state (`{waiting: 209, pending: 25, ...}`). Every writer has to correctly reconstruct the transition metadata AND correctly maintain the state — same information, expressed twice, with no compiler help if they diverge.

## Design shift — snapshots, not deltas

The wire protocol carries **snapshots** of step-level counters, not per-transition deltas.

- Server maintains the in-memory aggregator as today (`StepAgg` in `server/stepstats.go`).
- Every `TransitionTask` updates the aggregator AND marks the affected step **dirty**.
- A single flush goroutine wakes every 100 ms; for each dirty step it emits a **full snapshot** of that step's counters and clears the dirty flag. Idle workflows produce zero traffic.
- The UI's step-stats handler replaces its counters with the snapshot. No delta application, no case-per-status switch, no `retried` flag branching.

The upshot: the class of bugs listed in the Problem section literally cannot exist. The server never says "this task went from X to Y" over the wire — only "step X now has these counters." No `oldStatus` string to hardcode, no `Pending++` for the UI to reproduce, no `retried:true` branch to forget. If the server's counters are right, the UI is right, mechanically.

## Invariants the spec locks in

1. **Single writer**: `task.status` is UPDATE'd from exactly one function (`TransitionTask` / `TransitionTaskBatch`). Every other call site delegates.
2. **Atomic pair**: for any status change, the DB write and the aggregator adjust either **both** happen or **neither** does. On DB commit failure, nothing else moves; on aggregator adjust failure after commit, Reconcile heals within one interval (logged, not silent). The WS emit is decoupled — it's driven by the dirty flag, not by the transition itself.
3. **Wire carries state, not events**: WS messages for step-stats carry `{stepId, counters: {...}}`. There are no `oldStatus` / `newStatus` fields on step-stats messages. Per-task events (individual task rows, error toasts) remain delta-style on their own topics.
4. **Total is derived, not written**: the aggregator's `Total` is the sum of the non-hidden per-status buckets. Nothing writes `Total` directly. Reconcile computes it from `COUNT(*) FILTER (WHERE NOT t.hidden)`; the delta path decrements/increments through the per-status buckets and lets Total fall out.
5. **Hidden is a distinct dimension**: `task.hidden` transitions (retry-parent-hide, undelete) go through a separate `SetTaskHidden` primitive that also marks the step dirty. Composed with `TransitionTask` at the call site when both apply.

If any of these invariants would be violated by a proposed call site, that site has a bug — the fix is at the caller, not in `TransitionTask`.

## Proposed API

```go
package server

// TransitionTask is the ONLY function that writes task.status. All
// existing call sites that UPDATE task SET status=... must delegate to
// it. Runs the DB write, adjusts the aggregator, and marks the step
// dirty for the next snapshot flush. Atomic pair guaranteed under the
// aggregator mutex; the flush is decoupled and runs on its own ticker.
//
// tx may be nil for standalone writes; when provided, the caller owns
// the commit — TransitionTask defers the aggregator adjust + dirty
// mark until AFTER the caller's Commit() succeeds.
func (s *taskQueueServer) TransitionTask(ctx context.Context, tx *sql.Tx, req TransitionRequest) (TransitionResult, error)

type TransitionRequest struct {
    TaskID     int32
    NewStatus  string          // 'P'|'W'|'A'|'C'|'D'|'O'|'R'|'U'|'V'|'S'|'F'
    // Optional guard: only transition if the CURRENT status equals this
    // value. Empty = unconditional. Matches the existing "WHERE status='W'"
    // pattern in many call sites — the SQL was doing this guard inline.
    ExpectStatus string
    // Optional additional columns to set in the same UPDATE (worker_id
    // on assignment, run_started_at on P→R, output on P→S reuse). Kept
    // as a typed struct rather than a map[string]any so callers can't
    // accidentally set a column the aggregator would care about.
    Extras     TransitionExtras
}

type TransitionExtras struct {
    WorkerID       *int32     // NULL to unassign
    RunStartedAt   *time.Time
    Output         *string
    ReuseHit       *bool
    ReuseOriginal  *string
    FailureClass   *string
    // ... one entry per column that transitions today set alongside status.
    // The struct is grep-able: any new column added here forces callers
    // to make the field explicit rather than passing an untyped map.
}

type TransitionResult struct {
    OldStatus string  // What the row was before, or "" if row didn't exist / guard failed
    Applied   bool    // False when ExpectStatus didn't match
}
```

**Batch variant** for cases where a single SQL statement transitions many rows at once (dependency promotion, workflow deletion, template execution):

```go
// TransitionTaskBatch runs one SQL UPDATE that touches many rows, then
// aggregates the affected rows into a single aggregator adjustment and
// marks each affected step dirty. Callers must supply a WHERE — we need
// to know what rows moved (via RETURNING) to update the aggregator.
func (s *taskQueueServer) TransitionTaskBatch(ctx context.Context, tx *sql.Tx, req BatchTransitionRequest) (BatchTransitionResult, error)

type BatchTransitionRequest struct {
    NewStatus string
    Where     string  // becomes the WHERE clause; RETURNING captures the affected rows
    Args      []any
}
```

Neither function is called from outside `server/`. Both are backed by the same primitive (`applyTransition`) so the semantics are identical.

**Hidden transitions** get a sibling primitive:

```go
// SetTaskHidden flips task.hidden and adjusts the aggregator to move
// the task between visible and hidden buckets for its current status.
// Marks the step dirty. Composed with TransitionTask at call sites that
// hide a task AND change its status in the same operation (e.g.
// retryTaskInternal hides the parent and creates a P/W clone).
func (s *taskQueueServer) SetTaskHidden(ctx context.Context, tx *sql.Tx, taskID int32, hidden bool) error
```

## Snapshot dispatch — the wire protocol

### Server-side flow

```go
// StepStatsAgg gains a dirty set alongside its counter map.
type StepStatsAgg struct {
    mu    sync.Mutex
    data  map[int32]map[int32]*StepAgg  // as today
    dirty map[stepKey]struct{}            // steps whose counters changed since last flush
}

// applyTransition (called by TransitionTask / TransitionTaskBatch /
// SetTaskHidden / Reconcile) increments/decrements the counters and
// marks the step dirty. Under the mutex.
func (a *StepStatsAgg) markDirty(wfID, stepID int32) {
    a.dirty[stepKey{wfID, stepID}] = struct{}{}
}

// Flush goroutine: 100 ms tick. Reads and clears the dirty set under
// the mutex, snapshots each dirty step's counters, then ships one WS
// message per workflow containing all dirty steps for that workflow.
func (a *StepStatsAgg) flushLoop() {
    ticker := time.NewTicker(100 * time.Millisecond)
    for range ticker.C {
        a.flushOnce()
    }
}
```

Idle workflows produce zero traffic. `flushOnce()` observes the dirty set empty and returns without emitting.

### Wire shape

One `step-stats` topic, one message shape:

```json
{
  "workflowId": 3190,
  "steps": [
    {
      "stepId": 74313,
      "counters": {
        "total": 237,
        "waiting": 209,
        "pending": 25,
        "accepted": 0,
        "running": 2,
        "uploading": 0,
        "succeeded": 0,
        "failed": 294,
        "reallyFailed": 0,
        "retrying": 0
      },
      "runningRun":  { "count": 2, "sum": 42.0, "min": 15, "max": 27 },
      "successRun":  { "count": 0, "sum": 0,    "min": 0,  "max": 0  },
      "failedRun":   { "count": 0, "sum": 0,    "min": 0,  "max": 0  },
      "startTime":   1751443860,
      "endTime":     0
    }
  ]
}
```

Full step snapshot per dirty step. Payload is <500 bytes per step in practice. A workflow with 10 dirty steps ships ~5 KB per flush.

**Why full snapshot rather than only-changed-counters**: idempotent. If a flush is delivered out of order relative to another (across reconnects), the last one wins and the state is correct. Partial snapshots would require the UI to track which counters were most recent per field — reintroducing the ordering sensitivity the design was meant to eliminate.

**Why one WS message per workflow rather than one per step**: aligns with the existing `step-stats/<workflowId>` topic subscription. Every subscribed UI gets one message per flush cycle even if 20 steps changed, and the fan-out cost scales with subscribers, not with steps.

### UI-side flow

```typescript
// StepList.svelte's step-stats handler collapses to:
wsClient.subscribe('step-stats', workflowId, (msg) => {
    for (const snap of msg.steps) {
        const step = stepsById.get(snap.stepId);
        if (!step) continue;
        Object.assign(step, snap.counters);       // waiting, pending, ...
        step.runningRun = snap.runningRun;
        step.successRun = snap.successRun;
        step.failedRun  = snap.failedRun;
        step.startTime  = snap.startTime;
        step.endTime    = snap.endTime;
    }
});
```

No delta application, no `case 'W': step.waitingTasks--`, no `if (retried) step.failedTasks++`. The entire delta-processing branch in `StepList.svelte:414–450` is deleted.

### Reconnect / initial hydration

On subscribe, the server ships a "welcome" message with the current snapshot for every step of the workflow — same shape as a flush message, just carrying all steps whether they're dirty or not. Same code path as flush; the "welcome" is just `flushOnce()` with an "include-all-not-just-dirty" flag.

The UI's initial `getStepStats` fetch (still used for CLI/programmatic access via gRPC) can be dropped for the Svelte UI once the welcome-message path is in — the WS subscription hydrates directly.

## Reconciliation

`server/stepstats.go:253` (`Reconcile`) rebuilds the entire in-memory aggregator from a fresh SQL query. It runs every 5 minutes today (`server.go:230–242`) and exists precisely because the delta path is drift-prone — every one of the seven bugs listed in the Problem section was silent until the next Reconcile healed it.

Under the snapshot dispatch model, Reconcile fits naturally as a first-class part of the design:

1. **Heal-on-drift**: if the aggregator adjust panics or the DB write commits but the adjust doesn't run, Reconcile puts the state back to SQL truth within one interval. Post-Reconcile, every step is marked dirty and the next flush ships fresh snapshots — the UI's state converges automatically. No special "Reconcile just happened, force a full refresh" wire event.

2. **Startup hydration**: `NewStepStatsAgg(db)` at boot IS a Reconcile. Aggregator state is not persisted; it's derived. Recovery from a crash is the same code path as periodic healing.

3. **Verifier under the property test**: `assertAggregatorEqualsReconcile` in `TestTransitions_AggregatorParitySequence` (see Testing strategy) IS a Reconcile. The test's assertion is literally "does my delta-driven aggregator match a fresh Reconcile" — same query, same code.

Reconcile has one cost: a full-table scan of the `task` and `step` tables (`stepstats.go:92–156`). On a workflow with 500 K tasks this takes ~half a second. Running every 5 minutes on a large deployment is measurable but bounded.

### Reconcile in the snapshot model

Reconcile's action under the new model is:

```go
func (a *StepStatsAgg) Reconcile(db *sql.DB) {
    fresh, _ := NewStepStatsAgg(db)  // full SQL rebuild
    a.mu.Lock()
    a.data = fresh.data
    // Mark every step dirty so the next flush ships the fresh state.
    for wfID, steps := range a.data {
        for stepID := range steps {
            a.dirty[stepKey{wfID, stepID}] = struct{}{}
        }
    }
    a.mu.Unlock()
}
```

Reconcile and normal operation converge on the same wire protocol. There is no "Reconcile message" — Reconcile is a source of snapshots, no different from `TransitionTask`.

### Explicit Reconcile triggers to keep

- **Startup** (`NewStepStatsAgg`).
- **Periodic** (interval configurable, default 5 min → 1 hr post-migration once the property test has been green in CI for a month).
- **On-demand** via a `POST /debug/reconcile` endpoint or a `scitq admin reconcile` CLI command, for the operator to force healing during an incident without waiting for the timer.
- **After a workflow deletion completes** — the deletion touches many rows across many steps; one Reconcile at the end is cheaper than trusting the delta path across the entire cascade. Also naturally emits fresh snapshots for every affected step, so the UI sees "workflow gone" in one flush.

Never remove Reconcile entirely. The invariant "aggregator == Reconcile(db)" only holds if Reconcile stays authoritative. And startup hydration always needs it.

## Why not deltas

For completeness, the alternatives considered and why they lose to snapshots:

- **Per-transition deltas** (current model): the wire protocol carries `{oldStatus, newStatus, retried}` and the UI reconstructs state via a switch. This is what shipped the seven drift bugs — every writer has to correctly compute the transition metadata AND correctly update the aggregator, and any mismatch is silent until Reconcile. The design surface is quadratic in transitions × writers; each new writer is a new bug opportunity.

- **Opt-in batch scopes** (wrap known bulk operations in `BeginBatch()` / `End()`): reduces emit fan-out during known batches but requires every batching call site to opt in. Deletes cascade, template exec, retry-hide-then-clone all need the wrap; each unwrapped batch is a regression. Emit-side coalescing gets the same fan-out reduction without discipline requirements.

- **Emit-side coalescing of deltas** (merge delta messages within a 100 ms window): fixes fan-out but keeps the fundamental shape mismatch (wire carries transitions, UI wants state). Doesn't solve the class of #10-style bugs.

- **Sequence-number snapshots** (client sends "I have state at seq N", server replies with a delta stream or a snapshot): solves reconnect but adds a snapshot-vs-delta reconciliation. Snapshots everywhere is this idea's fixed point.

Snapshots subsume all of these. The design surface collapses to: aggregator counters, dirty flag, 100 ms flush.

## Migration plan

Existing call sites to migrate (grep `SET status = '[A-Z]'` under `server/`; ~15 sites total, listed by transition type):

| Transition | Call sites |
|---|---|
| `P → A` (assignment) | `assigntask.go:423`, `assigntask.go:1057` |
| `P → S` (reuse) | `assigntask.go:590`, `assigntask.go:802` |
| `P → S` (worker report) | `server.go:921` (UpdateTaskStatus main path) |
| `P → R`, `R → S`, `R → F`, etc. | `server.go:921` |
| `W → P` (dep clear, various) | `assigntask.go:650`, `assigntask.go:706`, `assigntask.go:896`, `server.go:685`, `server.go:1270`, `server.go:2293` |
| `P → W` (retry demote) | `server.go:2095` |
| `S → P` (edit_and_retry reuse reset) | `server.go:1803` |
| `F → W` (retry rearm) | `server.go:1814` |
| bulk status delta | `server.go:822` (updateTaskStatuses) |

Suggested order:

1. **Land the primitive first**: `server/task_transitions.go` with `TransitionTask` / `TransitionTaskBatch` / `SetTaskHidden`, backed by `applyTransition` that runs the SQL and adjusts the aggregator (marking the step dirty). Add no callers yet — just the API + tests.
2. **Land the snapshot dispatch**: `StepStatsAgg` gains the `dirty` set and `flushLoop`. WS emit path for step-stats becomes snapshot-only (no more delta events on that topic).
3. **Migrate the UI's step-stats handler**: switch from delta application to snapshot replacement. `StepList.svelte:414–450` collapses to `Object.assign(step, snap.counters)`. Deploy both server and UI together — the wire shape change is not backwards-compatible on that topic.
4. **Migrate `UpdateTaskStatus` (server.go:921)**: the fattest call site. This is where worker-reported status changes flow through. Every `P → R`, `R → S`, `R → F` in the system passes here.
5. **Migrate `retryTaskInternal`**: the trickiest — it does parent-hide (via `SetTaskHidden`) + clone-create (via `TransitionTask`) + demote check. Model the compound transition explicitly.
6. **Migrate the six silent W→P promoters**: `assigntask.go:650, 706, 896`, `server.go:685, 1270, 2293`. Each becomes a `TransitionTask(W→P, ExpectStatus="W")`. These are the sites that today have zero aggregator/emit code — the biggest drift reduction per LOC.
7. **Migrate the reuse P→S paths**: `assigntask.go:590, 802`. Include `ReuseHit`/`ReuseOriginal`/`Output` via `TransitionExtras`.
8. **Migrate `updateTaskStatuses` (server.go:822)**: bulk delta writer. Uses `TransitionTaskBatch`.
9. **Add the lint**: an integration test (or `go vet` plugin, or grep-based CI check) that fails if `UPDATE task SET status` appears outside `server/task_transitions.go`. Simplest form: `grep -rn "SET status\s*=" server/*.go | grep -v task_transitions.go | grep task && exit 1`.

Each migration step is verifiable by the property test below — no step lands without the test still passing.

## Testing strategy — property-based

The core invariant to test: at any moment, `s.stats.data == freshReconcile(db)`.

```go
// server/task_transitions_property_test.go
//
// Property: after any sequence of API calls that mutate task state,
// the in-memory aggregator is byte-identical to a fresh Reconcile.
//
// This is the test we lacked when the seven drift surfaces above
// shipped. Every one of them would fail this test the first time it
// tripped, in the same PR that introduced it.
func TestTransitions_AggregatorParitySequence(t *testing.T) {
    srv := newTestServer(t)

    // A prop-style loop: 1000 random operations from a fixed alphabet.
    // Each iteration picks one, invokes it, then asserts parity.
    ops := []op{
        submitTask, assignTask, completeTask, failTask,
        retryTask, editAndRetry, forceRun, deleteWorkflow,
        promoteWaiting, skipIfExistsHit, reuseHit, editStepCommand,
    }
    rng := rand.New(rand.NewSource(42)) // deterministic seed
    for i := 0; i < 1000; i++ {
        op := ops[rng.Intn(len(ops))]
        op(t, srv)
        assertAggregatorEqualsReconcile(t, srv)  // ← the invariant check
    }
}

// Every drift bug we've seen shows up as a specific mismatch here:
//   - hardcoded OldStatus:"F" retrying a W → parent counter over-decremented
//   - unconditional Pending++ on demote → Waiting missing 1, Pending +1
//   - Reconcile SQL missing 'A' → after any P→A, Accepted stays 0 in
//     Reconcile but is set in the aggregator, so the assertion fails
//   - silent W→P promoter → Waiting stays high after promotion, Pending
//     stays 0
func assertAggregatorEqualsReconcile(t *testing.T, srv *taskQueueServer) {
    fresh, err := NewStepStatsAgg(srv.db)
    require.NoError(t, err)
    diff := diffAggregators(srv.stats, fresh)
    require.Empty(t, diff, "aggregator drift detected: %s", diff)
}
```

**Additional unit tests** covering the primitive itself:

- `TransitionTask` with `ExpectStatus` mismatch returns `Applied=false, OldStatus=""` and touches nothing.
- Concurrent `TransitionTask` calls to the same taskID serialize via the aggregator mutex; no lost updates.
- `TransitionTaskBatch` returns the affected rows via `RETURNING` and aggregates in one call.
- The DB write and the aggregator adjust are transactional per the "atomic pair" invariant — a failure in one triggers a rollback of the other. Verify with a fault-injecting mock (aggregator adjust panics → tx not committed).

**Snapshot dispatch tests**:

- `TransitionTask` marks the step dirty. Next flush emits a message; the step is no longer dirty.
- Idle interval (no transitions) → `flushOnce()` observes empty dirty set, emits nothing.
- N transitions to the same step within 100 ms → one snapshot at the tick, not N.
- Transitions to K different steps within 100 ms → one WS message per workflow, carrying all dirty steps of that workflow.
- Subscribe emits a welcome snapshot containing every step of the workflow, whether dirty or not.
- Reconcile marks all steps dirty; next flush ships fresh snapshots.

**Regression tests** for the specific bugs we shipped fixes for in #10 (kept as targeted tests alongside the property test):

- Retry of a task in `W`/`P`/`O`/`R`/`S` produces a snapshot with the correct decremented counter for the parent's real bucket, not `reallyFailed--`.
- Retry with unmet prereqs produces a snapshot with `waiting` incremented, not `pending`.
- `promoteWaitingTasks` produces a snapshot with `waiting` decremented and `pending` incremented (was silent under the old delta path).

## What this spec does NOT change

- The DB schema. Same columns; same status alphabet.
- The `s.stats.data` in-memory aggregator's shape. Same `StepAgg` struct, same fields.
- The gRPC surface. `RetryTask`, `EditAndRetryTask`, `EditStepCommand`, `UpdateTaskStatus`, `GetStepStats`, etc. keep their proto messages — only their internals change.
- Reconcile stays. See the Reconciliation section — its role is elevated in this design (safety net + startup hydration + property-test verifier + trigger for post-cascade snapshots), not eliminated.
- Per-task events on the `task/*` topic (individual task rows, stdout arrival, error toasts) remain delta-style. This spec is specifically about the step-stats topic — the counter topic. Event streams (task changes, worker events, job progress) don't fit the snapshot model and stay as they are.

## What this spec DOES change

- The `step-stats` WS topic message shape. From `{oldStatus, newStatus, retried, ...}` deltas to `{workflowId, steps: [{stepId, counters, ...}]}` snapshots. Not backwards-compatible on that topic; server and UI ship together.
- The UI's `StepList.svelte` step-stats handler collapses from a delta-application switch to `Object.assign`. Delete ~50 LOC.
- Every task-status UPDATE in `server/*.go` funnels through `TransitionTask` / `TransitionTaskBatch` / `SetTaskHidden`. Delete ad-hoc `stepAgg.XYZ--` / `stepAgg.XYZ++` and `ws.EmitWS("step-stats", ...)` at the ~15 existing call sites.

## Open questions

1. **`task.hidden` transitions**: proposed as a sibling `SetTaskHidden` primitive rather than a field on `TransitionRequest`. Sibling keeps the aggregator adjust simpler (hidden-count math is a different axis from status math). Confirm during Step 5 of the migration (retryTaskInternal) when the first real hidden-transition-plus-status compound shows up.

2. **`Total` bucket derivation**: Reconcile derives `Total` from `COUNT(*) FILTER (WHERE NOT t.hidden)`. Recommend the in-memory aggregator does the same lazily (`func (a *StepAgg) Total() int32 { return a.Waiting + a.Pending + ... }`) rather than tracking it as a stored counter — one fewer field to drift. Read path already touches every bucket for the UI columns.

3. **Coalesce window tuning**: 100 ms is the default. Instrument the retry-burst latency operators observe post-migration and adjust if needed. Likely stays at 100 ms — imperceptible on isolated actions, big win on bursts.

4. **How to model an `edit_step_command` batch**: it can touch F tasks (retry, so `SetTaskHidden(parent, true)` + `TransitionTask(clone, P)` each), P/W tasks (in-place `TransitionTask` with same-status but new command — is that a transition at all if status doesn't change?), and R tasks (touch nothing today; probably touch nothing tomorrow). Recommend: `edit_step_command` emits `TransitionTask` calls for each row that actually changes status; command-only edits don't go through `TransitionTask` at all (they're a `task.command` UPDATE, not a status transition). The flush window handles the emit fan-out automatically — no explicit batching wrapper needed.

5. **Welcome snapshot vs `GetStepStats` gRPC**: with the WS welcome snapshot, the UI's initial `getStepStats()` fetch on subscribe becomes redundant. Keep the gRPC for CLI/programmatic access; drop from the UI once welcome-snapshot is in.

## Related work

- **#10 fix (2026-07-02)** — the immediate patches to `retryTaskInternal` and `stepstats.go` were tactical fixes to the specific drift surfaces we hit during the workflow 3190 incident. This spec is the structural fix.
- **Log stream synchronization** (`specs/log_stream_synchronization.md`) is a peer effort — same "many actors writing to the same table" shape, resolved by making the client-side handoff explicit. `TransitionTask` is the same idea on the server side.
