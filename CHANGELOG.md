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
