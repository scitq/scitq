# AI agent integration

scitq can be operated by AI agents (Claude, GPT, etc.) through its CLI with `--json` output and explicit `--server`/`--token` flags — no environment variables or interactive prompts required.

## Authentication

```sh
# Get a token (returns JSON with "token" field)
TOKEN=$(scitq login --user admin --password mypass --server myserver:443 --json | jq -r .token)

# All subsequent commands use --server and --token explicitly
scitq template list --server myserver:443 --token $TOKEN --json
```

The token is a JWT valid for 24 hours. Store it for the duration of your session.

### TLS certificate

The Python DSL client auto-fetches the server's TLS certificate via `scitq cert` if the `scitq` binary is on PATH. If not, set it manually:

```sh
export SCITQ_SSL_CERTIFICATE=$(scitq cert --server myserver:50051)
```

## Common workflows

### List and inspect templates

```sh
# List all templates
scitq template list --server $S --token $T --json

# Get template details (including parameter schema)
scitq template detail --name biomscope --server $S --token $T --json
```

The detail output includes a `param_json` field with the full parameter schema: names, types, defaults, choices, and help text. Use this to build valid `--param` strings.

### Run a template

```sh
# Run with parameters
scitq template run --name biomscope \
  --param 'bioproject=PRJEB6070,location=openstack.ovh:GRA11,depth=2x20M' \
  --server $S --token $T --json

# Dry run (create workflow without deploying workers)
scitq template run --name biomscope \
  --param 'bioproject=PRJEB6070,location=openstack.ovh:GRA11' \
  --no-recruiters --server $S --token $T --json
```

The JSON output includes `template_run_id`, `workflow_id`, and `status`.

### Monitor progress

```sh
# List workflows
scitq workflow list --server $S --token $T --json

# List tasks for a workflow (filter by step)
scitq task list --workflow 184 --server $S --token $T --json

# Get task stdout/stderr
scitq task stdout --id 12345 --server $S --token $T
scitq task stderr --id 12345 --server $S --token $T
```

### Manage modules

```sh
# List private modules on the server
scitq module list --server $S --token $T --json

# Upload a module
scitq module upload --path modules/my_step.yaml --server $S --token $T

# Download a module for inspection
scitq module download --name my_step.yaml --server $S --token $T
```

## JSON output

All list and detail commands support `--json`. The output is structured JSON printed to stdout, suitable for parsing with `jq` or any JSON library. Human-readable messages (emojis, tables) are suppressed in JSON mode.

| Command | JSON output |
|---|---|
| `login --json` | `{"token": "eyJ..."}` |
| `template list --json` | Array of template objects |
| `template detail --json` | Single template with `param_json` |
| `template run --json` | Template run result with status |
| `task list --json` | Array of task objects |
| `workflow list --json` | Array of workflow objects |
| `module list --json` | Array of module filenames |

### Fix broken commands

When tasks fail due to a command error, you can edit and retry without recreating the workflow:

```sh
# Edit a single task's command and retry it
scitq task edit --id 12345 --command "fixed_command ..." --server $S --token $T --json

# Find/replace across all failed tasks in a step
scitq step edit --id 456 --find "old_path" --replace "new_path" --server $S --token $T --json

# Regexp replace
scitq step edit --id 456 --find "bowtie2 -k \d+" --replace "bowtie2 -k 200" --regexp --server $S --token $T --json
```

## Tips for AI agents

- **Always use `--json`** for parseable output. Without it, the CLI outputs human-readable text with emojis.
- **Use `template detail --json`** to discover parameters before running a template. The `param_json` field contains the full schema.
- **Use `--no-recruiters`** for safe testing. This creates the workflow without deploying cloud workers.
- **Check task status** by filtering: `task list --workflow <id> --status F --json` to find failures.
- **Read stderr for errors**: `task stderr --id <id>` gives the task's error output, which usually contains the root cause.
- **Fix and retry**: use `task edit` to fix a single task, or `step edit` to fix all failed tasks in a step with find/replace.
- **Kill stuck tasks**: use `task kill --id <id>` (SIGKILL) or `task stop --id <id>` (SIGTERM, graceful) to terminate a running task's container.
- **Task durations**: `task list --json` includes `download_duration`, `run_duration`, and `upload_duration` (in seconds) for completed tasks, and `run_start_time` (epoch) for running tasks.
- **Quality scores**: steps can define quality extraction via regex. `task list --json` includes `quality_score` and `quality_vars` for tasks whose step has a quality definition.

