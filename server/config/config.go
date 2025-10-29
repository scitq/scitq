package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v2"
)

// Config represents the overall configuration for the scitq server.
// It includes settings for the server itself, cloud providers, and rclone integrations.
type Config struct {
	// Scitq contains configuration parameters specific to the scitq server.
	Scitq struct {
		// Port is the TCP port on which the scitq server listens for gRPC incoming connections.
		Port int `yaml:"port" default:"50051"`

		// DBURL is the database connection string used by scitq to connect to its PostgreSQL database.
		// It should include the username, password, host, database name, and SSL mode.
		DBURL string `yaml:"db_url" default:"postgres://localhost/scitq2?sslmode=disable"`

		// MaxDBConcurrency limits the maximum number of concurrent database connections.
		MaxDBConcurrency int `yaml:"max_db_concurrency" default:"50"`

		// LogLevel sets the verbosity level of logging output.
		// Common values include "debug", "info", "warn", and "error".
		LogLevel string `yaml:"log_level" default:"info"`

		// LogRoot specifies the root directory where remote task stdout/stderr files are stored.
		LogRoot string `yaml:"log_root" default:"log"`

		// ScriptRoot is the directory where (python) server-side scripts are located.
		// Scripts run by scitq are expected to be found here.
		ScriptRoot string `yaml:"script_root" default:"scripts"`

		// ScriptVenv specifies the path to the Python virtual environment used to run scripts.
		// This isolates script dependencies from the system Python environment.
		// Python venv creation and DSL installation in the venv is managed automatically by server.
		ScriptVenv string `yaml:"script_venv" default:"/var/lib/scitq/python"`

		// ScriptRunnerUser is the system user account under which scripts are executed.
		// Running scripts as a non-privileged user enhances security.
		ScriptRunnerUser string `yaml:"script_runner_user" default:"nobody"`

		// ClientBinaryPath is the filesystem path to the scitq client binary.
		// This is used for automated client installation.
		ClientBinaryPath string `yaml:"client_binary_path" default:"/usr/local/bin/scitq-client"`

		// ClientDownloadToken is a secret token used to authorize client binary downloads.
		// If not set, a random token is generated at startup.
		ClientDownloadToken string `yaml:"client_download_token"`

		// CertificateKey is the path or content of the TLS private key file for HTTPS.
		// Required if you use your own certificates
		CertificateKey string `yaml:"certificate_key"`

		// CertificatePem is the path or content of the TLS certificate file for HTTPS.
		// Required if you use your own certificates
		CertificatePem string `yaml:"certificate_pem"`

		// ServerName is the short name identifier for the server.
		ServerName string `yaml:"server_name"`

		// ServerFQDN is the fully qualified domain name of the server.
		ServerFQDN string `yaml:"server_fqdn"`

		// DockerRegistry specifies the default container registry URL for pulling images.
		DockerRegistry string `yaml:"docker_registry"`

		// DockerAuthentication holds the authentication token or credentials for the default Docker registry.
		DockerAuthentication string `yaml:"docker_authentication"`

		// DockerCredentials contains multiple registry→secret pairs for authenticating to container registries.
		// Used by clients to access private registries.
		DockerCredentials []DockerCredential `yaml:"docker_credentials"`

		// SwapProportion defines the proportion of disk space dedicated to swap on worker automated deploy.
		SwapProportion float32 `yaml:"swap_proportion" default:"0.1"`

		// WorkerToken is a secret token used to authenticate worker nodes.
		WorkerToken string `yaml:"worker_token"`

		// JwtSecret is the secret key used to sign JWT tokens.
		JwtSecret string `yaml:"jwt_secret"`

		// RecruitmentInterval sets the interval in seconds for recruiting new workers.
		RecruitmentInterval int `yaml:"recruiter_interval" default:"5"`

		// IdleTimeout defines the timeout in seconds after which idle workers are considered for shutdown.
		IdleTimeout int `yaml:"idle_timeout" default:"300"`

		// NewWorkerIdleTimeout is the timeout in seconds for newly started workers before they are considered idle.
		NewWorkerIdleTimeout int `yaml:"new_worker_idle_timeout" default:"900"`

		// OfflineTimeout is the timeout in seconds after which offline workers are considered lost.
		OfflineTimeout int `yaml:"offline_timeout" default:"30"`

		// TaskDownloadTimeout is the timeout in seconds for task data downloads.
		TaskDownloadTimeout int `yaml:"task_download_timeout" default:"600"`

		// TaskExecutionTimeout is the timeout in seconds for task execution.
		// A value of 0 disables the timeout.
		TaskExecutionTimeout int `yaml:"task_execution_timeout" default:"0"`

		// TaskUploadTimeout is the timeout in seconds for uploading task results.
		TaskUploadTimeout int `yaml:"task_upload_timeout" default:"600"`

		// ConsideredLostTimeout is the timeout in seconds after which a task is considered lost.
		ConsideredLostTimeout int `yaml:"considered_lost_timeout" default:"300" `

		// AdminUser is the username for the administrator account.
		AdminUser string `yaml:"admin_user" default:"admin"`

		// AdminHashedPassword is the hashed password for the administrator account.
		// It can be generated by CLI : `scitq hashpassword MySuperPassword`
		AdminHashedPassword string `yaml:"admin_hashed_password" default:""`

		// AdminEmail is the email address of the administrator.
		AdminEmail string `yaml:"admin_email" default:""`

		// DisableHTTPS disables HTTPS support when set to true.
		DisableHTTPS bool `yaml:"disable_https" default:"false"`

		// DisableGRPCWeb disables gRPC-Web support when set to true.
		// Used for test only
		DisableGRPCWeb bool `yaml:"disable_grpcweb" default:"false"`

		// HTTPSPort is the TCP port used for HTTPS connections.
		HTTPSPort int `yaml:"https_port" default:"443"`
	} `yaml:"scitq"`

	// Providers contains configurations for different cloud providers supported by scitq.
	// Each provider can use multiple account, so you can have several config called Primary, Secondary etc.
	// For OVH, use an Openstack account (that you can name OVH)
	// see the example for details
	Providers struct {
		Azure     map[string]*AzureConfig        `yaml:"azure"`
		Openstack map[string]*OpenstackConfig    `yaml:"openstack"`
		Fake      map[string]*FakeProviderConfig `yaml:"fake"`
	} `yaml:"providers"`

	// Rclone holds configuration mappings for rclone integrations.
	// Create your config using native rclone with `rclone config`
	// then export the config to `scitq.yaml` with the CLI `scitq config import-rclone >> /etc/scitq.yaml`
	Rclone map[string]map[string]string `yaml:"rclone"`
}

