package fake

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/scitq/scitq/client"
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

	AutoLaunch  bool
	ServerAddr  string
	WorkerToken string

	cancels map[string]context.CancelFunc
}

// New returns a new empty FakeProvider with a single "default" region.
func New() *FakeProvider {
	return &FakeProvider{
		regions: map[string]map[string]string{
			"default": {},
		},
		cancels: make(map[string]context.CancelFunc),
	}
}

// NewFromConfig returns a new FakeProvider with regions initialized from config.
func NewFromConfig(cfg config.Config, config config.FakeProviderConfig) *FakeProvider {
	rmap := make(map[string]map[string]string, len(config.Regions))
	for _, region := range config.Regions {
		rmap[region] = make(map[string]string)
	}
	return &FakeProvider{
		regions:     rmap,
		cancels:     make(map[string]context.CancelFunc),
		WorkerToken: cfg.Scitq.WorkerToken,
		ServerAddr:  fmt.Sprintf("%s:%d", cfg.Scitq.ServerFQDN, cfg.Scitq.Port),
		AutoLaunch:  config.AutoLaunch,
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

	if f.AutoLaunch {
		if f.ServerAddr != "" && f.WorkerToken != "" {
			workerNameCopy := workerName
			ctx, cancel := context.WithCancel(context.Background())
			f.cancels[workerNameCopy] = cancel
			go func() {
				log.Printf("Auto-launching real worker client for %s", workerNameCopy)
				tmpDir, err := os.MkdirTemp("", "scitq-worker-"+workerNameCopy+"-*")
				if err != nil {
					log.Printf("Failed to create temp dir for worker %s: %v", workerNameCopy, err)
					return
				}
				defer func() {
					err := os.RemoveAll(tmpDir)
					if err != nil {
						log.Printf("Failed to remove temp dir %s: %v", tmpDir, err)
					}
				}()

				storePath := filepath.Join(tmpDir, "store")
				err = os.Mkdir(storePath, 0755)
				if err != nil {
					log.Printf("Failed to create store dir for worker %s: %v", workerNameCopy, err)
					return
				}

				// Run the worker client with concurrency=1, block until context is canceled.
				err = client.Run(ctx, f.ServerAddr, 1, workerNameCopy, storePath, f.WorkerToken)
				if err != nil {
					log.Printf("Worker client %s exited with error: %v", workerNameCopy, err)
				} else {
					log.Printf("Worker client %s exited normally", workerNameCopy)
				}
			}()
		} else {
			log.Printf("⚠️ AutoLaunch is enabled but ServerAddr or WorkerToken is not set; skipping auto-launch for %s", workerName)
		}
	}

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

	if cancel, exists := f.cancels[workerName]; exists {
		cancel()
		delete(f.cancels, workerName)
		log.Printf("🧹 [FakeProvider] terminated auto-launched worker %s", workerName)
	}

	return nil
}
