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

## Tips for AI agents

- **Always use `--json`** for parseable output. Without it, the CLI outputs human-readable text with emojis.
- **Use `template detail --json`** to discover parameters before running a template. The `param_json` field contains the full schema.
- **Use `--no-recruiters`** for safe testing. This creates the workflow without deploying cloud workers.
- **Check task status** by filtering: `task list --workflow <id> --status F --json` to find failures.
- **Read stderr for errors**: `task stderr --id <id>` gives the task's error output, which usually contains the root cause.

For the full CLI reference, see the [CLI documentation](cli.md). For details on the `--server`, `--token`, and `--json` flags, see [Global flags](cli.md#global-flags).

## Roadmap: MCP server

A dedicated [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server for scitq is planned. This will expose scitq operations as native tools that AI agents can call directly — no shell commands, no JSON parsing, no token management. The MCP server will hold the connection state internally and present typed operations like `list_templates`, `run_template`, `get_task_status`, and `download_results`.

Until then, the CLI with `--json` provides a fully functional integration path.
