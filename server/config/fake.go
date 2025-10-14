package config

import "time"

// FakeProviderConfig is a lightweight test provider configuration
// that implements ProviderConfig. It can be used to simulate
// a provider in integration tests.
type FakeProviderConfig struct {
	Name          string           `yaml:"-"`
	DefaultRegion string           `yaml:"default_region"`
	Regions       []string         `yaml:"regions"`
	Quotas        map[string]Quota `yaml:"quotas"`

	// The following fields are used only in testing contexts to allow
	// the fake provider to automatically launch worker clients against
	// a running test server.
	AutoLaunch bool   `yaml:"auto_launch"`
	ServerAddr string `yaml:"server_addr"`
	DeployTime int    `yaml:"delayed_deploy" default:"0"` // seconds to wait before launching workers
}

func (f *FakeProviderConfig) GetRegions() []string {
	return f.Regions
}

func (f *FakeProviderConfig) SetRegions(regions []string) {
	f.Regions = regions
}

func (f *FakeProviderConfig) GetQuotas() map[string]Quota {
	return f.Quotas
}

func (f *FakeProviderConfig) GetUpdatePeriodicity() time.Duration {
	// Fake provider does not have automatic updates
	return 0
}

func (f *FakeProviderConfig) GetName() string {
	return f.Name
}

func (f *FakeProviderConfig) SetName(name string) {
	f.Name = name
}

func (f *FakeProviderConfig) GetDefaultRegion() string {
	return f.DefaultRegion
}

func (f *FakeProviderConfig) GetWorkspaceRoot(region string) (string, bool) {
	// Fake provider has no local workspace root mapping
	return "", false
}
