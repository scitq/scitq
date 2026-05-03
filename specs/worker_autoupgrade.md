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
5. **Phase II — Operator-triggered apply (worker only, amd64 only).**
   Phase I tells operators which workers are stale; Phase II gives them
   a button. `scitq worker upgrade <id|--all> [--emergency] [--cancel]`
   flags workers for upgrade; the worker sees the flag on its next ping
   and acts: idle-wait by default, drain if `--emergency`. The operator
   stays in control — bad builds don't propagate without consent, and
   urgency is the operator's call (it's a property of intent, not of the
   build itself: the same fix can be urgent for four workers running an
   affected step and pointless for fifty about to be reaped).
6. **Phase III — Server graceful gate via SIGUSR1.** Today the operator
   manually waits for in-flight admin jobs (worker create/delete, etc.)
   before kicking the server restart. Phase III installs a SIGUSR1
   handler on the server: `kill -USR1 <pid>` enters a gate that refuses
   new admin jobs, lets in-flight ones complete, emits a JSON status
   line, and exits cleanly. The supervisor (systemd, docker, the launch
   script) brings up the new binary the operator already laid down with
   `make install-server`. No CLI credentials needed on the server host —
   Unix already gates signals on UID/root, which is exactly the trust
   boundary we want.

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

(The deployed Phase I includes a fourth `urgent bool` field that was
intended for an automatic urgency classifier in Phase II. The
operator-triggered redesign removes that classifier — the field is
unused and should be dropped as part of Phase II's proto changes,
before any external caller depends on it.)

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

## Phase II — Operator-triggered upgrade (worker only, amd64 only)

### Why operator-triggered, not auto

Auto-apply on commit-mismatch was the obvious design and the wrong one.
Two arguments killed it:

1. **Bad-build blast radius.** A flawed client binary that auto-deploys
   to the entire fleet is a single decision that can take down every
   running task. Detection (Phase I) plus an explicit operator action
   keeps the operator in the loop without losing convenience — they
   already get the `⚠️ stale` badge and `worker list --needs-upgrade`,
   which is enough signal to act.
2. **"Emergency" is intent, not build identity.** Whether an upgrade
   is urgent depends on which workers are running which steps right
   now. The same fix can be urgent for four workers running an
   affected pipeline and pointless for fifty about to be reaped by the
   recruiter. The server can't know that. The operator can.

So Phase II ships a **manual trigger, centrally orchestrated**: one
command flags one or many workers; each worker reads the flag from its
ping response and acts. The mechanism (download, verify, atomic rename,
exit, supervisor restart) is unchanged from the original auto-apply
sketch — only the trigger moves from automatic to operator.

### Trigger surface

```
scitq worker upgrade <id>           # one worker, normal upgrade (idle wait)
scitq worker upgrade <id> --emergency   # one worker, drain
scitq worker upgrade --all          # every worker, normal upgrade
scitq worker upgrade --all --emergency  # every worker, drain
scitq worker upgrade <id> --cancel  # clear a pending request
```

Granularity is deliberately just *single ID* and *--all*. Batching by
step / workflow / arch is left to the shell:

```
scitq worker list --needs-upgrade --json | jq -r '.[].id' | \
    xargs -I{} scitq worker upgrade {}
```

`--needs-upgrade` already filters out workers the operator shouldn't
bother with (up-to-date and unsupported_arch); the loop is one line.
We can add native filters later if real usage demands it.

`--emergency` on an idle worker behaves identically to a normal upgrade
(both wait for idle; an idle worker is already idle). The server doesn't
refuse it — let the operator drive, keep the server dumb.

### Server side

A new column tracks pending requests per worker:

```sql
ALTER TABLE worker
    ADD COLUMN upgrade_requested TEXT;     -- NULL | 'normal' | 'emergency'
```

A new admin RPC sets it:

