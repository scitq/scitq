# Configuration reference

| Field | YAML key | Default | Type | Description |
|-------|-----------|----------|------|-------------|
| Scitq |  | `scitq` |  |  | Scitq contains configuration parameters specific to the scitq server. |
|  | Port | `scitq.port` | `50051` | `int` | Port is the TCP port on which the scitq server listens for gRPC incoming connections. |
|  | DBURL | `scitq.db_url` | `postgres://localhost/scitq2?sslmode=disable` | `string` | DBURL is the database connection string used by scitq to connect to its PostgreSQL database. It should include the username, password, host, database name, and SSL mode. |
|  | MaxDBConcurrency | `scitq.max_db_concurrency` | `50` | `int` | MaxDBConcurrency limits the maximum number of concurrent database connections. |
|  | LogLevel | `scitq.log_level` | `info` | `string` | LogLevel sets the verbosity level of logging output. Common values include "debug", "info", "warn", and "error". |
|  | LogRoot | `scitq.log_root` | `log` | `string` | LogRoot specifies the root directory where remote task stdout/stderr files are stored. |
|  | ScriptRoot | `scitq.script_root` | `scripts` | `string` | ScriptRoot is the directory where (python) server-side scripts are located. Scripts run by scitq are expected to be found here. |
|  | ScriptVenv | `scitq.script_venv` | `/var/lib/scitq/python` | `string` | ScriptVenv specifies the path to the Python virtual environment used to run scripts. This isolates script dependencies from the system Python environment. Python venv creation and DSL installation in the venv is managed automatically by server. |
|  | ScriptRunnerUser | `scitq.script_runner_user` | `nobody` | `string` | ScriptRunnerUser is the system user account under which scripts are executed. Running scripts as a non-privileged user enhances security. |
|  | ClientBinaryPath | `scitq.client_binary_path` | `/usr/local/bin/scitq-client` | `string` | ClientBinaryPath is the filesystem path to the scitq client binary. This is used for automated client installation. |
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
| Providers |  | `providers` |  |  | Providers contains configurations for different cloud providers supported by scitq. Each provider can use multiple account, so you can have several config called Primary, Secondary etc. For OVH, use an Openstack account (that you can name OVH) see the example for details |
|  | Azure | `providers.azure` |  | See below | Azure cloud provider configs |
|  | Openstack | `providers.openstack` |  | See below | Openstack cloud provider configs |
|  | Fake | `providers.fake` |  | Used for tests | Fake cloud provider configs |
| Rclone |  | `rclone` |  |  | Rclone holds configuration mappings for rclone integrations. Create your config using native rclone with `rclone config` then export the config to `scitq.yaml` with the CLI `scitq config import-rclone >> /etc/scitq.yaml` |

### AzureConfig (Providers.Azure map values)
| Field | YAML key | Default | Type | Description |
|-------|-----------|----------|------|-------------|
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
|  | Quotas | `azure.<account>.quotas` | `` | `map[string]Quota` | key: region |
|  | Regions | `azure.<account>.regions` | `` | `[]string` |  |
|  | UpdatePeriodicity | `azure.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `azure.<account>.local_workspaces` | `` | `map[string]string` |  |

### AzureImage (AzureConfig.Image field)
| Field | YAML key | Default | Type | Description |
|-------|-----------|----------|------|-------------|
| AzureImage |  | `azure.<account>.image` |  |  |  |
|  | Publisher | `azure.<account>.image.publisher` | `Canonical` | `string` |  |
|  | Offer | `azure.<account>.image.offer` | `UbuntuServer` | `string` |  |
|  | Sku | `azure.<account>.image.sku` | `24.04-LTS` | `string` |  |
|  | Version | `azure.<account>.image.version` | `latest` | `string` |  |

### OpenstackConfig (Providers.Openstack map values)
| Field | YAML key | Default | Type | Description |
|-------|-----------|----------|------|-------------|
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
|  | Quotas | `openstack.<account>.quotas` | `` | `map[string]Quota` | key: region |
|  | Regions | `openstack.<account>.regions` | `` | `[]string` |  |
|  | Custom | `openstack.<account>.custom` | `` | `map[string]*ast.InterfaceType` | Vendor-specific custom settings |
|  | UpdatePeriodicity | `openstack.<account>.update_periodicity` | `` | `string` | Update periodicity in minutes |
|  | LocalWorkspaceRoots | `openstack.<account>.local_workspaces` | `` | `map[string]string` |  |
|  | Keypair | `openstack.<account>.keypair` | `` | `string` | Name of the keypair to use for SSH access |
