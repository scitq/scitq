# Compatibility and versioning

From 1.0, scitq follows [semantic versioning](https://semver.org): a release
number `MAJOR.MINOR.PATCH` tells you what an upgrade can change.

| Release | What changes |
|---|---|
| Patch (`1.4.2` → `1.4.3`) | Fixes only. |
| Minor (`1.4` → `1.5`) | Additions: new commands, flags, tools, fields, template keys. Everything that worked keeps working. |
| Major (`1.x` → `2.0`) | May remove or change what was deprecated during `1.x`. |

## What is stable

These interfaces keep working across all `1.x` releases. Each one is checked
in CI against the previous release.

| Interface | Stable | Checked by |
|---|---|---|
| gRPC API (`proto/taskqueue.proto`) | Services, messages, field numbers, types and JSON names. | `make proto-breaking` (`buf breaking`, WIRE_JSON rules) |
| YAML templates | Accepted keys per section, iterator keys, supported `format` versions. A template declaring a newer `format` is rejected with a message, never run with a different meaning. | `compat/python.txt` |
| Python DSL | Names exported by `from scitq2 import *`, and the parameters of those classes and functions. | `compat/python.txt` |
| MCP tools | Tool names, parameter names and types, which parameters are required. | `compat/mcp.txt` |
| CLI | Commands, flags, flag types, which flags are required, and the field names of `--json` output (they are the gRPC messages' JSON names). | `compat/cli.txt`, `make proto-breaking` |
| Configuration (`scitq.yaml`) | Key names, nesting and value types. | `compat/config.txt` |

Everything else may change in any release: the database schema, internal Go
packages, the human-readable CLI output (layout, emoji, wording), log lines,
worker-event messages and the UI.

## Deprecation

A deprecated command, flag, tool, field or key keeps working until the next
major release. Where scitq can detect its use, it prints a warning naming the
replacement. The release notes of the minor release that deprecates it say
so.

## Mixing versions

During an upgrade, server, workers and CLI briefly run different versions.

- A server works with workers and CLIs of the same major version, down to
  the previous minor release. A `1.5` server works with `1.4` and `1.5`
  workers and CLIs.
- Upgrade the server first, then the workers. A worker newer than its
  server is not supported.
- The CLI warns when its build differs from the server's. The warning is
  informative; any supported combination works.
- Templates always run with the server's own Python DSL, which is embedded
  in the server binary, and the MCP interface is served by the server, so
  neither depends on the client's version.

## Upgrading a server

Follow the checklist in [Upgrading](../install.md#upgrading). In short: back
up the database, upgrade the server, let its migrations run at startup, then
upgrade the workers (`scitq worker upgrade --all`).

Downgrading is a restore, not a reinstall. Migrations only go forward: once a
newer server has started, its database schema no longer matches the older
binary, and reinstalling that binary alone leaves a server that fails at
startup or misreads its data. To go back, stop the server, restore the
database backup taken before the upgrade, then reinstall the previous
binaries.

## For contributors

Run `make compat` before opening a pull request; CI runs the same checks.

- The `compat/*.txt` files record each public surface. They must match the
  code: after an addition, regenerate them and commit the result.
  - Go surfaces: `go test ./cli ./server ./server/config -run SurfaceRecorded -update-surface`
  - Python surface: `cd python && SCITQ_UPDATE_SURFACE=1 pytest tests/test_public_surface.py`
- Regenerating a file makes the local test pass after a removal too, but CI
  also compares the files with their state at the last release
  (`make surface-check`) and fails on any line that disappeared. To retire
  something within `1.x`, deprecate it and keep it working.
- In `proto/taskqueue.proto`, add fields with new numbers. When a major
  release removes a field, reserve its number and name.
