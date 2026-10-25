# Security

Who can call the scitq server, with what rights, and for how long.

## Callers

Every gRPC call, unary or streaming, carries a bearer token. The server
accepts three kinds:

| Token | Who holds it | What it allows |
|---|---|---|
| **Session** | A logged-in user: the UI, the CLI, an MCP client | Everything the user's account allows |
| **Worker token** | Every worker (`scitq.worker_token` in the configuration) | The calls a worker makes, and nothing else |
| **Task token** | A task started with `scitq_auth` | `scitq file copy` and `scitq file list`, while the task is active |

Three calls need no token: `Login`, `ChangePassword` (it checks the old
password) and `GetCertificate`. Workers from before v1.0 send their task log
streams without a token; the server accepts those for tasks running on such
workers only, until they are upgraded.

The HTTP endpoints follow the same rule: the event socket (`/ws`, which
feeds the UI's live updates) and `/api/task/edit-retry` need a session, and
`/mcp` needs one for every tool except `login`.

## Sessions

`scitq login`, the UI login and the MCP `login` tool each open a session.
The token they return is checked against the server's session table on
every call — the session table is the only proof of login.

- A session expires after **30 days without use**, and in any case **365
  days** after login. Each call counts as use.
- **Logout** revokes that session.
- **Changing your password** revokes every other session of your account;
  the session you changed it from stays open.
- **An admin password reset** and **deleting a user** revoke all of that
  user's sessions.
- Removing a user's admin flag takes effect on their next call.

Revocation applies everywhere at once: gRPC, the UI and MCP.

## Users and admins

A user can run templates, submit and edit tasks, and manage workflows,
steps, recruiters and workers. An admin can additionally manage users and
flavors, fork, delete and upgrade library modules, and prune the audit
trail.

## AI agents and MCP

The MCP server acts with exactly the rights of the account its session
belongs to — no more, and no less. Give each agent its own non-admin
account: that bounds what the agent can do, and it is how the
[audit trail](../usage/cli.md#audit) tells agents apart, since it records
an agent as its user plus the MCP client name.

## Workers

All workers share the worker token. It allows registering, taking tasks,
reporting task status and logs, worker events, upgrade information, and
fetching the rclone and Docker registry configuration that workers need to
move data and pull images. Every other call — submitting tasks, deploying
or deleting workers, changing workflows — needs a user session.

The worker token is a credential for the storage and registries it can
fetch configuration for: keep it as secret as those credentials.

## Tasks calling scitq: `scitq_auth`

A task with `scitq_auth: true` receives `SCITQ_SERVER` and `SCITQ_TOKEN`,
and the worker's `scitq` binary. The token is the task's own:

- it allows `scitq file copy` and `scitq file list` only;
- it stops working when the task leaves the active states (assigned
  through uploading);
- a retry is a new task with a new token.

`scitq file copy` runs rclone inside the task with the server's rclone
configuration, so a `scitq_auth` task sees the storage credentials. Enable
it only for tasks you trust with them.

Workers from before v1.0 hand `scitq_auth` tasks the worker token instead;
those tasks keep working, with the worker token's rights.