// Quota defines resource limits such as CPU and memory for cloud provider instances.
type Quota struct {
	MaxCPU   int32   `yaml:"cpu"`
	MaxMemGB float32 `yaml:"mem,omitempty"` // optional
}

// DockerCredential represents a registry→secret pair used by clients to auth to a container registry
type DockerCredential struct {
	Registry string `yaml:"registry"`
	Secret   string `yaml:"secret"`
}

type AzureConfig struct {
	Name                string            `yaml:"-"`
	DefaultRegion       string            `yaml:"default_region"`
	SubscriptionID      string            `yaml:"subscription_id"`
	ClientID            string            `yaml:"client_id"`
	ClientSecret        string            `yaml:"client_secret"`
	TenantID            string            `yaml:"tenant_id"`
	UseSpot             bool              `yaml:"use_spot" default:"true"`
	Username            string            `yaml:"username" default:"ubuntu"` // Default username for the VM, using OVH default
	SSHPublicKey        string            `yaml:"ssh_public_key" default:"~/.ssh/id_rsa.pub"`
	Image               AzureImage        `yaml:"image"`
	Quotas              map[string]Quota  `yaml:"quotas"` // key: region
	Regions             []string          `yaml:"regions"`
	UpdatePeriodicity   string            `yaml:"update_periodicity"` // Update periodicity in minutes
	LocalWorkspaceRoots map[string]string `yaml:"local_workspaces"`
}

type AzureImage struct {
	Publisher string `yaml:"publisher" default:"Canonical"`
	Offer     string `yaml:"offer" default:"UbuntuServer"`
	Sku       string `yaml:"sku" default:"24.04-LTS"`
	Version   string `yaml:"version" default:"latest"`
}