```proto
rpc RequestWorkerUpgrade(WorkerUpgradeRequest) returns (WorkerUpgradeReply);

message WorkerUpgradeRequest {
    repeated int32 worker_ids = 1;     // empty + all=true means every worker
    bool all = 2;
    string mode = 3;                   // 'normal' | 'emergency' | 'cancel'
}

message WorkerUpgradeReply {
    repeated int32 affected_worker_ids = 1;
}
```

`mode='cancel'` clears the column to NULL.

The ping response carries the current value back to the worker:

```proto
message TaskQueueClientResponse {
    // ... existing fields ...
    string upgrade_requested = N;     // '' | 'normal' | 'emergency'
}
```

The server sets the column once and lets the worker pick it up on its
next ping. The flag is *latched*: a busy worker with `upgrade_requested
= 'normal'` keeps seeing it on every ping until either it acts (clears
the column itself once it has restarted on the new commit, via
RegisterWorker — same commit means request fulfilled, server clears it)
or the operator cancels.

### Worker-side flow — normal (`upgrade_requested='normal'`)

```
1. Wait for full idle: no tasks of this worker in any of D / U / V / R.
   Continue accepting new task slots while waiting — the operator chose
   normal precisely because they don't want to preempt work.
2. Download:
     curl -ksSL https://<server>/scitq-client?token=<t>      -> /var/lib/scitq/scitq-client.new
     curl -ksSL https://<server>/scitq-client.sha256?token=<t> -> sha
3. Verify: sha matches sha256(scitq-client.new). Mismatch → log,
   abort this attempt, leave column set, retry on next idle window.
4. Emit worker_event 'upgrade_starting' (mode=normal).
5. atomic rename(scitq-client.new, /usr/local/bin/scitq-client).
   Linux unlink-while-open keeps the running process alive.
6. Exit cleanly. Supervisor (systemd / docker --restart unless-stopped /
   the launch script) restarts on the new binary.
7. New binary calls RegisterWorker with new commit; server sees match,
   clears upgrade_requested.
```

**Full idle** = no tasks of this worker in `D`, `U`, `V`, or `R`. All
four — losing a `U` mid-flight forfeits the output, so uploads count as
much as runs.

### Worker-side flow — emergency (`upgrade_requested='emergency'`)

The worker has to *force* an idle window:

```
1. Set worker status to 'draining'. Server stops handing out new task
   assignments to this worker. Existing assignments are not revoked.
2. Allow in-flight D / U / V to finish (short, bounded, lose data if
   interrupted).
3. Wait for in-flight R tasks (Docker) to finish.
   - Hard timeout: 30 minutes. After that, force-kill remaining
     containers and proceed (the operator chose emergency knowing this).
   - Force-killed tasks are marked F with reason 'killed by emergency
     upgrade'; they'll be retried by normal scheduling once the new
     binary is up.
4. Once drained, run steps 2–7 of the normal flow.
```

Docker re-attach (worker exits while leaving `R` containers alive,
re-attaches after restart) is a Phase 2.5 optimisation. Out of scope
for v1: simpler to wait or kill. We can add it when measured drain
latency justifies the state-file invariant cost.

### Distribution: existing endpoint, plus checksum

The server already serves the worker binary at:

```
GET /scitq-client?token=<cfg.Scitq.ClientDownloadToken>
```

(see `server/http.go:48`, used by Azure / OpenStack cloud-init scripts
at worker provisioning). Phase II reuses it — the same single-file model
(`cfg.Scitq.ClientBinaryPath` co-deployed with the server binary) covers
both new-worker bootstrap and existing-worker upgrade. **No new
endpoint, no commit/arch namespacing.** The implicit contract is
"operators co-deploy server + client binaries", which we already rely on.

The one addition is a tamper-detection sibling:

```
GET /scitq-client.sha256?token=<cfg.Scitq.ClientDownloadToken>
```

