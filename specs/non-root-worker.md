# Non-root worker execution

## Motivation

Workers currently run as root. This is primarily because Docker containers create files as root (or arbitrary UIDs), and the worker needs to read, move, and clean up those files after task execution. Running as root is a security concern, especially on permanent workers that persist across workflows.

## Proposed approach

Run the worker as a dedicated unprivileged user (`scitq`), member of the `docker` group, with a single targeted `sudo` command for file ownership reclamation.

### Setup

```sh
# Create dedicated user
useradd -r -m -s /bin/bash scitq
usermod -aG docker scitq

# Allow only the specific chown command, no password
echo 'scitq ALL=(root) NOPASSWD: /usr/bin/chown -R scitq\:scitq /var/lib/scitq/tasks/*' > /etc/sudoers.d/scitq
```

### Where root is needed today

| Operation | Why root | Non-root solution |
|---|---|---|
| Run Docker | Docker daemon socket | `docker` group membership |
| Read task output files | Docker creates as root/random UID | `sudo chown` after task completion |
| Clean up task directories | Same ownership issue | `sudo chown` first, then `rm -rf` |
| Install worker (first boot) | System packages, swap, disks | Keep install phase as root (cloud-init), then drop to `scitq` user |
| Bind to privileged ports | Not needed (worker doesn't listen) | N/A |

### Implementation

Only one code change needed in `client/client.go`: after task execution (both success and failure), before reading output or cleaning up:

```go
exec.Command("sudo", "chown", "-R", "scitq:scitq", taskDir).Run()
```

The systemd service unit changes from:

```ini
[Service]
ExecStart=/usr/local/bin/scitq-client ...
```

to:

```ini
[Service]
User=scitq
Group=scitq
ExecStart=/usr/local/bin/scitq-client ...
```

The install phase (cloud-init / `scitq-client -install`) still runs as root to set up the system, then starts the service as `scitq`.

### What about bare mode

Bare tasks (see `bare_execution.md`) don't use Docker, so they create files as the worker user directly. No `sudo chown` needed. Bare mode on a non-root worker is the cleanest combination.

### Risks and limitations

- The `sudo chown` adds a small overhead per task (negligible compared to Docker execution time).
- The sudoers rule must be precise — wildcarding `/var/lib/scitq/tasks/*` limits the scope but still allows ownership changes within that subtree.
- If the worker store path changes (e.g. custom `--store`), the sudoers rule must match.
- Container options like `--privileged` or `--pid=host` still give Docker tasks root-equivalent access on the host. This is a Docker limitation, not a scitq one.

### Priority

Low. This is a security hardening measure, not a functional requirement. Ephemeral cloud workers are destroyed after use, limiting the exposure window. Permanent workers benefit the most but are the minority of deployments.
