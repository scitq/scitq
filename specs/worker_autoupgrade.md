# Worker / CLI version awareness, and server self-upgrade

## Scenario

The driving picture for this spec:

1. The operator upgrades the server while workers are still running tasks.
2. The server restart briefly drops every gRPC connection. The worker side
   already retries every server call (registration, ping, status updates,
   log streams), so a single in-flight call breaks but its retry succeeds —
   no task is lost just because the server bounced.
3. Within seconds, the next `PingAndTakeNewTasks` from each worker reaches
   the new server. At that moment the server compares the worker's reported
   commit to its own.
4. **Phase I — Visibility.** When the commits diverge, the server flags the
   worker `needs_upgrade`. The UI surfaces a badge on the worker row, and
   `scitq worker list` shows a status column / `--needs-upgrade` filter so
   an operator can see, at a glance, which slice of the fleet is running
   stale code and decide whether (and when) to redeploy.
5. **Phase II — Apply (worker only, amd64 only).** Once Phase I is in,
   workers can actually upgrade themselves on top of it: download the new
   binary, drain or wait for idle, restart. Two urgency modes
   (non-emergency idle-wait vs emergency drain). Optional Docker
   re-attach for in-flight runs.
6. **Phase III — Server self-upgrade.** Today the operator manually waits
   for in-flight admin jobs (worker create/delete, etc.) before kicking
   the server restart. The same job-completion check the server is already
   doing internally to gate destructive operations can drive an automated
   self-upgrade window.

The same Phase I detection runs on **the CLI** every time it talks to the
server: a one-line stderr warning if the CLI binary is older than the
server. **No Phase II for the CLI** — the CLI runs on every kind of
operator workstation (laptop, CI runner, locked-down corporate machine,
SSH bastion); the install method varies (brew, apt, manual binary, vendored
in a CI container) and the running user often doesn't have the permission
to overwrite its own binary anyway. Phase I (warn) is the only useful
behaviour. Phase III (auto-apply) doesn't exist for the CLI either.

## Non-goals

- **CLI auto-upgrade (any phase).** Explicitly out of scope, period. The
  CLI's only awareness behaviour is the Phase I stderr warning + a `scitq
  self version` inspection command. CLI install diversity (brew/apt/manual
  binary/CI containers) plus permission realities (the user often can't
  overwrite their own binary) make auto-apply harmful. Document the
  warning, leave the install up to the user.
- **Cross-architecture support for worker auto-apply (Phase II).**
  Phase II initially targets `linux/amd64` only. arm64 workers (Apple
  Silicon dev workers, Graviton) report their arch and show in Phase I
  visibility but are exempted from auto-apply until we sort out
  multi-arch binary distribution.
- **Container/Docker image upgrade.** Out of scope. Tasks pin their own
  container images; this spec is only about the scitq client binary
  running on the host.
- **Atomic fleet rollout.** Each worker decides for itself when to upgrade
  (idle window or drain). No central choreography. Mixed-version fleets
  during rollout are expected; the protocol is forward-compatible.

## Phase I — Detection and visibility (worker + CLI)

### Versioning

`internal/version/version.go` already defines:

```go
var (
    Version = "dev"     // e.g. v0.1.0 or git describe
    Commit  = "none"    // short sha
    Date    = "unknown" // build time, UTC
)
```

These are populated via `-ldflags` at build time (`make install` already
does it; verify it does for the worker binary too). Both the server and the
worker import this package, so each side knows its own version at runtime.

The unit of comparison is the **commit hash** (full SHA, not the short form).
Semver is not enough — between two `v0.7.7-dev` builds, only the commit
distinguishes "before fix X" from "after fix X". The full SHA is unambiguous,
ordered by `vcs.time`, and resilient to forks/branches.

We deliberately do **not** require workers to be on the *exact* same commit
as the server. The detection rule is:

> A worker is `up_to_date` iff `worker.commit == server.commit`.
> Otherwise it is `needs_upgrade`.

A worker built from a different branch than the server's current one will
flag as `needs_upgrade` even if its commit is "newer in time" — that is
intended. The worker fleet should track the server's branch.

### Reporting (worker → server)

Extend `WorkerInfo` (the registration message) and the periodic ping with
the worker's build identity. Add to `proto/taskqueue.proto`:

```proto
message WorkerInfo {
    string name = 1;
    optional int32 concurrency = 2;
    optional bool is_permanent = 3;
    optional string provider = 4;
    optional string region = 5;
    // New (phase 1):
    optional string version = 6;       // e.g. "v0.7.7-dev"
    optional string commit = 7;        // full git SHA
    optional string build_arch = 8;    // GOOS/GOARCH, e.g. "linux/amd64"
}
```

Also add the same three fields to `Worker` (the read-side message used by
`list_workers`, the UI, etc.) and persist them in the `worker` table:

```sql
ALTER TABLE worker
    ADD COLUMN version    TEXT,
    ADD COLUMN commit_sha TEXT,
    ADD COLUMN build_arch TEXT;