Streams the SHA-256 hex of the file at `cfg.Scitq.ClientBinaryPath`,
gated by the same token as the binary itself. Cached keyed by the
binary's `(mtime, size)` so repeated calls don't re-hash 30 MB on every
request. Workers verify the binary they just downloaded against this
hash before the atomic rename.

### Failure modes

- **Download / SHA mismatch.** Log, abort, leave `upgrade_requested`
  set, retry on next idle window. Worker stays on its old binary.
- **New binary refuses to start.** Supervisor restarts it; if it
  crashloops, it shows up in `worker list` as `crashing`. v1 keeps it
  simple and lets the operator roll back manually (the `--cancel` flag
  is no help once the worker has already restarted on the bad binary —
  operator action is `scp` the old binary back into place, restart).
  A bake-then-rename safety rail (boot on the new binary in a child
  process and require a self-test before swap) is a future hardening
  step.
- **Upgrade requested mid-task.** Idle gate (normal) or drain (emergency)
  handles it.
- **Architecture mismatch.** Worker checks `worker.build_arch` on
  receiving `upgrade_requested`. If not `linux/amd64`, it emits a
  worker_event `'upgrade_refused_arch'`, leaves the flag in place (so
  the operator sees it persist in `scitq worker list`), and does
  nothing. Operator clears it with `--cancel`.
- **Server rebuilt before worker upgraded.** The fetched binary is
  always whatever the server has on disk *now*. If the server bounced
  to a newer commit between the operator's request and the worker's
  idle window, the worker upgrades to the latest. That's fine —
  forward progress.

### Open questions

- **Permanent workers.** A permanent worker (manually created, no
  provider) is exactly as eligible as a recruited one — the operator
  triggers explicitly per-worker or via `--all`. No `--auto-upgrade`
  opt-in needed; there is no auto. (This was the only place in the old
  spec where permanent / recruited workers diverged. Operator-trigger
  removes the distinction.)
- **Worker_event surface.** Lifecycle events to emit:
  `upgrade_requested` (server side, when the column is set),
  `upgrade_starting` / `upgrade_complete` / `upgrade_failed` (worker
  side). Payload: `{from: <old commit>, to: <new commit>, mode:
  normal|emergency, duration_ms: ...}`.
- **`scitq worker upgrade --all` size.** On a fleet of 200 workers,
  `--all --emergency` would drain everything at once — almost certainly
  not what the operator wants. Should the CLI prompt "you are about to
  drain N workers, continue?" for `--all`? Lean yes for `--emergency
  --all`, no for plain `--all` (idle wait is harmless). Cheap to add.
- **Drain timeout configurability.** Hard 30-min cap is a default;
  exposing it as a flag (`--drain-timeout 5m`) is one line and worth
  doing if any operator ever runs into a task that legitimately needs
  longer than 30 minutes.

## Phase III — Server graceful gate via SIGUSR1

Today an operator who wants to redeploy the server has to do it
carefully: if a `worker create` or `worker delete` job is in flight
(provider call in progress, half-provisioned VM, partial cleanup),
bouncing the server mid-call leaves an orphaned cloud resource or a
half-deleted record that needs hand-untangling. The current discipline
is: wait for `scitq job list --active` to be empty, then upgrade. Phase
III automates that wait.

### Why a Unix signal, not a CLI command

The earlier sketch in this spec used `scitq admin self-upgrade` — an
authenticated CLI command. That has two problems:

1. **Root doesn't necessarily have CLI credentials.** The operator
   doing the file ops is root; the operator with `SCITQ_TOKEN` /
   `SCITQ_SERVER` is the user account that admins the cluster. Forcing
   one to assume the other (token-smuggling via `sudo -E`, or expecting
   `/root/.scitq/credentials` to exist) creates a permission knot we
   don't want to demand of any deployment shape.

2. **The trust boundary is already where it should be.** "You can
   signal this process" maps exactly to "you have authority over the
   server host" — same UID or root. We don't need application-level
   auth on top of kernel-level auth that's already correct.

So Phase III's trigger is a Unix signal: `kill -USR1 <pid>` (or
`pkill -USR1 -x scitq2-server`, or `systemctl kill -s USR1 scitq2-server`
if the operator runs systemd). No JWT, no shared secret, no localhost
RPC, no oneshot binary, no peer-handoff race against the supervisor.

### Why SIGUSR1 specifically

- **SIGTERM** would conflict with `systemctl stop` semantics — operators
  expect "stop" to actually stop, not "drain for potentially many minutes
  then stop."
- **SIGHUP** is the daemon-control idiom (nginx, postfix), but its
  *traditional* meaning is "reload config." Wiring SIGHUP to graceful
  exit means anyone who runs `systemctl reload scitq2-server` (or its
  unit's `ExecReload=`) gets a graceful exit instead of a reload. We
  could avoid the trap by simply not defining `ExecReload=`, but the
  semantic mismatch is still a foot-gun for someone who doesn't read
  the unit file first.
- **SIGUSR1** is explicitly reserved for application-defined behaviour
  in the POSIX signal table. Zero baggage. No surprise.

### Lifecycle

```
1. Server process running normally; signal handler installed for SIGUSR1.
2. Operator: kill -USR1 <scitq-server pid>
3. Server enters "gating" state:
     - Refuses to *start* new admin jobs (worker create/delete,
       recruiter changes, template runs that require new workers).
     - Lets in-flight admin jobs run to completion.
     - Existing workers and running tasks are untouched — they're
       independent of admin-job lifecycle, the old binary keeps
       serving their gRPC calls right up to its own exit.
