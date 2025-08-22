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

type Config struct {
	Scitq struct {
		Port                 int     `yaml:"port" default:"50051"`
		DBURL                string  `yaml:"db_url" default:"postgres://localhost/scitq2?sslmode=disable"`
		MaxDBConcurrency     int     `yaml:"max_db_concurrency" default:"50"`
		LogLevel             string  `yaml:"log_level" default:"info"`
		LogRoot              string  `yaml:"log_root" default:"log"`
		ScriptRoot           string  `yaml:"script_root" default:"scripts"`
		ScriptInterpreter    string  `yaml:"script_interpreter" default:"python3"`
		ScriptRunnerUser     string  `yaml:"script_runner_user" default:"nobody"`
		ClientBinaryPath     string  `yaml:"client_binary_path" default:"/usr/local/bin/scitq-client"`
		ClientDownloadToken  string  `yaml:"client_download_token"`
		CertificateKey       string  `yaml:"certificate_key"`
		CertificatePem       string  `yaml:"certificate_pem"`
		ServerName           string  `yaml:"server_name"`
		ServerFQDN           string  `yaml:"server_fqdn"`
		DockerRegistry       string  `yaml:"docker_registry"`
		DockerAuthentication string  `yaml:"docker_authentication"`
		SwapProportion       float32 `yaml:"swap_proportion" default:"0.1"`
		WorkerToken          string  `yaml:"worker_token"`
		JwtSecret            string  `yaml:"jwt_secret"`
		RecruitmentInterval  int     `yaml:"recruiter_interval" default:"15"`
		IdleTimeout          int     `yaml:"idle_timeout" default:"300"`
		NewWorkerIdleTimeout int     `yaml:"new_worker_idle_timeout" default:"900"`
		OfflineTimeout       int     `yaml:"offline_timeout" default:"30"`
		AdminUser            string  `yaml:"admin_user" default:"admin"`
		AdminHashedPassword  string  `yaml:"admin_hashed_password" default:""`
		AdminEmail           string  `yaml:"admin_email" default:""`
		DisableHTTPS         bool    `yaml:"disable_https" default:"false"`
		DisableGRPCWeb       bool    `yaml:"disable_grpcweb" default:"false"`
		HTTPSPort            int     `yaml:"https_port" default:"443"`
	} `yaml:"scitq"`
	Providers struct {
		Azure     map[string]*AzureConfig     `yaml:"azure"`
		Openstack map[string]*OpenstackConfig `yaml:"openstack"`
	} `yaml:"providers"`
}

type Quota struct {
	MaxCPU   int32   `yaml:"cpu"`
	MaxMemGB float32 `yaml:"mem,omitempty"` // optional
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
	Name                string                 `yaml:"-"`
	AuthURL             string                 `yaml:"auth_url"`
	Username            string                 `yaml:"username"`
	Password            string                 `yaml:"password"`
	DomainName          string                 `yaml:"domain_name"`
	TenantName          string                 `yaml:"tenant_name"`
	DefaultRegion       string                 `yaml:"region"`
	ImageID             string                 `yaml:"image_id"`
	FlavorID            string                 `yaml:"flavor_id"`
	NetworkID           string                 `yaml:"network_id"`
	Quotas              map[string]Quota       `yaml:"quotas"` // key: region
	Regions             []string               `yaml:"regions"`
	Custom              map[string]interface{} `yaml:"custom"`             // Vendor-specific custom settings
	UpdatePeriodicity   string                 `yaml:"update_periodicity"` // Update periodicity in minutes
	LocalWorkspaceRoots map[string]string      `yaml:"local_workspaces"`
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
