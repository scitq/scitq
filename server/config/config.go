package config

import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"os"

	"github.com/creasty/defaults"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Scitq struct {
		Port                int    `yaml:"port" default:"50051"`
		DBURL               string `yaml:"db_url" default:"postgres://localhost/scitq2?sslmode=disable"`
		LogLevel            string `yaml:"log_level" default:"info"`
		LogRoot             string `yaml:"log_root" default:"log"`
		ClientBinaryPath    string `yaml:"client_binary_path" default:"/usr/local/bin/scitq-client"`
		ClientDownloadToken string `yaml:"client_download_token"`
		CertificateKey      string `yaml:"certificate_key"`
		CertificatePem      string `yaml:"certificate_pem"`
	} `yaml:"scitq"`
	Providers struct {
		Azure     map[string]AzureConfig     `yaml:"azure"`
		Openstack map[string]OpenstackConfig `yaml:"openstack"`
	} `yaml:"providers"`
}

type AzureConfig struct {
	DefaultRegion  string     `yaml:"default_region"`
	SubscriptionID string     `yaml:"subscription_id"`
	ResourceGroup  string     `yaml:"resource_group"`
	Location       string     `yaml:"location"`
	AdminUsername  string     `yaml:"admin_username"`
	AdminPassword  string     `yaml:"admin_password"`
	VMSize         string     `yaml:"vm_size"`
	NICName        string     `yaml:"nic_name"`
	Image          AzureImage `yaml:"image"`
}

type AzureImage struct {
	Publisher string `yaml:"publisher" default:"Canonical"`
	Offer     string `yaml:"offer" default:"UbuntuServer"`
	Sku       string `yaml:"sku" default:"24.04-LTS"`
	Version   string `yaml:"version" default:"latest"`
}

type OpenstackConfig struct {
	AuthURL       string                 `yaml:"auth_url"`
	Username      string                 `yaml:"username"`
	Password      string                 `yaml:"password"`
	DomainName    string                 `yaml:"domain_name"`
	TenantName    string                 `yaml:"tenant_name"`
	DefaultRegion string                 `yaml:"region"`
	ImageID       string                 `yaml:"image_id"`
	FlavorID      string                 `yaml:"flavor_id"`
	NetworkID     string                 `yaml:"network_id"`
	Custom        map[string]interface{} `yaml:"custom"` // Vendor-specific custom settings
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
	if cfg.Scitq.ClientDownloadToken == "" {
		cfg.Scitq.ClientDownloadToken = randomToken()
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
