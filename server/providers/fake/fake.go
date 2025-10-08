package fake

import (
	"errors"
	"fmt"
	"sync"

	"github.com/scitq/scitq/server/config"
	"github.com/scitq/scitq/server/providers"
)

// Ensure FakeProvider implements providers.Provider
var _ providers.Provider = (*FakeProvider)(nil)

// FakeProvider is an in-memory implementation of the Provider interface.
// It keeps track of created workers and their fake IPs, supporting multiple regions.
type FakeProvider struct {
	mu sync.Mutex
	// regions maps region name -> (workerName -> fake IP)
	regions map[string]map[string]string
}

// New returns a new empty FakeProvider with a single "default" region.
func New() *FakeProvider {
	return &FakeProvider{
		regions: map[string]map[string]string{
			"default": {},
		},
	}
}

// NewFromConfig returns a new FakeProvider with regions initialized from config.
func NewFromConfig(cfg config.Config, regions []string) *FakeProvider {
	rmap := make(map[string]map[string]string, len(regions))
	for _, region := range regions {
		rmap[region] = make(map[string]string)
	}
	return &FakeProvider{
		regions: rmap,
	}
}

// Create records a new worker in the specified region and returns a fake IP address.
func (f *FakeProvider) Create(workerName, flavor, location string, jobId int32) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	region := location
	if region == "" {
		region = "default"
	}
	if _, ok := f.regions[region]; !ok {
		f.regions[region] = make(map[string]string)
	}
	if workerName == "" {
		return "", errors.New("workerName required")
	}
	if _, exists := f.regions[region][workerName]; exists {
		return "", fmt.Errorf("worker %s already exists", workerName)
	}
	ip := fmt.Sprintf("10.0.0.%d", len(f.regions[region])+1)
	f.regions[region][workerName] = ip
	return ip, nil
}

// List returns all currently known workers with their fake IPs across all regions.
func (f *FakeProvider) List() (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]string)
	for _, workers := range f.regions {
		for k, v := range workers {
			out[k] = v
		}
	}
	return out, nil
}

// Restart succeeds if the worker exists in the given region, otherwise returns an error.
func (f *FakeProvider) Restart(workerName, location string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	region := location
	if region == "" {
		region = "default"
	}
	workers, ok := f.regions[region]
	if !ok {
		return fmt.Errorf("region %s not found", region)
	}
	if _, ok := workers[workerName]; !ok {
		return fmt.Errorf("worker %s not found", workerName)
	}
	// No real action needed
	return nil
}

// Delete removes the worker from memory in the given region.
func (f *FakeProvider) Delete(workerName, location string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	region := location
	if region == "" {
		region = "default"
	}
	workers, ok := f.regions[region]
	if !ok {
		return fmt.Errorf("region %s not found", region)
	}
	if _, ok := workers[workerName]; !ok {
		return fmt.Errorf("worker %s not found", workerName)
	}
	delete(workers, workerName)
	return nil
}