```

Set on `RegisterWorker`. Ping does **not** re-send these — the binary
doesn't change in-process; only after a restart from a newer image. Saves
bandwidth on every ping. The post-upgrade restart will hit `RegisterWorker`
again and refresh the row.

### Server-side decision

The server holds a single `expected_commit` value: the commit of the running
server binary. On each `RegisterWorker` and on each `worker list` query, the
server computes the worker's status:

```
status = match (worker.build_arch, worker.commit, server.commit) {
    ("linux/amd64", c, s) where c == s   => "up_to_date"
    ("linux/amd64", c, s) where c != s   => "needs_upgrade"
    (_, _, _)                            => "unsupported_arch"  // any non-amd64 worker
    (anything missing)                   => "unknown"           // pre-upgrade workers
}
```

This is computed live from the columns (no separate `upgrade_status`
column). The worker row stores the raw facts; the comparison is recomputed
whenever someone asks. That keeps the model honest if either side rebuilds:
the next read reflects the new state without a migration.

The `unknown` bucket exists deliberately. Workers built from this commit or
older won't send the new fields. Treat them as "pre-upgrade-aware" rather
than as `needs_upgrade` — we don't know their commit so we can't say. Once
they restart on a binary that does send the fields, they leave this bucket.

### Surface

- **CLI**: `scitq worker list` gains a `version`/`commit` column and a
  derived `status` column. Add a flag `--needs-upgrade` to filter. A
  shorthand `scitq worker list --needs-upgrade` returns just the list of
  workers that should be redeployed, suitable for piping into a manual
  redeploy loop.
- **UI**: in the worker table, render the status as a small badge next to
  the worker name. Tooltip shows the commit delta. Filter pill at the top.
- **gRPC**: `GetWorkerStatuses` and `list_workers` already return per-worker
  state; extend their messages with the three new fields (and the derived
  status as a string).

### Testing

Add an integration test that:

1. Starts a server.
2. Starts a worker with `internal/version.Commit = "abcdef1"` (overriden via
   ldflags or a test helper).
3. Asserts the registered worker row has `commit_sha = "abcdef1"`.
4. Asserts `worker list` reports `status = needs_upgrade` (server commit
   differs).
5. Restarts the worker with `Commit = server's commit`.
6. Asserts status flips to `up_to_date`.

Plus a unit test for the comparison function — easy to test in isolation
once it's a pure function of `(workerArch, workerCommit, serverCommit)`.

## CLI version warning (Phase I — same detection, different action)

The CLI imports `internal/version` exactly like the worker does, so its
commit is known at runtime. The CLI should compare its commit to the
server's on every command and warn — never auto-apply.

### Server-supplied commit

The server already exposes its own version on a few endpoints; if there
isn't a clean one for the CLI to use, add a tiny one:

```proto
rpc ServerVersion(google.protobuf.Empty) returns (ServerVersionResponse);

message ServerVersionResponse {
    string version = 1;
    string commit = 2;
    string build_arch = 3;     // informational
}
```