For the full CLI reference, see the [CLI documentation](cli.md). For details on the `--server`, `--token`, and `--json` flags, see [Global flags](cli.md#global-flags).

## MCP server

scitq includes a built-in [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server, accessible at `https://<server>/mcp`. This exposes scitq operations as native tools that AI agents (Claude, etc.) can call directly via the Streamable HTTP transport — no shell commands, no JSON parsing.

### Endpoint

```
POST https://<server>/mcp
```

The endpoint accepts JSON-RPC 2.0 messages per the MCP Streamable HTTP specification.

### Available tools

| Tool | Description |
|---|---|
| **Auth** | |
| `login` | Authenticate with username/password. Must be called first (unless using Bearer token). |
| **Templates** | |
| `list_templates` | List available workflow templates |
| `template_detail` | Get template metadata and parameter schema |
| `run_template` | Run a template with parameters |
| `upload_template` | Upload a template script (YAML or Python) |
| `download_template` | Download template source code |
| `list_template_runs` | List template execution runs |
| **Modules** | |
| `list_modules` | List YAML modules in the library (flat `path@version`, plus structured `entries` with origin / description) |
| `upload_module` | Upload a YAML module (path may contain slashes; `version:` field required in the YAML) |
| `download_module` | Download a module by `path`, `path@version`, or `path@latest` |
| `module_origin` | Show provenance of a module version: origin (`bundled`/`local`/`forked`), content SHA, uploader, fork-outdated flag |
| `fork_module` | Admin: clone a module row to a new local version |
| `upgrade_modules` | Admin: seed/update bundled modules from the installed scitq2 package (`apply=true` to commit) |
| **Workflows** | |
| `list_workflows` | List workflows |
| `update_workflow_status` | Pause, resume, or debug a workflow |
| `delete_workflow` | Delete a workflow and all its tasks |
| **Tasks** | |
| `list_tasks` | List tasks with filters (workflow_id, status, limit) |
| `task_logs` | Get stdout and stderr for a task |
| `task_status_counts` | Get task counts per status |
| `retry_task` | Retry a failed task |
| `force_run_task` | Force a waiting task to pending (bypass dependencies) |
| `kill_task` | Send a kill signal (SIGKILL) to a running task |
| `stop_task` | Send a graceful stop (SIGTERM) to a running task |
| `edit_and_retry_task` | Edit a task's command and retry it |
| `edit_step_command` | Find/replace in all failed tasks of a step and retry them |
| **Workers** | |
| `list_workers` | List deployed workers |
| `deploy_worker` | Deploy new worker instances (by provider/flavor name) |
| `delete_worker` | Delete a worker |
| `update_worker` | Update worker settings (concurrency, step, permanent, scope) |
| **Steps** | |
| `list_steps` | List steps (optionally by workflow) |
| **Recruiters** | |
| `list_recruiters` | List recruiters (optionally by step) |
| `create_recruiter` | Create a recruiter with protofilter, concurrency, max_workers |
| `update_recruiter` | Update a recruiter's settings |
| `delete_recruiter` | Delete a recruiter |
| **Flavors** | |
| `list_flavors` | List instance flavors with filter (e.g. cpu>=8:mem>=30) |
| **Files** | |
| `file_list` | List files at a remote URI |
| **Events** | |
| `list_worker_events` | List worker lifecycle events (install, errors) |
| `prune_worker_events` | Delete old worker events |

### Authentication

Two options:

**Option 1: Bearer token** (recommended for agents that already have a token)

Pass the JWT from `scitq login` in the `Authorization` header during `initialize`:

```
POST /mcp
Authorization: Bearer eyJ...
Content-Type: application/json

{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {...}}
```

The session starts pre-authenticated — no need to call the `login` tool.

**Option 2: Login tool** (for agents without a pre-existing token)

1. Send `initialize` request → server returns `Mcp-Session-Id`
2. Call `login` tool with username/password → session is authenticated
3. Call other tools — session token is reused automatically

### Server configuration

The MCP endpoint is enabled automatically on the HTTPS server. No additional configuration is needed. The endpoint is at `https://<server_fqdn>/mcp`.

### Claude Code integration

Add the scitq MCP server to Claude Code so tools appear natively:

```bash
# Get a token first
TOKEN=$(scitq login --server myserver:50051 --json | jq -r .token)

# Register the MCP server with Bearer token for pre-authentication
claude mcp add --transport http scitq https://myserver/mcp \
  --header "Authorization: Bearer $TOKEN"
```

Verify it's configured:

```bash
claude mcp list          # should show "scitq"
```

Inside Claude Code, use `/mcp` to see the available tools. After that, scitq tools (`list_flavors`, `list_templates`, `run_template`, `list_tasks`, etc.) are available as native tool calls — no shell commands needed.

To register multiple servers (e.g. production and testing):

```bash
claude mcp add --transport http scitq-prod https://prod.example.com/mcp \
  --header "Authorization: Bearer $PROD_TOKEN"

claude mcp add --transport http scitq-test https://test.example.com/mcp \
  --header "Authorization: Bearer $TEST_TOKEN"
```

To remove a server:

```bash
claude mcp remove scitq
```

### CLI alternative

For environments where MCP is not available, the CLI with `--json` provides equivalent functionality. See sections above.
