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