4. Once the active-jobs queue empties, server emits a single JSON
   line on stdout (which goes to journald under systemd, or to the
   supervisor's log file otherwise):

     {"event":"server_drained","at":"2026-05-03T16:42:11Z","drain_seconds":47}

5. Server os.Exit(0) cleanly. The supervisor's restart policy
   (systemd Restart=on-success, docker --restart=on-success,
   the launch script's loop, etc.) brings up the new binary
   that the operator already laid down with `make install-server`.
6. Workers' gRPC retry loop reconnects within seconds — no
   client-side action needed.
```

### Hard timeout for the drain

A wedged provider call (worker-create stuck for 20 minutes on Azure)
shouldn't keep the gate open forever. Hard cap of 10 minutes; past
that, force the lingering job(s) to a terminal `failed/aborted` state,
log loudly, and proceed with the exit. The 10-minute cap is a constant
in the source, not configurable in v1; revisit if we hit a real
deployment that wants longer.

Cancelling a gate already in progress: not in v1. Sending a second
SIGUSR1 is idempotent (the gate-in-progress flag is set; second signal
is a no-op). If you want to cancel, your only option in v1 is to
`kill -KILL` the server before it exits — which will leave any
in-flight admin jobs in their committed-but-pending state, exactly
what Phase III is designed to avoid. Don't do that.

### Worker-side signal during the gate

`TaskQueueClientResponse` (the ping reply) gets a small flag:

```proto
message TaskListAndOther {
    // ... existing fields ...
    bool server_upgrade_in_progress = 8;
}
```

Workers respect it by simply *not asking for new task slots* during the
window — but they keep running existing tasks, and the gRPC retry loop
absorbs the brief restart. Symmetric with Phase II's worker drain, but
server-driven. The flag is informational; a worker that doesn't
understand it (older binary) just keeps working as normal, and that's
fine.

### What about emergency mode for the server?

There isn't one. If the operator wants an emergency restart, they
already have `kill -TERM` (graceful Go shutdown via context cancel,
which is what the existing service stop already does) or `kill -KILL`.
Phase III is the *non-emergency* automation; the existing manual stop
remains the emergency path. Adding a third "emergency drain with cap"
mode for the server would be overkill — admin jobs that are in flight
but not making progress are exactly what the 10-minute hard timeout
already handles.

## Installation: Makefile targets

The current Makefile has one `install` target that bundles all three
binaries (server, client, CLI). For Phase III we add:

1. **Per-binary `install-{server,client,cli}` targets**, independent.
   A sysadmin who only wants to refresh the CLI says
   `sudo make install-cli` and gets exactly that — no surprise client
   refresh, no surprise service restart.

2. **`.prev` backup**, one generation kept, alongside the binary.
   Before each install, if the destination already exists, it's
   renamed to `<binary>.prev` (in the same directory). One-command
   roll-back if a bad upgrade is detected:

   ```
   sudo mv /usr/local/bin/scitq2-server.prev /usr/local/bin/scitq2-server
   sudo kill -USR1 $(pgrep -x scitq2-server)
   ```

3. **No service management in the Makefile.** Build systems lay down
   files; init systems manage daemons. Mixing them is a sysadmin trap
   (non-portable across systemd / SysV / launchd / runit / Docker, and
   invisibly side-effecting for someone reading `make install`).

4. **`make server-upgrade` convenience target.** Bundles
   `install-server` + `pkill -USR1`. Operator does
   `sudo make server-upgrade` after the build; install puts the new
   binary in place (with `.prev` backup), the SIGUSR1 tells the
   running old server to drain-and-exit, the supervisor restarts on
   the new binary. No `systemctl` invocation, no shell-out specific
   to one init system.

```make
INSTALL_PREFIX ?= /usr/local/bin

# Atomic install with one-generation backup. Uses GNU `install` which
# does unlink+create+rename — works while the binary is running
# (avoids the ETXTBSY error a plain `cp` triggers on Linux when the
# target inode is mmap'd as a running ELF). The pre-rename of the
# existing target to `.prev` keeps a single roll-back generation.
define install_with_backup
	@if [ -f $(INSTALL_PREFIX)/$$(basename $(1)) ]; then \
	    mv $(INSTALL_PREFIX)/$$(basename $(1)) $(INSTALL_PREFIX)/$$(basename $(1)).prev; \
	fi
	install -m 755 $(1) $(INSTALL_PREFIX)/
endef

install-server: build-server
	$(call install_with_backup,$(BINARY_SERVER))

install-client: build-client
	$(call install_with_backup,$(BINARY_CLIENT))

install-cli: build-cli
	$(call install_with_backup,$(BINARY_CLI))

# Phase III: install new server binary, then signal the running old
# server to drain and exit. Supervisor restarts on the new binary.
# Uses pkill (POSIX) so this works on systemd, runit, launchd, and
# bare-process deployments alike. Adjust the binary name to match
# your install layout (scitq-server vs scitq2-server) via
# SERVER_PROCESS_NAME.
SERVER_PROCESS_NAME ?= scitq2-server
server-upgrade: install-server
	@if pkill -USR1 -x $(SERVER_PROCESS_NAME); then \
	    echo "🛠 Sent SIGUSR1 to running $(SERVER_PROCESS_NAME); supervisor will restart on new binary."; \
	else \
	    echo "ℹ️ No running $(SERVER_PROCESS_NAME) found; supervisor will start fresh on next launch."; \
	fi
```

### Why not also `make worker-upgrade`?

For provider-deployed workers, Phase II already handles upgrades
through the operator-triggered flow (`scitq worker upgrade <id>`):
operator from their CLI, no Makefile, no root on the worker host.

For permanent workers run by an operator who has root on the worker
host, the same `install -m 755` + supervisor-restart pattern applies,
but it's a manual operation per host and doesn't benefit from a
Makefile target — the operator is already running ad-hoc commands
on a remote host. The right pattern is to document the install
sequence (`sudo make install-client install-cli` then their own
service restart), not to bake a `worker-upgrade` target that assumes
the worker's process name and supervisor.

### Why `cp` was failing in your old script (operator note)

If you've ever tried to `sudo cp bin/scitq-client /usr/local/bin/`
while the worker service was running, the kernel returned
`ETXTBSY ("Text file busy")`. That's because `cp` opens the
destination with `O_TRUNC` and writes in place, so the destination
inode doesn't change and Linux refuses to truncate an inode that's
currently mmap'd as a running ELF. `install -m 755` (and `mv`) avoid
the problem because they create a *new* inode and rename it over the
old one — atomic at the directory entry level, and the running
process keeps an FD on the old inode that the kernel keeps alive
until the FD closes (the same trick Phase II's `os.Rename` worker
self-swap exploits). Net effect: with `make install-{server,client,cli}`
you no longer need to stop the service before installing.

## Migration / rollout plan

1. **Phase I — Detection and visibility** (next): proto fields, DB
   columns, status computation, CLI stderr warning, `scitq self version`,
   `scitq worker list --needs-upgrade`, UI badges, integration tests. No
   behavioural change for workers beyond the extra fields on register.
2. Run for a release cycle. Operators get visibility into the upgrade
   lag and manually redeploy `--needs-upgrade` workers.
3. **Phase II — Operator-triggered upgrade (amd64)**:
   `worker.upgrade_requested` column, `RequestWorkerUpgrade` RPC,
   `scitq worker upgrade [<id>|--all] [--emergency] [--cancel]` CLI,
   `/scitq-client.sha256` checksum endpoint, worker idle-wait + drain
   flows, supervisor restart. No server-side urgency classification —
   the operator picks the mode per invocation. No `--auto-upgrade`
   config (there is no auto).
4. **Phase II.5 — Multi-arch distribution**: arm64. Punted until we
   actually run an arm64 fleet in production. When it lands, the
   `/scitq-client?token=...` endpoint grows arch-aware: either an
   `?arch=linux-arm64` query param or a sibling
   `/scitq-client/<arch>?token=...` path. Workers send `build_arch`
   with the request so the server returns the matching binary.
5. **Phase III — Server graceful gate via SIGUSR1**: signal handler in
   the server, active-admin-jobs gate, JSON drained-line, clean exit;
   `server_upgrade_in_progress` ping flag; Makefile targets
   `install-server` / `install-client` / `install-cli` (each independent,
   each with one-generation `.prev` backup), `server-upgrade`
   convenience target that bundles install-server + `pkill -USR1`. No
   CLI command, no service-management invocation, no shared secret —
   trust is kernel-level (UID match or root). Independent of Phases
   I/II, but Phase III's payoff for the operator is greater once
   Phase II is also live (worker fleet rolls forward without
   intervention while the server self-upgrades).

## Files & components touched (Phase I — done)

| Area | File / module | Change |
|---|---|---|
| proto | `proto/taskqueue.proto` | add `version`, `commit`, `build_arch` to `WorkerInfo` and `Worker`; add `ServerVersion` RPC + `ServerVersionResponse`; regen via `make proto-all` |
| migration | `server/migrations/000026_worker_build_info.up.sql` | add the three columns to `worker` |
| server | `server/server.go` (`RegisterWorker`, list-workers query, new `ServerVersion`) | persist incoming fields; project derived `status`; expose own version |
| server | `server/version_compare.go` | pure function `WorkerUpgradeStatus(workerArch, workerCommit, serverCommit) string` |
| client | `client/client.go` (registration) | populate `WorkerInfo.version` etc. from `internal/version` and `runtime.GOOS`/`runtime.GOARCH` |
| CLI | `cli/cli.go` (post-connect hook) | call `ServerVersion`, compare to local commit, print stderr warning if mismatched (skipped if `SCITQ_NO_VERSION_CHECK=1`) |
| CLI | `cli/cli.go` (`worker list`) | version/commit/arch columns, `--needs-upgrade` filter, badge rendering |
| CLI | `cli/cli.go` (new `self version`) | print local + server build info and the comparison |
| UI | `ui/src/components/WorkerCompo.svelte` | status badge, tooltip with commit delta |
| tests | `tests/integration/worker_version_test.go` | worker registration + status flip integration test, `ServerVersion` RPC wire test |

## Files & components touched (Phase II — operator-triggered upgrade)

| Area | File / module | Change |
|---|---|---|
| proto | `proto/taskqueue.proto` | drop unused `urgent` field from `ServerVersionResponse`; add `upgrade_requested` string to `TaskQueueClientResponse` (ping reply); new `RequestWorkerUpgrade` RPC + `WorkerUpgradeRequest`/`WorkerUpgradeReply` |
| migration | new `server/migrations/000027_worker_upgrade_request.up.sql` | `ALTER TABLE worker ADD COLUMN upgrade_requested TEXT;` |
| server | `server/server.go` (`RequestWorkerUpgrade`, ping path, `RegisterWorker`) | new RPC handler (admin-gated); ping projects column into response; RegisterWorker clears column when worker.commit==server.commit |
| server | `server/http.go` | new handler `GET /scitq-client.sha256?token=...` — streams hex SHA-256 of `cfg.Scitq.ClientBinaryPath`, cached by `(mtime, size)` |
| server | `server/http_sha256_cache.go` (new, small) | the cache helper for the above |
| client | `client/client.go` (ping handler, upgrade goroutine) | observe `upgrade_requested`; on `'normal'` wait for full idle then upgrade; on `'emergency'` set status=draining, hard 30-min drain timeout, then upgrade; download from `/scitq-client?token=...`, verify against `/scitq-client.sha256`, atomic rename, exit |
| CLI | `cli/cli.go` (new `worker upgrade`) | subcommand: positional `<id>` or `--all`, flags `--emergency` / `--cancel`; prompts on `--all --emergency` |
| tests | `tests/integration/worker_upgrade_test.go` (new) | end-to-end: idle worker normal upgrade, busy worker waits, emergency drain, --cancel, unsupported_arch refusal, SHA mismatch |

## Files & components touched (Phase III — graceful gate)

| Area | File / module | Change |
|---|---|---|
| proto | `proto/taskqueue.proto` | add `bool server_upgrade_in_progress` to `TaskListAndOther` (ping reply); regen |
| server | `server/server.go` (signal handler, admin-job gate) | install `signal.Notify(syscall.SIGUSR1)`; on receipt set `gating=true`; intercept admin-job submission paths to refuse new jobs while gating; poll until in-flight jobs are zero or 10-minute hard timeout fires; emit `{"event":"server_drained", ...}` JSON line on stdout; `os.Exit(0)` |
| server | `server/server.go` (ping path) | project `gating` flag into `TaskListAndOther.server_upgrade_in_progress` |
| client | `client/client.go` (worker loop) | observe `server_upgrade_in_progress`; while true, skip asking for new task slots; keep running existing tasks; gRPC retry loop absorbs the bounce |
| Makefile | `Makefile` | add `INSTALL_PREFIX`, `install_with_backup` macro, `install-server` / `install-client` / `install-cli` (each with `.prev` backup), `server-upgrade` (install-server + pkill -USR1) |
| docs | install/upgrade section | document the SIGUSR1 trigger, the `.prev` rollback, the ETXTBSY note for the manual `cp` antipattern |
| tests | `tests/integration/server_upgrade_test.go` (new) | start server → submit a long-running admin job → SIGUSR1 → verify new admin jobs are refused → wait for in-flight to finish → verify JSON drained line → verify process exit 0; second test: SIGUSR1 with no in-flight jobs exits within ~1s |

Estimated effort: Phase I: done. Phase II: done. Phase III: ~1 day (signal handler + admin-gate + drained line is half a day; ping flag + worker-side handling + Makefile + integration tests is the rest).