type OpenstackConfig struct {
	Name       string `yaml:"-"`
	AuthURL    string `yaml:"auth_url"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	DomainName string `yaml:"domain_name"`
	DomainID   string `yaml:"domain_id"`
	TenantName string `yaml:"tenant_name"`

	// Keystone project identifiers (either one can be used)
	ProjectID   string `yaml:"project_id"`
	ProjectName string `yaml:"project_name"`

	// Domain scoping (Keystone v3)
	UserDomainName  string `yaml:"user_domain_name"`
	ProjectDomainID string `yaml:"project_domain_id"`

	// Optional: prefer Application Credentials when provided (portable OpenStack)
	ApplicationCredentialID     string `yaml:"application_credential_id"`
	ApplicationCredentialSecret string `yaml:"application_credential_secret"`

	// Optional interface selection for service endpoints (public/internal/admin)
	Interface string `yaml:"interface"` // Unsure this is used - prefer NetworkID

	// Optional: Keystone identity API version (default 3)
	IdentityAPIVersion int `yaml:"identity_api_version" default:"3"`

	DefaultRegion       string                 `yaml:"region"`
	ImageID             string                 `yaml:"image_id"`
	FlavorID            string                 `yaml:"flavor_id"`
	NetworkID           string                 `yaml:"network_id"`
	ExtNetworkID        string                 `yaml:"ext_network_id"`
	Quotas              map[string]Quota       `yaml:"quotas"` // key: region
	Regions             []string               `yaml:"regions"`
	Custom              map[string]interface{} `yaml:"custom"`             // Vendor-specific custom settings
	UpdatePeriodicity   string                 `yaml:"update_periodicity"` // Update periodicity in minutes
	LocalWorkspaceRoots map[string]string      `yaml:"local_workspaces"`
	Keypair             string                 `yaml:"keypair"` // Name of the keypair to use for SSH access
}

func (c *Config) Validate() error {
	if c.Scitq.DBURL == "" {
		return fmt.Errorf("scitq.db_url must be provided")
	}
	if c.Scitq.Port == 0 {
		return fmt.Errorf("scitq.port must be provided and non-zero")
	}
	if c.Scitq.JwtSecret == "" {
		return fmt.Errorf("scitq.jwt_secret must be provided")
	}
	if c.Scitq.WorkerToken == "" {
		return fmt.Errorf("switq.worker_token must be provided")
	}
	if len(c.Scitq.DockerCredentials) > 0 && (c.Scitq.DockerRegistry != "" || c.Scitq.DockerAuthentication != "") {
		log.Printf("warning: both scitq.docker_credentials and legacy scitq.docker_registry/docker_authentication are set; using docker_credentials only")
	}
	// You can add more rules as needed
	return nil
}

type ProviderConfig interface {
	GetRegions() []string
	SetRegions([]string)
	GetQuotas() map[string]Quota
	GetUpdatePeriodicity() time.Duration
	GetName() string
	SetName(string)
	GetDefaultRegion() string
	GetWorkspaceRoot(region string) (string, bool)
}

// GetDockerCredentials returns all configured docker registry credentials.
// It supports the new multi-credential form and the legacy single-pair fields.
func (c *Config) GetDockerCredentials() map[string]string {
	creds := make(map[string]string)
	// New format
	for _, dc := range c.Scitq.DockerCredentials {
		if dc.Registry == "" || dc.Secret == "" {
			continue
		}
		creds[dc.Registry] = dc.Secret
	}
	// Legacy fallback (single pair)
	if len(creds) == 0 && c.Scitq.DockerRegistry != "" && c.Scitq.DockerAuthentication != "" {
		creds[c.Scitq.DockerRegistry] = c.Scitq.DockerAuthentication
	}
	return creds
}

func parsePeriodicity(periodicity string, name string) time.Duration {
	if periodicity == "" {
		return 0
	}
	d, err := time.ParseDuration(periodicity)
	if err != nil {
		log.Printf("Error parsing update_periodicity for provider %s (%q): %v", name, periodicity, err)
		// Return a default duration if parsing fails, or handle the error as needed.
		return 0
	}
	return d
}

func (a *AzureConfig) GetRegions() []string {
	return a.Regions
}

func (a *AzureConfig) GetDefaultRegion() string {
	return a.DefaultRegion
}

func (a *AzureConfig) SetRegions(r []string) {
	a.Regions = r
}

func (a *AzureConfig) GetQuotas() map[string]Quota {
	return a.Quotas
}

func (a *AzureConfig) GetUpdatePeriodicity() time.Duration {
	return parsePeriodicity(a.UpdatePeriodicity, a.Name)
}

func (a *AzureConfig) GetName() string {
	return a.Name
}

func (a *AzureConfig) SetName(name string) {
	a.Name = name
}

func (a *AzureConfig) GetWorkspaceRoot(region string) (string, bool) {
	if a.LocalWorkspaceRoots == nil {
		return "", false
	}
	if root, ok := a.LocalWorkspaceRoots[region]; ok {
		return root, true
	}
	if root, ok := a.LocalWorkspaceRoots["*"]; ok {
		return root, true
	}
	return "", false
}

func (o *OpenstackConfig) GetRegions() []string {
	return o.Regions
}

func (o *OpenstackConfig) GetDefaultRegion() string {
	return o.DefaultRegion
}

func (o *OpenstackConfig) SetRegions(r []string) {
	o.Regions = r
}

func (o *OpenstackConfig) GetQuotas() map[string]Quota {
	return o.Quotas
}

func (o *OpenstackConfig) GetUpdatePeriodicity() time.Duration {
	return parsePeriodicity(o.UpdatePeriodicity, o.Name)
}

func (o *OpenstackConfig) SetName(name string) {
	o.Name = name
}

func (o *OpenstackConfig) GetName() string {
	return o.Name
}

func (o *OpenstackConfig) GetWorkspaceRoot(region string) (string, bool) {
	if o.LocalWorkspaceRoots == nil {
		return "", false
	}
	if root, ok := o.LocalWorkspaceRoots[region]; ok {
		return root, true
	}
	if root, ok := o.LocalWorkspaceRoots["*"]; ok {
		return root, true
	}
	return "", false
}

func (cfg *Config) GetProviders() []ProviderConfig {
	var providers []ProviderConfig
	for n, p := range cfg.Providers.Azure {
		p.SetName("azure." + n)
		providers = append(providers, p)
	}
	for n, p := range cfg.Providers.Openstack {
		p.SetName("openstack." + n)
		providers = append(providers, p)
	}
	for n, p := range cfg.Providers.Fake {
		p.SetName("fake." + n)
		providers = append(providers, p)
	}
	return providers
}

// LoadConfig reads a YAML file and returns a Config structure.
func LoadConfig(file string) (*Config, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var cfg Config
	// Set defaults based on struct tags.
	if err := defaults.Set(&cfg); err != nil {
		log.Printf("failed to set defaults: %v", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	// Then set defaults on any zero-valued fields.
	if err := defaults.Set(&cfg); err != nil {
		log.Printf("failed to set defaults: %v", err)
	}
	if cfg.Scitq.ClientDownloadToken == "" {
		cfg.Scitq.ClientDownloadToken = randomToken()
	}
	for _, p := range cfg.GetProviders() {
		if p.GetRegions() == nil && p.GetQuotas() != nil {
			log.Printf("Setting regions based on quotas for provider %s", p.GetName())
			var regions []string
			for region := range p.GetQuotas() {
				regions = append(regions, region)
			}
			p.SetRegions(regions)
		}
		if p.GetRegions() == nil {
			log.Printf("Setting regions based on default region for provider %s", p.GetName())
			p.SetRegions([]string{p.GetDefaultRegion()})
		}
	}
	return &cfg, nil
}

// randomToken generates a random token string.
func randomToken() string {
	b := make([]byte, 32) // 32 bytes = 256 bits of randomness
	if _, err := rand.Read(b); err != nil {
		// In production, you might want to handle the error differently.
		return "defaultRandomToken"
	}
	// Use RawURLEncoding to avoid padding and ensure URL safety.
	return base64.RawURLEncoding.EncodeToString(b)
}