Cheap unauthenticated call (the version isn't a secret), so the CLI can
hit it on connect before the user has logged in. Cache the result for the
duration of the process.

### Behaviour

On every command that connects to the server (everything except `--help`,
`--version`, and offline-only operations):

1. The CLI calls `ServerVersion` after the gRPC channel comes up.
2. It compares `cli.commit` to `server.commit`.
3. If they differ, prints to **stderr** before producing the actual output:

   ```
   ⚠️  scitq CLI is out of date.
       Your CLI:  v0.7.7-dev (abc1234, 2026-04-12)
       Server:    v0.7.8-dev (def5678, 2026-05-01)
       Update with:  brew upgrade scitq      (or your install method)
   ```

   Always to stderr — that way `scitq task list -o json | jq …` keeps
   working without being polluted by the warning.
4. Continues with the real command. The warning is **never fatal**: a
   stale CLI is allowed to run; it just may not understand new fields or
   may emit slightly off output.

The wording should suggest the user's actual install method when known
(brew, apt, manual), but a generic "see https://…/install" is fine for v1.

### `scitq self version`

Add a small inspection command that prints the comparison even when no
real work is happening:

```
$ scitq self version
CLI:    v0.7.7-dev (abc1234, 2026-04-12, linux/amd64)
Server: v0.7.8-dev (def5678, 2026-05-01, linux/amd64)
Status: out of date — your CLI is behind the server by 12 commits
        update via:  brew upgrade scitq
```

When the CLI is on the same commit as the server it just prints `Status:
up to date` and exits 0. When they differ, it prints the warning above
and exits **0** (not an error — exit codes are reserved for actual
failures; the user might be checking on purpose).

The warning suppression: `SCITQ_NO_VERSION_CHECK=1` env var skips the
comparison entirely, for scripted environments where the noise isn't
helpful (CI test runners, automated workflows). The `self version`
command ignores this env var and always reports.

## Phase II — Worker auto-apply (sketched, not in immediate scope)

This part is roughed out so Phase I doesn't paint itself into a corner. It
is **not** in scope for the immediate implementation. CLI does not get a
Phase II — see Non-goals.

### Distribution

The server hosts the worker binary at a versioned URL:

```
GET /worker/<arch>/<commit>/scitq-client[.sha256]
```

`<arch>` is one of `linux-amd64` (initially), `linux-arm64`, ...; `<commit>`
is the full SHA. The server serves the binary it itself was built from —
i.e. the binaries that match `server.commit`. There are exactly two ways
this binary gets there:

1. The server's own build pipeline produces `scitq-client-<arch>` artifacts
   alongside `scitq-server` and they're embedded into the server (`embed`
   directive) or sit next to the server binary on disk.
2. The server is configured with an external URL (S3 / GitHub release) and
   redirects to it.

Phase 2 chooses (1) for amd64. (2) becomes interesting only when we cross
arches.

### Trigger urgency: emergency vs. non-emergency

Not every upgrade is equally urgent. A point-release with a fetch-layer fix
can wait until the worker is naturally idle — running tasks are still
correct, they're just running on a slightly older binary. A breaking
protocol change, by contrast, has to land *now* or in-flight tasks will
fail outright.

Workers therefore distinguish two modes:

| Mode | When it applies | What the worker does |
|---|---|---|
| **non-emergency** | default for any commit-difference upgrade | wait for natural idle (no `D`/`U`/`R` tasks); never preempt anything |
| **emergency** | server flags this build as breaking | actively *create* an idle window: stop accepting new task slots, let in-flight `D`/`U`/`R` finish (with optional Docker re-attach for `R`), restart |

The classification is computed by the **server** and reported alongside
the expected commit. The worker doesn't second-guess — if the server says
emergency, the worker drains.

#### What makes an upgrade emergency

Two triggers, OR'd:

1. **Major-version bump** detected from the version strings on either side.
   `v0.7.x → v0.7.y` is non-emergency; `v0.7.x → v0.8.0` (or any change in
   the leading two semver components) is automatically emergency. Major
   bumps are reserved for breaking protocol/runtime changes, so this is
   safe by construction.

2. **Opt-in marker on the build.** A new ldflag `internal/version.Urgent`
   set to `"true"` at build time marks an arbitrary commit as emergency
   even within a minor-version stream. Set it on the build command when
   merging a fix that breaks running workers (the recent reuse-redirect
   driver bug, for instance, would have warranted it: stale workers
   continued to silently corrupt downstream tasks). Default is `"false"`.

The server reads its own `version.Urgent` and sends it in the
`expected_commit` response payload:

```proto
message ServerVersionResponse {
    string version = 1;
    string commit = 2;
    string build_arch = 3;
    bool urgent = 4;          // server build was marked urgent
}
```

