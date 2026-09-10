# Configuration reference

## Environment-variable substitution

Any `${VAR}` reference in `scitq.yaml` is replaced with the value of the
corresponding environment variable at config-load time. Typical use:
keep secrets (rclone crypt passwords, cloud-provider access keys, JWT
secret, etc.) out of `scitq.yaml` and source them from a systemd
`EnvironmentFile`, a docker `--env-file`, or a Kubernetes `Secret` —
whichever fits your deployment.

Rules:

- **Brace form only.** `${VAR}` triggers substitution. Bare `$VAR` is left
  literal — so a `DBURL` like
  `postgres://scitq:p@ss$word@db.host/scitq` is **not** affected. This is
  deliberate, so adding `${...}` references to a long-lived config never
  retroactively breaks existing values with `$` in them.
- **Valid identifiers only.** What sits inside the braces must be a
  POSIX-style identifier (`[A-Za-z_][A-Za-z0-9_]*`). Anything else —
  `${X:default}`, `${X-default}`, `${X.Y}`, `${1BAD}`, `${}`, `${...` —
  is left literal and **not** substituted.
- **Unset variables expand to empty string.** No error, no warning. If
  scitq tries to use the empty value at runtime (e.g. an empty crypt
  password), the failure surfaces at that point.
- **Partial substitution is allowed.** Compose env values inside larger
  strings: `account_url: https://${AZURE_ACCOUNT}.blob.core.windows.net/`.

### Example: rclone crypt password from a systemd EnvironmentFile

`scitq.yaml`:

```yaml
rclone:
  azure_archive:
    type: azureblob
    account: <storage-account-name>
    key: ${SCITQ_AZURE_KEY}        # literal Azure access key — NOT obscured
  crypt_archive:
    type: crypt
    remote: azure_archive:cegat-archive
    password:  ${SCITQ_CRYPT_PASSWORD_OBSCURED}   # `rclone obscure <pw>` output
    password2: ${SCITQ_CRYPT_PASSWORD2_OBSCURED}  # `rclone obscure <pw2>` output
    filename_encryption: "off"
    directory_name_encryption: "false"
```

`/etc/scitq-secrets/env` (chmod `0400` owned by the scitq daemon user):

```
SCITQ_AZURE_KEY=<storage-account-access-key>
SCITQ_CRYPT_PASSWORD_OBSCURED=<output of `rclone obscure <plaintext>`>
SCITQ_CRYPT_PASSWORD2_OBSCURED=<output of `rclone obscure <plaintext2>`>
```

systemd unit `/etc/systemd/system/scitq.service` (under `[Service]`):

```ini
EnvironmentFile=-/etc/scitq-secrets/env
```

The leading `-` on the path makes the file optional — if it's absent
(e.g. on a dev machine that has no encrypted backend configured), the
service starts normally and the env vars resolve to empty. The
secret-using workflow will fail at use-time with a clear "wrong
password" error from rclone, rather than the service refusing to start.

After editing: `systemctl daemon-reload && systemctl restart scitq`.

## Settings reference

| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| Scitq |  | `scitq` |  |  | Scitq contains configuration parameters specific to the scitq server. |
|  | Port | `scitq.port` | `50051` | `int` | Port is the TCP port on which the scitq server listens for gRPC incoming connections. |
|  | DBURL | `scitq.db_url` | `postgres://localhost/scitq2?sslmode=disable` | `string` | DBURL is the database connection string used by scitq to connect to its PostgreSQL database. It should include the username, password, host, database name, and SSL mode. |
|  | MaxDBConcurrency | `scitq.max_db_concurrency` | `50` | `int` | MaxDBConcurrency limits the maximum number of concurrent database connections. |
|  | LogLevel | `scitq.log_level` | `info` | `string` | LogLevel sets the verbosity level of logging output. Common values include "debug", "info", "warn", and "error". |
|  | LogRoot | `scitq.log_root` | `/var/lib/scitq/tasks` | `string` | LogRoot specifies the root directory where remote task stdout/stderr files are stored. |
|  | ScriptRoot | `scitq.script_root` | `/var/lib/scitq/scripts` | `string` | ScriptRoot is the directory where (python) server-side scripts are located. Scripts run by scitq are expected to be found here. |
|  | ScriptVenv | `scitq.script_venv` | `/var/lib/scitq/python` | `string` | ScriptVenv specifies the path to the Python virtual environment used to run scripts. This isolates script dependencies from the system Python environment. Python venv creation and DSL installation in the venv is managed automatically by server. |
|  | ScriptRunnerUser | `scitq.script_runner_user` | `nobody` | `string` | ScriptRunnerUser is the system user account under which scripts are executed. Running scripts as a non-privileged user enhances security. |
|  | ClientBinaryPath | `scitq.client_binary_path` | `/usr/local/bin/scitq-client` | `string` | ClientBinaryPath is the filesystem path to the scitq client binary. This is used for automated client installation. |
|  | ClientDownloadToken | `scitq.client_download_token` | `` | `string` | ClientDownloadToken is a secret token used to authorize client binary downloads. If not set, a random token is generated at startup. |
|  | CertificateKey | `scitq.certificate_key` | `` | `string` | CertificateKey is the path or content of the TLS private key file for HTTPS. Required if you use your own certificates (e.g. Let's Encrypt) |
|  | CertificatePem | `scitq.certificate_pem` | `` | `string` | CertificatePem is the path or content of the TLS certificate file for HTTPS. Required if you use your own certificates. The Python DSL client auto-fetches this cert via `scitq cert` when `scitq` is on PATH; set `SCITQ_SSL_CERTIFICATE` manually otherwise |
|  | ServerName | `scitq.server_name` | `` | `string` | ServerName is the short name identifier for the server. |
|  | ServerFQDN | `scitq.server_fqdn` | `` | `string` | ServerFQDN is the fully qualified domain name of the server. |
|  | DockerRegistry | `scitq.docker_registry` | `` | `string` | DockerRegistry specifies the default container registry URL for pulling images. |
|  | DockerAuthentication | `scitq.docker_authentication` | `` | `string` | DockerAuthentication holds the authentication token or credentials for the default Docker registry. |
|  | DockerCredentials | `scitq.docker_credentials` | `` | `[]DockerCredential` | DockerCredentials contains multiple registry→secret pairs for authenticating to container registries. Used by clients to access private registries. |
|  | SwapProportion | `scitq.swap_proportion` | `0.1` | `float32` | SwapProportion defines the proportion of disk space dedicated to swap on worker automated deploy. |
|  | WorkerToken | `scitq.worker_token` | `` | `string` | WorkerToken is a secret token used to authenticate worker nodes. |
|  | JwtSecret | `scitq.jwt_secret` | `` | `string` | JwtSecret is the secret key used to sign JWT tokens. |
|  | RecruitmentInterval | `scitq.recruiter_interval` | `5` | `int` | RecruitmentInterval sets the interval in seconds for recruiting new workers. |
|  | IdleTimeout | `scitq.idle_timeout` | `300` | `int` | IdleTimeout defines the timeout in seconds after which idle workers are considered for shutdown. |
|  | NewWorkerIdleTimeout | `scitq.new_worker_idle_timeout` | `900` | `int` | NewWorkerIdleTimeout is the timeout in seconds for newly started workers before they are considered idle. |
|  | OfflineTimeout | `scitq.offline_timeout` | `30` | `int` | OfflineTimeout is the timeout in seconds after which offline workers are considered lost. |
|  | TaskDownloadTimeout | `scitq.task_download_timeout` | `600` | `int` | TaskDownloadTimeout is the timeout in seconds for task data downloads. |
|  | TaskExecutionTimeout | `scitq.task_execution_timeout` | `0` | `int` | TaskExecutionTimeout is the timeout in seconds for task execution. A value of 0 disables the timeout. |
|  | TaskUploadTimeout | `scitq.task_upload_timeout` | `600` | `int` | TaskUploadTimeout is the timeout in seconds for uploading task results. |
|  | ConsideredLostTimeout | `scitq.considered_lost_timeout` | `300` | `int` | ConsideredLostTimeout is the timeout in seconds after which a task is considered lost. |
|  | AdminUser | `scitq.admin_user` | `admin` | `string` | AdminUser is the username for the administrator account. |
|  | AdminHashedPassword | `scitq.admin_hashed_password` | `` | `string` | AdminHashedPassword is the hashed password for the administrator account. It can be generated by CLI : `scitq hashpassword MySuperPassword` |
|  | AdminEmail | `scitq.admin_email` | `` | `string` | AdminEmail is the email address of the administrator. |
|  | DisableHTTPS | `scitq.disable_https` | `false` | `bool` | DisableHTTPS disables HTTPS support when set to true. |
|  | DisableGRPCWeb | `scitq.disable_grpcweb` | `false` | `bool` | DisableGRPCWeb disables gRPC-Web support when set to true. Used for test only |
|  | HTTPSPort | `scitq.https_port` | `443` | `int` | HTTPSPort is the TCP port used for HTTPS connections. |
|  | GRPCDSLTimeout | `scitq.grpc_dsl_timeout` | `1800` | `int` | GRPCDSLTimeout is the timeout (in seconds) for DSL scripts which can take a long time to complete. |
| Providers |  | `providers` |  |  | Providers contains configurations for different cloud providers supported by scitq. Each provider can use multiple account, so you can have several config called Primary, Secondary etc. For OVH, use an Openstack account (that you can name OVH) see the example for details |
|  | Azure | `providers.azure` |  | See below | Azure cloud provider configs |
|  | Openstack | `providers.openstack` |  | See below | Openstack cloud provider configs |
|  | Fake | `providers.fake` |  | Used for tests | Fake cloud provider configs |
|  | Local | `providers.local` |  | Used for permanent worker (no recruit) | Local provider config |
| Rclone |  | `rclone` |  |  | Rclone holds configuration mappings for rclone integrations. Create your config using native rclone with `rclone config` then export the config to `scitq.yaml` with the CLI `scitq config import-rclone >> /etc/scitq.yaml` |

### AzureConfig (Providers.Azure map values)
| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| AzureConfig |  | `azure.<account>` |  |  |  |
|  | Name | `azure.<account>.-` | `` | `string` |  |
|  | DefaultRegion | `azure.<account>.default_region` | `` | `string` |  |
|  | SubscriptionID | `azure.<account>.subscription_id` | `` | `string` |  |
|  | ClientID | `azure.<account>.client_id` | `` | `string` |  |
|  | ClientSecret | `azure.<account>.client_secret` | `` | `string` |  |
|  | TenantID | `azure.<account>.tenant_id` | `` | `string` |  |
|  | UseSpot | `azure.<account>.use_spot` | `true` | `bool` |  |
|  | Username | `azure.<account>.username` | `ubuntu` | `string` | Default username for the VM, using OVH default |
|  | SSHPublicKey | `azure.<account>.ssh_public_key` | `~/.ssh/id_rsa.pub` | `string` |  |
|  | Image | `azure.<account>.image` | `` | `AzureImage` |  |
| Image |  | `azure.<account>.image` |  |  |  |
|  | Publisher | `azure.<account>.image.publisher` | `Canonical` | `string` |  |
|  | Offer | `azure.<account>.image.offer` | `UbuntuServer` | `string` |  |
|  | Sku | `azure.<account>.image.sku` | `24.04-LTS` | `string` |  |
|  | Version | `azure.<account>.image.version` | `latest` | `string` |  |
|  | Quotas | `azure.<account>.quotas` | `` | `map[string]Quota` | key: region (see Quota below) |
|  | Regions | `azure.<account>.regions` | `` | `[]string` |  |
|  | UpdatePeriodicity | `azure.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `azure.<account>.local_workspaces` | `` | `map[string]string` |  |

### AzureImage (AzureConfig.Image field)
| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| AzureImage |  | `azure.<account>.image` |  |  |  |
|  | Publisher | `azure.<account>.image.publisher` | `Canonical` | `string` |  |
|  | Offer | `azure.<account>.image.offer` | `UbuntuServer` | `string` |  |
|  | Sku | `azure.<account>.image.sku` | `24.04-LTS` | `string` |  |
|  | Version | `azure.<account>.image.version` | `latest` | `string` |  |

### OpenstackConfig (Providers.Openstack map values)
| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| OpenstackConfig |  | `openstack.<account>` |  |  |  |
|  | Name | `openstack.<account>.-` | `` | `string` |  |
|  | AuthURL | `openstack.<account>.auth_url` | `` | `string` |  |
|  | Username | `openstack.<account>.username` | `` | `string` |  |
|  | Password | `openstack.<account>.password` | `` | `string` |  |
|  | DomainName | `openstack.<account>.domain_name` | `` | `string` |  |
|  | DomainID | `openstack.<account>.domain_id` | `` | `string` |  |
|  | TenantName | `openstack.<account>.tenant_name` | `` | `string` |  |
|  | ProjectID | `openstack.<account>.project_id` | `` | `string` | Keystone project identifiers (either one can be used) |
|  | ProjectName | `openstack.<account>.project_name` | `` | `string` |  |
|  | UserDomainName | `openstack.<account>.user_domain_name` | `` | `string` | Domain scoping (Keystone v3) |
|  | ProjectDomainID | `openstack.<account>.project_domain_id` | `` | `string` |  |
|  | ApplicationCredentialID | `openstack.<account>.application_credential_id` | `` | `string` | Optional: prefer Application Credentials when provided (portable OpenStack) |
|  | ApplicationCredentialSecret | `openstack.<account>.application_credential_secret` | `` | `string` |  |
|  | Interface | `openstack.<account>.interface` | `` | `string` | Optional interface selection for service endpoints (public/internal/admin) |
|  | IdentityAPIVersion | `openstack.<account>.identity_api_version` | `3` | `int` | Optional: Keystone identity API version (default 3) |
|  | DefaultRegion | `openstack.<account>.region` | `` | `string` |  |
|  | ImageID | `openstack.<account>.image_id` | `` | `string` |  |
|  | FlavorID | `openstack.<account>.flavor_id` | `` | `string` |  |
|  | NetworkID | `openstack.<account>.network_id` | `` | `string` |  |
|  | ExtNetworkID | `openstack.<account>.ext_network_id` | `` | `string` |  |
|  | Quotas | `openstack.<account>.quotas` | `` | `map[string]Quota` | key: region (see Quota below) |
|  | Regions | `openstack.<account>.regions` | `` | `[]string` |  |
|  | Custom | `openstack.<account>.custom` | `` | `map[string]*ast.InterfaceType` | Vendor-specific custom settings |
|  | UpdatePeriodicity | `openstack.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `openstack.<account>.local_workspaces` | `` | `map[string]string` |  |
|  | Keypair | `openstack.<account>.keypair` | `` | `string` | Name of the keypair to use for SSH access |

### LocalConfig (Providers.Local map values)
| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| LocalConfig |  | `local.local` |  |  |  |
|  | Name | `local.local.-` | `` | `string` |  |
|  | DefaultRegion | `local.local.default_region` | `` | `string` |  |
|  | Regions | `local.local.regions` | `` | `[]string` |  |
|  | LocalWorkspaceRoots | `local.local.local_workspaces` | `` | `map[string]string` |  |

### Quota (per-region resource limits)

Quotas are defined per region within a provider. They control how many resources scitq is allowed to consume before it stops deploying new workers.

| Field | YAML key | Default | Type | Description |
|-------|----------|---------|------|-------------|
| MaxCPU | `cpu` | `0` | `int32` | Maximum vCPUs allowed in this region. Required. |
| MaxMemGB | `mem` | `0` | `float32` | Maximum memory in GB (optional, 0 = unlimited). |
| MaxInstances | `instances` | `0` | `int32` | Maximum number of VM instances (optional, 0 = unlimited). Prevents `PublicIPCountLimitReached` and similar per-instance Azure limits. |

Example:

```yaml
providers:
  azure:
    primary:
      quotas:
        swedencentral:
          cpu: 340
          instances: 20
```

The `instances` limit is also **learned from failures**: if scitq encounters an instance-count error (like Azure `PublicIPCountLimitReached`) during deployment, it automatically sets `MaxInstances` to the current instance count for that region. Further deploys are blocked until existing workers are deleted, freeing instance slots. This means even without configuring `instances` explicitly, scitq will stop hammering the Azure API after the first failure.

### Workspace and resource roots (`local_workspaces`, `local_resources`)

Each provider can declare per-region URIs for the **workspace root** (where
scitq stages task input/output between steps) and the **resource root**
(where module-level resources live, referenced by `{RESOURCE_ROOT}` in
module YAML). They're set on the provider config block:

```yaml
providers:
  azure:
    primary:
      local_workspaces:
        swedencentral: "azswed://rnd/workspace"
        westeurope:    "azwest://rnd/workspace"
        northeurope:   "aznorth://rnd/workspace"
      local_resources:
        swedencentral: "azswed://rnd/resource"
        westeurope:    "azwest://rnd/resource"
        northeurope:   "aznorth://rnd/resource"
  local:
    local:
      local_workspaces:
        "*": "s3://rnd/workspace"
      local_resources:
        "*": "s3://rnd/resource"
```

**Resolution order** (see `LocalConfig.GetWorkspaceRoot` /
`AzureConfig.GetWorkspaceRoot` / `OpenstackConfig.GetWorkspaceRoot`):

1. Exact match on the worker's region.
2. Fall back to the `"*"` wildcard key if defined.
3. Else `GetWorkspaceRoot` returns "not found" and the workflow's
   `client.get_workspace_root(...)` call raises `unknown provider` /
   `no workspace root for region`.

The `"*"` wildcard is convenient for providers with a single (or no real)
region — e.g. `local.local` typically has only the synthetic `local`
region, so a wildcard avoids needing to repeat the entry for every
permanent worker. Cloud providers with multiple regions usually want
explicit per-region entries so cross-region transfer fees are visible
(and avoidable).

## Notifications (user-facing)

scitq can send convenience notifications when a workflow reaches a
terminal state (Succeeded or Failed). This is deliberately **not** the
admin monitoring path — leaked workers, DB pool saturation, quota
exhaustion and other operational alerts surface through the
Prometheus `/metrics` endpoint (see [Monitoring](monitoring.md)) so
that Zabbix / Prometheus / Grafana own the alerting logic (silence,
escalate, correlate, page on-call).

The whole subsystem is best-effort: a failed webhook is logged and
the caller keeps going. Users who miss a workflow-done ping are
mildly annoyed; that's the whole failure surface.

Absent or empty `notifications:` block means "no notifications sent"
— the dispatcher becomes a no-op. Safe to leave unconfigured.

### Config shape

```yaml
notifications:
  # Optional. When true, every dispatched notification is also
  # written to the server log in addition to being routed to
  # configured channels. Useful as an audit trail or to smoke-test
  # rules without wiring a real backend. Default: false.
  always_log: false

  # Delivery targets. Each entry has a name (used in the route
  # table below and in log lines) plus a backend kind.
  channels:
    - name: gmt-alerts
      kind: zulip
      url: "https://gmt.zulipchat.com/api/v1/external/generic?api_key=${ZULIP_KEY}&stream=alerts&topic=scitq"
      # options are backend-specific string knobs (see per-backend
      # sections below). Unknown keys are ignored.
      options:
        topic: "workflows"

  # Event -> channels. One event can fan out to several channels;
  # one channel can subscribe to several events.
  routes:
    - event: workflow.terminal
      channels: [gmt-alerts]
```

### Backends

Four concrete kinds ship out of the box. Others can be added by
implementing the `Notifier` interface in `server/notifications/`.

#### `zulip` — Zulip incoming webhook

Native support for Zulip's "Incoming webhook (generic)" integration.
Set `url` to the URL Zulip hands out on the integration page (it
already carries `api_key`, `stream`, and `topic` as query
parameters). No other config is required.

```yaml
- name: gmt-alerts
  kind: zulip
  url: "https://gmt.zulipchat.com/api/v1/external/generic?api_key=${ZULIP_KEY}&stream=alerts&topic=scitq"
  options:
    # Optional. Overrides the URL's default topic per-channel. Handy
    # when one integration URL covers several events and each event
    # wants its own thread.
    topic: "workflows"
```

Message format: the notification's `Title` and `Body` are rendered as
`**<title>**` on the first line, then a blank line, then `<body>`.
Zulip's Markdown renderer bolds the title, so it stands out in the
stream without any extra widget.

#### `zulip-dm` — Zulip direct message to the workflow owner

The right backend for per-user personal notifications ("your
workflow finished"). Unlike `zulip` above (which POSTs to a stream
via the incoming-webhook endpoint), this backend hits Zulip's
regular `messages` API and sends a private message. The recipient
address is not baked into the channel — it's read from each
message's `Meta["run_by_email"]` at Send time, so ONE channel
serves every user whose scitq account has an email set.

Requires a Zulip bot of type **"Generic bot"** (not "Incoming
webhook"). Incoming-webhook bots are limited to the
`external/generic` endpoint and cannot send DMs.

```yaml
- name: gmt-zulip-dm
  kind: zulip-dm
  url: "https://gmt.zulipchat.com"          # Zulip realm root, no path
  options:
    bot_email: "scitq-bot@gmt.zulipchat.com"
    api_key: ${ZULIP_API_KEY}
    # recipient_meta_key defaults to "run_by_email". Override if a
    # future event carries the target under a different key.
    recipient_meta_key: "run_by_email"
```

For the `workflow.terminal` event, `run_by_email` is populated
automatically from the workflow's owning `scitq_user.email` (via
`workflow.created_by` — migration 000036). Users with `email IS
NULL` in the DB receive nothing (the backend logs `dropped (no
run_by_email in message meta)` and moves on — not an error).

Message format is the same as the `zulip` backend: `**<title>**`,
blank line, `<body>` — Zulip's Markdown renderer bolds the title
so the subject line stands out at the top of the DM view.

#### `webhook` — generic HTTP POST with a templated body

Fits Slack, Discord, ntfy.sh, or any custom endpoint that accepts
JSON or plain text. The body is a Go
[`text/template`](https://pkg.go.dev/text/template) with access to
the notification's fields; the default template emits a compact JSON
object that most generic hook consumers can parse without extra
mapping.

Available template variables:

- `{{.Event}}` — the event routing key (e.g. `workflow.terminal`)
- `{{.Severity}}` — `info` / `warn` / `crit`
- `{{.Title}}` — short title (e.g. `Workflow "hermes.PRJNA..." succeeded (#123)`)
- `{{.Body}}` — one-line summary (e.g. `status=S tasks=42 succeeded=42 failed=0`)
- `{{.Meta.workflow_id}}`, `{{.Meta.workflow_name}}`,
  `{{.Meta.status}}`, `{{.Meta.total_tasks}}`,
  `{{.Meta.succeeded_tasks}}`, `{{.Meta.failed_tasks}}`,
  `{{.Meta.run_by}}` — per-event context
- `{{toJSON <value>}}` — JSON-quote a string or serialise a map
  (needed to keep JSON output safe when a title contains quotes)

```yaml
# Slack incoming-webhook
- name: slack-alerts
  kind: webhook
  url: "https://hooks.slack.com/services/T.../B.../..."
  options:
    template: '{"text": {{toJSON .Title}} }'

# Discord webhook
- name: discord-alerts
  kind: webhook
  url: "https://discord.com/api/webhooks/.../..."
  options:
    template: '{"content": {{toJSON .Title}} }'

# ntfy.sh — plain text body, headers carry the title
- name: ntfy-alerts
  kind: webhook
  url: "https://ntfy.sh/my-scitq-topic"
  options:
    template: "{{.Body}}"
    content_type: "text/plain"
  headers:
    Title: "scitq"
    Tags: "workflow"

# Custom internal endpoint with JWT auth
- name: internal-events
  kind: webhook
  url: "https://events.internal/scitq"
  headers:
    Authorization: "Bearer ${INTERNAL_EVENTS_TOKEN}"
  # options.template omitted → default JSON envelope
  #   {"event":"...","severity":"...","title":"...","body":"...","meta":{...}}
```

All `options` keys are string; all `headers` are appended verbatim to
every request. `method` (default `POST`) and `content_type` (default
`application/json`) are the two other knobs.

#### `log` — server log

Writes the notification to the standard server log
(`journalctl -u scitq.service` on a systemd install). Always
available with zero external dependencies — useful as a smoke test
channel, or as a fallback "notify me on the console" for a lab
operator without a separate integration.

```yaml
- name: console
  kind: log
```

### Events

Currently one event fires from the server:

| Event                 | When                                            | Payload keys                                                                                                                     |
|-----------------------|-------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------|
| `workflow.terminal`   | Workflow transitions to `S` (succeeded) or `F` (failed) | `workflow_id`, `workflow_name`, `status`, `total_tasks`, `succeeded_tasks`, `failed_tasks`, `run_by`, `run_by_email` (when set)  |

More events (workflow entered Debug, template run failed to compile,
long-running step exceeded a threshold, …) can be added in
`server/notifications/notifications.go` — one enum entry per event,
one `Emit()` call at the site.

### End-to-end example

Route successful runs to a low-priority stream, and failed runs to a
higher-signal one:

```yaml
notifications:
  channels:
    - name: workflows-info
      kind: zulip
      url: "https://gmt.zulipchat.com/api/v1/external/generic?api_key=${ZULIP_KEY}&stream=scitq-info"
    - name: workflows-alert
      kind: zulip
      url: "https://gmt.zulipchat.com/api/v1/external/generic?api_key=${ZULIP_KEY}&stream=scitq-alerts"
    - name: audit
      kind: log
  routes:
    - event: workflow.terminal
      channels: [workflows-info, workflows-alert, audit]
```

For v1 the route table dispatches every matching event to every
listed channel — filtering by status (S vs F) or severity is left to
the receiving side (Zulip stream muting, Slack channel routing) or to
a future `match:` clause on `NotificationRoute`.

### What is NOT covered here (yet)

- **Per-user route resolution beyond email lookup.** The `zulip-dm`
  backend uses `scitq_user.email` as the recipient address. If a
  future integration needs a different per-user identifier (Slack
  user ID, Discord snowflake, Matrix handle, ...), the `scitq_user`
  table would need one more column and the meta-building code needs
  one more field. Not hard; not shipped.
- **Retries / dedup / rate limiting.** Best-effort delivery only. A
  flapping event source (which we don't have today) would need
  per-channel `min_interval` before it becomes an issue.
- **Admin alerts (worker leaked, quota exhausted, ...).** Those are
  metrics + Zabbix triggers, not notifications. See
  [Monitoring](monitoring.md).
