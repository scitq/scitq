# Changelog

Each release lists what it adds, changes, deprecates, removes and fixes.
Versions follow [semantic versioning](https://semver.org); what each kind of
release may change is in
[Compatibility and versioning](docs/reference/compatibility.md). Releases
before 1.0 are described in their GitHub release notes.

When a release needs more than the [routine upgrade](docs/upgrading.md),
its entry says so under **Upgrade notes**.

## 1.0.0 — unreleased

From 1.0, scitq's interfaces are stable: the gRPC API, the YAML template
format, the Python DSL, the MCP tools, the CLI and the configuration keep
working across all 1.x releases.

### Upgrade notes

Read [Upgrading → To 1.0](docs/upgrading.md#to-10) before upgrading: the
worker token is restricted to workers, every CLI must be upgraded, and
sessions now expire after 30 days without use.

### Added

- **Output manifest.** Each task records the files and bytes it produced
  in `/output` and how many reached their destination with the right size
  (`task list --long`, UI, MCP). A shortfall fails the task with class
  `network`, so it is retried if it has retries left.
- **Audit trail.** Every change to a worker's assignment, a task, a step, a
  workflow or a template records what changed, when, and who made it — a
  user, an agent (user and MCP client name), or a scheduler component such
  as a recruiter. `scitq audit list|prune`, MCP `list_audit`.
- **Compatibility policy** and its CI checks: `buf breaking` on the gRPC
  API, recorded public surfaces (`compat/*.txt`), and an upgrade test from
  the previous release.
- **Recruitment rules** written down in [Recruitment](docs/reference/recruitment.md),
  also served to agents by MCP `get_doc`.
- **Security page** describing who can call the server and with what rights
  ([Security](docs/reference/security.md)).
- **Concepts page** (task lifecycle, retries, workflow success,
  recruitment), **upgrade guide**, and this changelog.
- `failure_class` is returned by `ListTasks`, shown by `task list --long`
  and MCP `list_tasks`.
- `scitq worker list --json`.
- **Branding**: an installation's name, logo (optionally a second one for
  dark backgrounds), two brand colors and the sidebar's background per
  theme (`ui:` in `scitq.yaml`), shown from the first render.
- **Tasks survive a client restart.** A restarted worker client re-attaches
  to the containers its tasks still run in: the tasks keep their id, their
  run and their log, and finish normally. Output printed while no client
  was running is sent when it re-attaches; a few lines may repeat.
  `RegisterWorker` takes `running_task_ids` and returns `kept_task_ids`.
- A force-run button on waiting tasks in the UI's task list (runs the task
  without waiting for its prerequisites).
- The UI's worker events panel filters by level (all, warnings, errors;
  a click on the unread warnings count shows the warnings) and loads older
  events on demand. `ListWorkerEvents` takes `before_event_id` to page.
- MCP `update_recruiter` takes `rounds` and `timeout`.
- Each `job.created` event carries the complete job row.
- Step-stats snapshots carry the workflow's totals.

### Changed

- **The worker token allows only the calls workers make.** Every other call
  needs a user session.
- **Sessions** are checked against the server's session table on every
  path (gRPC, UI, MCP). They expire after 30 days without use (365 days at
  most). A password change revokes the user's other sessions; an admin
  password reset revokes all of them.
- **`scitq_auth` tasks** get a token of their own, valid while the task is
  active, allowing `scitq file copy` and `scitq file list` only.
- **The event socket `/ws`** requires a session.
- **All recruiters of a step share one `rounds` value**: creating one that
  disagrees is refused, and updating `rounds` applies to all of them.
- **`oom`** is reported only when Docker sets `OOMKilled`; exit code 137
  alone is `other`.
- **UI live updates** are applied in batches, so values no longer flash
  states a task passes through in milliseconds; the workflow list's
  counters follow the server's snapshots.

### Removed

- The `/WorkerToken` HTTP endpoint.

### Fixed

- Streaming calls (task log streams) were not authenticated.
- MCP accepted logged-out tokens, and deleted users kept MCP access.
- Two logins of the same user within one second shared a session.
- The initial admin password was generated with a non-cryptographic random
  source, and its log line was inverted, so a generated password was never
  shown.