The combined "is this an emergency upgrade for *this* worker" decision
lives in a pure helper, easy to unit-test:

```go
func UpgradeMode(workerVersion, serverVersion string, serverUrgent bool) Mode {
    if majorOrMinorChanged(workerVersion, serverVersion) { return Emergency }
    if serverUrgent                                       { return Emergency }
    return NonEmergency
}
```

### Worker-side flow — non-emergency

```
on RegisterWorker / ping response:
    if mismatch and arch matches and mode == non-emergency:
        schedule an upgrade attempt
        wait for full idle, then upgrade
```

**Full idle** = no tasks of this worker in any of: `D` (downloading
inputs/resources), `U` / `V` (uploading outputs), `R` (running). Note all
three — leaving a `U` mid-flight would lose its output, so uploads count
just as much as downloads and runs do.

The worker does not stop *accepting* new tasks while waiting; it just
won't trigger the upgrade until the queue empties on its own. On a busy
fleet this can take an arbitrary amount of time, which is fine: the
upgrade isn't urgent.

Once idle:

```
1. Download new binary to /var/lib/scitq/scitq-client.new
2. Verify SHA-256 against the server-supplied checksum
3. Atomically rename(...) — Linux unlink-while-open keeps the running process alive
4. Emit a "going down for upgrade" worker_event so it shows in the audit trail
5. Exit cleanly. The supervisor (systemd / docker --restart unless-stopped /
   the launch script) restarts the worker on the new binary.
```

The supervisor-restart path is preferable to `exec`-in-place: no
in-process gymnastics, no half-state, the gRPC stream is cleanly closed,
and any goroutine leaks die with the parent.

### Worker-side flow — emergency

The worker has to *force* an idle window, not wait for one. The plan:

```
on emergency upgrade detection:
    1. Set worker status to "draining" — server stops handing out new
       task assignments to this worker. Existing assignments are not
       revoked.
    2. Allow currently in-flight D/U operations to finish (they are
       short, bounded, and lose data if interrupted).
    3. For currently-running R tasks (Docker), one of two paths:
         a) Wait for them to finish (simple, may be slow if a task is long)
         b) (Optional optimisation) Detach the worker from the container
            without killing it. Persist {task_id → docker_container_id}
            to /var/lib/scitq/in-flight-tasks.json before detaching.
            On restart, the new binary scans that file, reattaches via
            `docker logs --follow <id>` for each, and resumes the
            stdout/stderr capture + exit-code-and-upload sequence.
       Choose (a) for the first emergency-mode implementation; (b) is a
       latency win we can add later when we have appetite for the state-
       file invariant.
    4. Once drained, do steps 1–5 of the non-emergency flow.
```

The "queued actions to redo" naturally fall out of the restart: the new
binary registers, the server sees its task assignments still exist, and
hands them back. No special server-side bookkeeping needed for that.

#### Docker re-attach (optional, advanced)

The reason this is "advanced" and not in the first cut:

- We have to be sure the container hasn't already exited between the
  worker writing the state file and the worker dying. A stale entry on
  restart needs a container-still-running check before we trust it.
- If the new binary changed how outputs are uploaded (e.g. a new
  metadata field), the half-finished task picks up the new behaviour
  mid-flight. Usually fine but a sharp edge.
- If the supervisor restart loop kills the container (some systemd
  setups kill the whole cgroup), re-attach is moot — we'd just lose the
  task. The `docker run --label scitq-task=<id>` + `--detach` lifecycle
  needs to outlive the supervisor's child-cleanup.

For the first emergency-mode cut, simpler is better: drain by waiting.
Re-attach is a Phase 2.5 optimisation we can add when we measure the
cost of the simple wait and find it unacceptable.

### Failure modes

- **Download fails / hash mismatch**: log, skip this attempt, retry on next
  poll. The worker keeps running on its old binary.
- **New binary refuses to start**: the supervisor will restart it; if it
  keeps crashing, it'll eventually be in a crashloop. We need a safety
  rail: the worker writes the *previous* binary path to disk before the
  rename, and the supervisor's restart logic falls back to it after N
  failed starts. Simpler version for v1: don't try to be clever; let it
  crashloop and surface that in `worker list` (status `crashing`). The
  operator manually rolls back.
