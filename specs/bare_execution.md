# Bare execution mode

## Motivation

scitq currently requires Docker for all task execution. This is the right default for production (isolation, reproducibility, dependency management), but it creates friction in two contexts:

1. **Integration tests**: Docker is the main source of test flakiness — image pulls, container name collisions, daemon load under parallel tests. Tasks like `echo hello` don't need Docker.

2. **Lightweight workloads**: some tasks are simple shell commands, Python scripts, or binaries already present on the worker. Docker overhead (container startup, layer resolution, mount setup) is wasted.

scitq v1 supported non-Docker execution with shell variables (`$INPUT`, `$OUTPUT`, `$TEMP`) pointing to task directories. This spec brings that back in a controlled way.

## Design

### Opt-in via container field

When `container` is set to `bare`, the task runs as a direct shell command on the worker, without Docker:

```yaml
steps:
  - name: process
    container: bare
    command: "cat $INPUT/data.csv | sort > $OUTPUT/sorted.csv"
```

Or in the DSL:

```python
step = workflow.Step(
    name="process",
    container="bare",
    command='cat $INPUT/data.csv | sort > $OUTPUT/sorted.csv',
)
```

Or via CLI:

```sh
scitq task create --container bare --command 'echo hello > $OUTPUT/result.txt'
```

### Environment variables

Bare tasks get the same directory structure as Docker tasks, exposed via environment variables instead of mount points:

| Docker mount | Bare env var | Description |
|---|---|---|
| `/input/` | `$INPUT` | Downloaded inputs |
| `/output/` | `$OUTPUT` | Task outputs (uploaded after execution) |
| `/resource/` | `$RESOURCE` | Shared resources (read-only) |
| `/tmp/` | `$TEMP` | Temporary working directory |
| `/scripts/` | `$SCRIPTS` | Script files (for long commands) |
| `/builtin/` | `$BUILTIN` | Built-in shell libraries |

Additionally, `$CPU`, `$THREADS`, and `$MEM` are set (same as Docker's `-e` flags).

### Execution

The client's `executeTask` function checks the container field:

```go
if task.Container == "bare" {
    // Set up environment and run directly
    cmd := exec.Command("sh", "-c", task.Command)
    cmd.Env = append(os.Environ(),
        "INPUT="+inputDir,
        "OUTPUT="+outputDir,
        "RESOURCE="+resourceDir,
        "TEMP="+tmpDir,
        "SCRIPTS="+scriptsDir,
        "BUILTIN="+builtinDir,
        "CPU="+cpuStr,
        "THREADS="+cpuStr,
        "MEM="+memStr,
    )
    cmd.Dir = tmpDir
    // ... stdout/stderr capture, same as Docker path
} else {
    // Existing Docker path
    cmd := exec.Command("docker", "run", "--rm", "--name", containerName, ...)
}
```

The download/upload/resource flow is identical — only the execution step changes. Inputs are downloaded to the same task directory structure, outputs are uploaded from the same path.

### Shell handling

- If `shell` is set (e.g. `bash`, `python`), use that as the interpreter: `exec.Command(shell, "-c", command)`
- If `shell` is not set, default to `sh -c`
- For `python` shell, the worker uses the system Python (not a venv)
- The `language:` YAML field maps to `shell` as usual

### Security considerations

Bare tasks run as the worker's OS user (root). This is no worse than Docker mode security-wise: since scitq accepts arbitrary `container_options` (including `-v` mounts), Docker tasks already have de facto full filesystem access. Bare mode is marginally more direct but equivalent in practice.

Workers can be configured to refuse bare commands via the client binary flag:

```sh
scitq-client -server ... -no-bare   # reject bare tasks (default: bare allowed)
```

When `-no-bare` is set, the worker rejects tasks with `container: bare` and reports an error. This allows permanent shared workers to opt out while ephemeral cloud workers accept bare tasks by default.

### Signal handling

Bare tasks use the same signal mechanism as Docker:

| Signal | Docker | Bare |
|---|---|---|
| K (kill) | `docker kill <container>` | `cmd.Process.Kill()` (SIGKILL) |
| T (stop) | `docker stop --time <grace> <container>` | `cmd.Process.Signal(SIGTERM)`, then SIGKILL after grace period |

The process handle is stored the same way as the Docker command handle.

### Integration test usage

Tests can use `bare` to avoid Docker entirely:

```go
taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
    Command:   "echo hello > $OUTPUT/result.txt",
    Container: "bare",
    Shell:     strPtr("sh"),
    Status:    "P",
})
```

This eliminates:
- Docker image pulls (no `alpine` needed)
- Container name collisions
- Docker daemon contention under parallel tests
- Orphaned containers after test failures

### What doesn't change

- Task lifecycle (P → A → C → D → O → R → U/V → S/F)
- Input downloading and output uploading
- Resource management
- Quality scoring (stdout/stderr capture is the same)
- Signal handling (kill/stop)
- Log streaming
- Retry logic
- Dependencies and workflow management

### What changes

Only `executeTask()` in `client/client.go` — a single `if task.Container == "bare"` branch that runs the command directly instead of via `docker run`. Everything before (download) and after (upload, status update) is identical.

## Implementation estimate

This is a small change:
- ~30 lines in `client/client.go` (the bare execution branch)
- ~5 lines for signal handling (process kill/stop without Docker)
- Client flag `-no-bare` (checked before execution, rejects bare tasks with an error)
- Test migration: change `Container: "alpine"` to `Container: "bare"` in integration tests

The download manager, upload manager, resource linking, log streaming, and status reporting are all container-agnostic — they work on directories, not on Docker mounts.
