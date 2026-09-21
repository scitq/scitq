# Configuration reference

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
|  | CliBinaryPath | `scitq.cli_binary_path` | `/usr/local/bin/scitq` | `string` | CliBinaryPath is the filesystem path to the scitq CLI binary. Served from `/scitq-cli` so worker bootstrap scripts (and any task using `task_spec.scitq_auth: true`) can keep their CLI in sync with the rest of the deployment. Must be a fully-static build (`CGO_ENABLED=0`) so it runs inside alpine/distroless containers when bind-mounted — see `make build-cli`. |
|  | ClientDownloadToken | `scitq.client_download_token` | `` | `string` | ClientDownloadToken is a secret token used to authorize client binary downloads. If not set, a random token is generated at startup. |
|  | CertificateKey | `scitq.certificate_key` | `` | `string` | CertificateKey is the path or content of the TLS private key file for HTTPS. Required if you use your own certificates |
|  | CertificatePem | `scitq.certificate_pem` | `` | `string` | CertificatePem is the path or content of the TLS certificate file for HTTPS. Required if you use your own certificates |
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
|  | OfflineTimeout | `scitq.offline_timeout` | `600` | `int` | OfflineTimeout is the number of seconds without a worker ping after which the server flips the worker's status to O (offline) AND reclaims its assigned/accepted/downloading tasks (resets them to P so other workers can pick them up). 10 minutes is the default: long enough to tolerate normal network hiccups, short-lived upgrades, and worker restarts, short enough that a truly dead worker doesn't strand its share of the queue for hours. Set lower (e.g. 60) for latency-sensitive fleets where task reclaim should be aggressive; set higher (e.g. 1800) for flaky networks where transient ping loss is common. |
|  | WorkerStatsRetentionHours | `scitq.worker_stats_retention_hours` | `168` | `int` | WorkerStatsRetentionHours controls how long historical per-worker stats samples (worker_stats_history table) are kept. Each ping writes one row with the worker's current and peak-since-last-ping cpu/mem/iowait, so a completed workflow can be inspected days later ("what did this workflow actually need?"). 0 disables the feature entirely — no rows are written and the sweep goroutine does not run. Default 168 (7 days) comfortably covers a Friday workflow inspected on Monday. |
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
|  | HTTPPort | `scitq.http_port` | `0` | `int` | HTTPPort is the TCP port used for plain HTTP (MCP, API endpoints, the worker-binary download endpoint). Only consulted when HTTPS is disabled (e.g. integration tests). Default 0 means "derive from Port + 1", which is what production deployments rely on. The override exists for tests that need to reserve two independent free ports (without the +1 assumption that otherwise races with parallel tests reserving consecutive numbers). |
|  | GRPCDSLTimeout | `scitq.grpc_dsl_timeout` | `1800` | `int` | GRPCDSLTimeout is the timeout (in seconds) for DSL scripts which can take a long time to complete. |
|  | WorkerRetention | `scitq.worker_retention` | `30` | `int` | WorkerRetention is the number of days to retain soft-deleted workers before pruning. |
|  | ModulesRoot | `scitq.modules_root` | `/var/lib/scitq/modules` | `string` | ModulesRoot is the directory where YAML module files are stored as the canonical source of truth. Each module row in the `module` table points at `{ModulesRoot}/<path>/<version>.yaml`. Admins can inspect / grep / rsync / git-track this directory; losing the DB is recoverable by reindexing the tree at startup. See specs/module_library.md. |
|  | AutoupgradeModules | `scitq.autoupgrade_modules` | `true` | `bool` | AutoupgradeModules, when true (default), triggers the server to run `module upgrade --apply` semantics at every startup so bundled YAML modules shipped in the scitq2_modules Python package appear in the server-side module library without an explicit admin step. Set to false for frozen-library environments (audit-sensitive sites, CI) where every module change must be gated through a manual review. See specs/module_library.md. |
|  | AutoupgradeExclude | `scitq.autoupgrade_exclude` | `` | `[]string` | AutoupgradeExclude is a list of glob-style path patterns that the auto-upgrade step will skip. `*` matches a single path segment, `**` matches any number of segments; everything else is literal. Examples: - "metagenomics/*"     # skip every direct child of the namespace - "genomics/multiqc"   # skip exactly this module - "internal/**"        # skip every module under internal/ recursively Excluded modules are NOT reinstated on subsequent startups; does not retroactively delete rows already in the library (use `scitq module delete` for that, then add to this list to prevent reinstatement). |
| Providers |  | `providers` |  |  | Providers contains configurations for different cloud providers supported by scitq. Each provider can use multiple account, so you can have several config called Primary, Secondary etc. For OVH, use an Openstack account (that you can name OVH) see the example for details |
|  | Azure | `providers.azure` |  | See below | Azure cloud provider configs |
|  | Openstack | `providers.openstack` |  | See below | Openstack cloud provider configs |
|  | Fake | `providers.fake` |  | Used for tests | Fake cloud provider configs |
|  | Local | `providers.local` |  | Used for permanent worker (no recruit) | Local provider config |
| Rclone |  | `rclone` |  |  | Rclone holds configuration mappings for rclone integrations. Create your config using native rclone with `rclone config` then export the config to `scitq.yaml` with the CLI `scitq config import-rclone >> /etc/scitq.yaml` |
| Notifications |  | `notifications` |  |  | Notifications configures user-facing convenience notifications (workflow completion, ...). NOT the admin monitoring path — leaked workers etc. surface via server/metrics for Zabbix / Prometheus / Grafana consumption. See server/notifications. |
|  | AlwaysLog | `notifications.always_log` | `false` | `bool` | AlwaysLog writes every dispatched notification to the server log in addition to routing to configured channels. Useful as an audit trail or to smoke-test rules without wiring a real backend. |
|  | Channels | `notifications.channels` | `` | `[]NotificationChannel` | Channels declares the delivery targets. See NotificationChannel for the shape. |
|  | Routes | `notifications.routes` | `` | `[]NotificationRoute` | Routes maps events to channel names. One event can fan out to several channels; one channel can subscribe to several events. |

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
|  | GPUImage | `azure.<account>.gpu_image` | `` | `AzureGPUImage` | Image booted for workers recruited on a has_gpu=true flavor (Standard_N* on Azure). Default: Microsoft's "Ubuntu HPC 22.04" image — free on the marketplace, ships NVIDIA drivers + CUDA runtime + nvidia-container-toolkit + docker preconfigured for `--gpus all`. cloud-init then just installs scitq-client as usual. Override per-field to point at a different image (e.g. NVIDIA's own GPU-Optimized VMI or a custom baked one). |
| GPUImage |  | `azure.<account>.gpu_image` |  |  |  |
|  | Publisher | `azure.<account>.gpu_image.publisher` | `microsoft-dsvm` | `string` |  |
|  | Offer | `azure.<account>.gpu_image.offer` | `ubuntu-hpc` | `string` |  |
|  | Sku | `azure.<account>.gpu_image.sku` | `2204` | `string` |  |
|  | Version | `azure.<account>.gpu_image.version` | `latest` | `string` |  |
|  | Quotas | `azure.<account>.quotas` | `` | `map[string]Quota` | key: region |
|  | Regions | `azure.<account>.regions` | `` | `[]string` |  |
|  | UpdatePeriodicity | `azure.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `azure.<account>.local_workspaces` | `` | `map[string]string` | key: region (or "*" wildcard); value: workspace URI for that region |
|  | LocalResourceRoots | `azure.<account>.local_resources` | `` | `map[string]string` | key: region (or "*" wildcard); value: resource-root URI for that region |
|  | FlavorIncludePatterns | `azure.<account>.flavor_include_patterns` | `` | `[]string` | Regex lists applied at flavor-sync time. Logic: include first (default = include all), then exclude (default = exclude none). A flavor is synced into the catalog iff it matches at least one include pattern (or include is empty) AND matches no exclude pattern. Lets operators take entire VM families (e.g. confidential `_cc_v\d+$`) out of recruitment without having to disable each flavor individually after every Azure catalog update. |
|  | FlavorExcludePatterns | `azure.<account>.flavor_exclude_patterns` | `` | `[]string` |  |

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
|  | GPUImageID | `openstack.<account>.gpu_image_id` | `NVIDIA GPU Cloud (NGC)` | `string` | Image (name or UUID) booted for workers recruited on a has_gpu=true flavor. Default: OVH's "NVIDIA GPU Cloud (NGC)" — Ubuntu 22.04 with NVIDIA drivers + nvidia-container-toolkit + docker preconfigured for `--gpus all`. Override per environment if your OpenStack catalog uses a different name; `openstack image list --tag gpu` lists candidates. |
|  | FlavorID | `openstack.<account>.flavor_id` | `` | `string` |  |
|  | NetworkID | `openstack.<account>.network_id` | `` | `string` |  |
|  | ExtNetworkID | `openstack.<account>.ext_network_id` | `` | `string` |  |
|  | Quotas | `openstack.<account>.quotas` | `` | `map[string]Quota` | key: region |
|  | Regions | `openstack.<account>.regions` | `` | `[]string` |  |
|  | Custom | `openstack.<account>.custom` | `` | `map[string]*ast.InterfaceType` | Vendor-specific custom settings |
|  | UpdatePeriodicity | `openstack.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `openstack.<account>.local_workspaces` | `` | `map[string]string` | key: region (or "*" wildcard); value: workspace URI for that region |
|  | LocalResourceRoots | `openstack.<account>.local_resources` | `` | `map[string]string` | key: region (or "*" wildcard); value: resource-root URI for that region |
|  | Keypair | `openstack.<account>.keypair` | `` | `string` | Name of the keypair to use for SSH access |
|  | FlavorIncludePatterns | `openstack.<account>.flavor_include_patterns` | `` | `[]string` | See AzureConfig.FlavorIncludePatterns / FlavorExcludePatterns — same semantics for OVH/Openstack catalogs. |
|  | FlavorExcludePatterns | `openstack.<account>.flavor_exclude_patterns` | `` | `[]string` |  |

### LocalConfig (Providers.Local map values)
| Section | Field | YAML key | Default | Type | Description |
|---------|-------|-----------|----------|------|-------------|
| LocalConfig |  | `local.local` |  |  |  |
|  | Name | `local.local.-` | `` | `string` |  |
|  | DefaultRegion | `local.local.default_region` | `` | `string` |  |
|  | Regions | `local.local.regions` | `` | `[]string` |  |
|  | LocalWorkspaceRoots | `local.local.local_workspaces` | `` | `map[string]string` | key: region (or "*" wildcard); value: workspace URI for that region |
|  | LocalResourceRoots | `local.local.local_resources` | `` | `map[string]string` | key: region (or "*" wildcard); value: resource-root URI for that region |