- **Upgrade triggered mid-task**: the idle gate prevents this. The worker
  runs every running task to completion (or to its own error) before
  upgrading.
- **Two workers race the same server reload**: not a problem — each worker
  decides independently based on its own idle state.
- **Architecture not amd64**: the `unsupported_arch` status path means the
  upgrade trigger never fires. The worker sits forever on its build,
  visible in `worker list`. Acceptable until we add arm64 binaries.

### Open questions

- **Do permanent workers (manual, not provider-deployed) opt-in?** Default
  behaviour for permanent workers should probably be: report status,
  do not auto-apply (non-emergency). Operators opt in with `--auto-upgrade`
  on `scitq worker create`. Provider-deployed workers (cloud VMs created by
  a recruiter) auto-apply by default — they're disposable. **Emergency
  mode overrides this**: if the server flags the build as urgent, every
  worker drains and restarts regardless of the opt-in. Otherwise an urgent
  fix can't reach the fleet, which defeats the point of the marker.
- **How is the upgrade observed by users?** Probably as a worker_event of
  class `lifecycle` with payload `{from: "<old commit>", to: "<new commit>",
  mode: "emergency"|"non-emergency", duration_ms:
  <download+drain+verify+restart>}`. Surfaces in the UI's worker events
  tab.
- **What if the server is rolled back?** The same comparison flips: workers
  whose commit was the new one now show `needs_upgrade`. They'd download
  the older binary on the next idle window (or drain immediately, if the
  rollback is itself flagged urgent). That's correct — we follow the
  server. (If the operator wanted to pin the fleet to a specific commit
  irrespective of server rebuilds, that's a different feature: `pin_worker
  <commit>`.)
- **`Urgent` flag at build time vs. at deploy time.** The spec puts it on
  the binary (ldflag). Alternative: a server-side runtime toggle (`scitq
  admin urgent on`) that flips the ServerVersionResponse field without a
  rebuild. Build-time is more honest (the urgency travels with the code
  that needs it) but operationally less flexible. Open: do we need both?
  Probably not for v1; revisit if we ever wish we'd flagged a *prior*
  build as urgent after the fact.
- **Drain timeout.** Emergency drain waits for in-flight tasks. What if a
  task takes 8 hours? Hard time bound (e.g. drain at most 30 minutes,
  then force-kill remaining containers and restart anyway) — the
  emergency presumably matters more than one task. Soft suggestion: the
  drain timeout is itself part of the emergency marker payload, so a
  particularly nasty bug can declare "drain at most 60 seconds, then
  pull the rug."

## Phase III — Server self-upgrade window (sketched)

Today an operator who wants to redeploy the server has to do it carefully:
if a `worker create` or `worker delete` job is in flight (provider call
in progress, half-provisioned VM, partial cleanup), bouncing the server
mid-call leaves an orphaned cloud resource or a half-deleted record that
needs hand-untangling. The current discipline is: wait for `scitq job
list --active` to be empty, then upgrade.

Phase III automates that wait. The server already tracks active admin
jobs internally (it has to, to gate destructive operations); the same
gating can drive a self-upgrade window:

1. Operator places a new `scitq-server` binary on the server host (or a
   container with the new image) but doesn't restart yet — they invoke
   `scitq admin self-upgrade <new-binary-path>` (or the equivalent
   container-orchestrator hook).
2. The server enters `pre-upgrade` mode: refuses to *start* new admin
   jobs (worker create/delete, recruiter changes, template runs that
   require new workers), lets in-flight ones complete. Existing
   workers / running tasks are untouched — those are independent and
   the old binary keeps serving them right up to its restart.
3. Once the active-jobs queue empties, the server emits a
   `pre-upgrade-complete` event, then exits cleanly (or hands off to a
   supervisor that swaps in the new binary, exactly as Phase II workers
   do — atomic rename + exit + restart).
4. New server starts, registers, workers' next ping reaches it (the
   gRPC retry loop is what makes this seamless from the worker side),
   and admin jobs resume.

The trigger for Phase III is **independent** of the urgency marker. The
server's self-upgrade is always graceful — there's no "emergency" lane,
because if the operator wants an emergency restart they still have the
nuclear option of restarting the process directly. Phase III is the
*non-emergency* automation; the existing manual restart remains the
emergency path.

Phase III does **not** require a new Urgent flag and does **not** read
`internal/version.Urgent`. It's the operator's own choice to trigger the
self-upgrade; the server doesn't second-guess based on what the new build
contains.

Open questions for Phase III:

- **What's the timeout?** If a worker-create job has been hanging on a
  cloud provider for 20 minutes, do we wait? Probably hard cap (e.g. 10
  min) then either bail with a clear error to the operator or force the
  job into a `failed/aborted` terminal state and proceed.
- **How does the new binary actually get there?** The CLI command takes
  a path; the server reads it on disk and execs it. For container
  deployments, the orchestrator (k8s, docker compose, systemd unit with
  `Restart=on-failure` and a swapped image) does the swap and the
  Phase III command just gracefully exits the running container.
- **Can workers tell the server is in `pre-upgrade`?** Probably yes —
  ping responses include an `upgrade_in_progress` flag so workers know
  not to spam unnecessary work. They keep running their existing tasks;
  they just don't ask for new ones during the window. (Symmetry with
  Phase II's drain semantics, but server-driven.)

## Migration / rollout plan

1. **Phase I — Detection and visibility** (next): proto fields, DB
   columns, status computation, CLI stderr warning, `scitq self version`,
   `scitq worker list --needs-upgrade`, UI badges, integration tests. No
   behavioural change for workers beyond the extra fields on register.
2. Run for a release cycle. Operators get visibility into the upgrade
   lag and manually redeploy `--needs-upgrade` workers.
3. **Phase II — Worker auto-apply (amd64)**: distribution endpoint,
   urgency classifier (major-bump detection + `Urgent` ldflag), worker
   idle gate or drain, atomic rename, supervisor restart. Add a
   `--auto-upgrade` server config (default off) to gate the rollout,
   then enable by default once we've watched a few rebuilds.
4. **Phase II.5 — Multi-arch distribution**: arm64. Punted until we
   actually run an arm64 fleet in production.
5. **Phase III — Server self-upgrade window**: `scitq admin self-upgrade`,
   active-jobs gate, `upgrade_in_progress` ping flag, supervisor restart.
   Independent of Phases I/II — can land in any order after Phase I, but
   Phase III's payoff for the operator is greater once Phase II is also
   live (worker fleet rolls forward without intervention while the server
   self-upgrades).

## Files & components touched (Phase I)

| Area | File / module | Change |
|---|---|---|
| proto | `proto/taskqueue.proto` | add `version`, `commit`, `build_arch` to `WorkerInfo` and `Worker`; add `ServerVersion` RPC + `ServerVersionResponse` (with `urgent` field, reserved for Phase 2 but defined now to avoid a later proto bump); regen via `make proto-all` |
| version | `internal/version/version.go` | add `Urgent string = "false"` ldflag-injected var; helper `IsUrgent() bool` for cleanliness |
| migration | new `server/migrations/000026_worker_build_info.up.sql` | add the three columns to `worker` |
| server | `server/server.go` (`RegisterWorker`, list-workers query, new `ServerVersion`) | persist incoming fields; project derived `status`; expose own version |
| server | new helper `server/version_compare.go` | pure function `func WorkerStatus(workerArch, workerCommit, serverCommit string) string` |
| client | `client/client.go` (registration) | populate `WorkerInfo.version` etc. from `internal/version` and `runtime.GOOS`/`runtime.GOARCH` |
| CLI | `cli/cli.go` (post-connect hook) | call `ServerVersion`, compare to local commit, print stderr warning if mismatched (skipped if `SCITQ_NO_VERSION_CHECK=1`) |
| CLI | `cli/cli.go` (`worker list`) | new column, `--needs-upgrade` filter |
| CLI | `cli/cli.go` (new `self version`) | print local + server build info and the comparison |
| UI | `ui/src/routes/.../workers.svelte` | status badge, filter pill |
| tests | `tests/integration/worker_version_test.go` | worker registration + status flip integration test |
| tests | `tests/integration/cli_version_warning_test.go` | CLI prints stderr warning on commit mismatch; suppressed by env var |

Estimated effort: half a day for Phase 1 wired end-to-end (worker reporting + CLI warning + `self version`). Phase 2 (worker auto-apply) is a separate ~1–2 days.