- A task with container options failed at submission (migration 57).
- MCP `get_doc` refused pages it listed (`optimization`).
- Dashboard counts: an automatic retry took its failure off the Failed
  count; a manual retry subtracted the parent twice.
- The workflow list's succeeded / running / failed counters did not update
  live.
- Jobs added live disappeared from the dashboard's job list; the provider
  failure alert ignored failures after page load.
- Collapsing an expanded workflow stopped live updates on the workflow
  page.
- Steps created while the step table was scrolled never appeared.
- Forcing a waiting task to run left it counted as waiting on the
  dashboard.
- Workers crashed under parallel transfers ("CRITICAL: Failed to load config
  file" from rclone), failing every task they held: each transfer wrote its
  own rclone config file, set it as rclone's process-wide config path
  without a lock, and deleted it when done. A worker now writes one config
  file and keeps it.
- When a worker's client restarts, the tasks it had downloaded ahead (O) or
  was uploading after a failure (V) are failed and retried at once, like
  its running ones, instead of lingering until the next reconciliation.
- A task printing a progress bar (redrawn with `\r`), a line over 64 KiB, or
  output that is not valid UTF-8 hung forever: the worker stopped reading
  its output, and the task blocked on its next write. Worker logs now split
  lines on `\r` as well as `\n`, cut lines at 16 KiB, replace invalid UTF-8,
  and always read a task's output to its end.
- `scitq step stats --totals` showed an elapsed time of decades when a
  workflow had a skipped step: a step none of whose tasks ran reported
  its start as 0 (1970). Its start and end are now absent, and the CLI
  ignores a start of 0 from older servers.
- Workers warned "no cgroup peak found" for tasks that ended within a
  second or two, typically ones failing at start: the container was gone
  before the memory sampler found it. Such misses are now debug events;
  the warning remains for containers that ran 5 seconds or more.
- Azure's "The requested VM size … is not available in the current region"
  (`InvalidParameter`) was retried and never blacklisted the flavor. It now
  disables the flavor in that region at the first refusal, like the other
  permanent VM-size refusals; `scitq flavor enable` re-enables it.
- When a worker's client restarted (an automatic package upgrade restarts
  it, for one), the containers of its running tasks kept running: the
  server failed and retried those tasks, and the worker took new ones on
  top, overcommitting its memory. The new client now re-attaches to them
  (see Added), and removes those the server no longer assigns to it.
- Tasks failed because their worker re-registered are now in the audit
  trail, with the re-registration as the actor.
- A task that a worker still held when it was recycled to another step ran
  with `$CPU` and `$MEM` from the new step's concurrency: a 16-CPU task
  moved onto a 1-CPU step ran with `CPU=1`. A task now gets its share of
  the worker (its weight over the concurrency), which recycling preserves.
  Recycling also rescales the weight of tasks downloaded ahead (O).
- A YAML step's `retry:`, or its module's, was ignored: only the template's
  top-level `retry:` reached the tasks. It now applies to the step's tasks.
- A task submitted without a retry budget had none recorded, so its
  failure never counted as final: a fan-in with `accept_failure` waited
  for it forever. Such a task now has a budget of 0.
- A task whose prerequisite failed for good, through a dependency that does
  not accept failure, stayed in W forever, and its workflow was marked F
  while a fan-in could still run. The task now fails too, with failure
  class `upstream`, and so do its own dependents; a fan-in with
  `accept_failure` then runs with what succeeded. Retrying the failed
  prerequisite puts these tasks back in W. A workflow finishes only when
  all its tasks are S or F.
- Forcing a waiting task to run (`force_run_task`) or retrying a task in a
  failed workflow left the workflow F, so nothing assigned the task or
  recruited for it. Both now set the workflow back to running.
- SRA downloads (`run+fastq://` falling back to `sra-aws` or `sra-tools`)
  ignored a failed compression, and could keep a `.gz` left by a failed ENA
  transfer of the same run. A failed compression now fails the download,
  and the compression replaces any such file. The SRA download gives up a
  stalled transfer after 3 tries of 60 seconds (it waited up to 15 minutes
  for each of 20 tries), and the ENA metadata query after 2 minutes (it
  never did). The worker logs which method delivered each run.
