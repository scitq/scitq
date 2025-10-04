package updater

import (
	"fmt"
	"log"
	"time"

	"github.com/scitq/scitq/server/config"
)

// Session is an interface abstracting DB operations.
// In a real implementation, these methods would be implemented
// using your chosen database/ORM library.
type Session interface {
	QueryFlavors(providerID int32) ([]*Flavor, error)
	DeleteFlavor(f *Flavor) error
	AddFlavor(f *Flavor) error
	UpdateFlavor(f *Flavor) error
	QueryFlavorMetrics(providerID int) ([]*FlavorMetrics, error)
	DeleteFlavorMetrics(fm *FlavorMetrics) error
	AddFlavorMetrics(fm *FlavorMetrics) error
	UpdateFlavorMetrics(fm *FlavorMetrics) error
	Commit() error
	Begin() error
	Rollback() error
	Close() error
	IsFlavorInUse(name string, providerID int32) (bool, error)
}

// Flavor represents a compute flavor.
type Flavor struct {
	Name         string
	ProviderID   int
	ProviderName string
	CPU          int
	Mem          float64
	Disk         float64
	Bandwidth    int
	GPU          string
	GPUMem       int
	HasGPU       bool
	HasQuickDisk bool
}

// FlavorMetrics holds regional or cost data for a Flavor.
type FlavorMetrics struct {
	FlavorName string
	ProviderID int
	RegionName string
	Cost       float64
	Eviction   int
	Available  bool
}

// GenericProvider holds a database session, provider name,
// a live flag, and a buffer for non-live output.
type GenericProvider struct {
	Session      Session
	ProviderID   int32
	ProviderName string
	lastSeenMap  map[string]time.Time
}

const maxRetries = 5

// NewGenericProvider creates a new GenericProvider.
func NewGenericProvider(cfg config.Config, provider string) (*GenericProvider, error) {
	db, err := NewPostgresSession(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create database session: %w", err)
	}
	err = db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer db.Rollback()

	var providerID int32
	for i := 0; i < maxRetries; i++ {
		providerID, err = db.GetProviderID(provider)
		if err == nil {
			break
		}
		log.Printf("Attempt %d: failed to find provider %s: %v", i+1, provider, err)
		db.Rollback()
		time.Sleep(time.Duration(i+1) * time.Second) // Increasing sleep duration
		err = db.Begin()
		if err != nil {
			return nil, fmt.Errorf("failed to begin transaction: %w", err)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("provider %s not found after %d retries: %v", provider, maxRetries, err)
	}

	db.Commit()
	return &GenericProvider{
		Session:      db,
		ProviderID:   providerID,
		ProviderName: provider,
		lastSeenMap:  make(map[string]time.Time),
	}, nil
}

func (gp *GenericProvider) GetFlavorLastSeen(name string, providerID int32) time.Time {
	key := fmt.Sprintf("%d|%s", providerID, name)
	inUse, err := gp.Session.IsFlavorInUse(name, providerID)
	if err != nil {
		log.Printf("⚠️ Could not check in-use status for flavor %s: %v", key, err)
		// Defensive fallback: return now
		return time.Now()
	}
	if inUse {
		// If seen before, clean up memory
		if _, ok := gp.lastSeenMap[key]; ok {
			delete(gp.lastSeenMap, key)
		}
		return time.Now()
	}
	// Not in use now
	if seen, ok := gp.lastSeenMap[key]; ok {
		return seen
	}
	now := time.Now()
	gp.lastSeenMap[key] = now
	return now
}

// UpdateFlavors compares the list of new flavors with what is stored in the DB
// and performs updates, deletions, and additions.
func (gp *GenericProvider) UpdateFlavors(newFlavors []*Flavor) error {
	err := gp.Session.Begin()
	if err != nil {
		log.Printf("Error beginning transaction: %v\n", err)
		return err
	}
	defer gp.Session.Rollback()
	// For Flavor, primary keys are Provider and Name.
	newMap := make(map[string]*Flavor)
	for _, f := range newFlavors {
		key := fmt.Sprintf("%d|%s", f.ProviderID, f.Name)
		newMap[key] = f
	}

	existingFlavors, err := gp.Session.QueryFlavors(gp.ProviderID)
	if err != nil {
		return err
	}

	// Compare each existing flavor.
	for _, existing := range existingFlavors {
		key := fmt.Sprintf("%d|%s", existing.ProviderID, existing.Name)
		if newFlavor, ok := newMap[key]; !ok {
			// Check if flavor is safe to delete
			inUse, err := gp.Session.IsFlavorInUse(existing.Name, gp.ProviderID)
			if err != nil {
				log.Printf("⚠️ Error checking if flavor %s is in use: %v\n", key, err)
				return err
			}
			lastSeen := gp.GetFlavorLastSeen(existing.Name, gp.ProviderID)
			if inUse || time.Since(lastSeen) < 72*time.Hour {
				log.Printf("⏳ Flavor %s retained: still in use or seen recently\n", key)
				continue
			}

			log.Printf("🗑️ Flavor %s is removed (not in use and not seen in >72h)\n", key)
			if err := gp.Session.DeleteFlavor(existing); err != nil {
				return err
			}
			delete(gp.lastSeenMap, key)
		} else {
			// Remove the entry to mark it as processed.
			delete(newMap, key)
			changed := false
			// Check and update each attribute explicitly.
			if existing.CPU != newFlavor.CPU {
				log.Printf("Flavor %s update CPU: %d->%d\n", key, existing.CPU, newFlavor.CPU)
				existing.CPU = newFlavor.CPU
				changed = true
			}
			if existing.Mem != newFlavor.Mem {
				log.Printf("Flavor %s update RAM: %f->%f\n", key, existing.Mem, newFlavor.Mem)
				existing.Mem = newFlavor.Mem
				changed = true
			}
			if existing.Disk != newFlavor.Disk {
				log.Printf("Flavor %s update Disk: %f->%f\n", key, existing.Disk, newFlavor.Disk)
				existing.Disk = newFlavor.Disk
				changed = true
			}
			if existing.Bandwidth != newFlavor.Bandwidth {
				log.Printf("Flavor %s update Bandwidth: %d->%d\n", key, existing.Bandwidth, newFlavor.Bandwidth)
				existing.Bandwidth = newFlavor.Bandwidth
				changed = true
			}
			if existing.GPU != newFlavor.GPU {
				log.Printf("Flavor %s update GPU: %s->%s\n", key, existing.GPU, newFlavor.GPU)
				existing.GPU = newFlavor.GPU
				changed = true
			}
			if existing.GPUMem != newFlavor.GPUMem {
				log.Printf("Flavor %s update GPUMem: %d->%d\n", key, existing.GPUMem, newFlavor.GPUMem)
				existing.GPUMem = newFlavor.GPUMem
				changed = true
			}
			if existing.HasGPU != newFlavor.HasGPU {
				log.Printf("Flavor %s update GPU status: %t->%t\n", key, existing.HasGPU, newFlavor.HasGPU)
				existing.HasGPU = newFlavor.HasGPU
				changed = true
			}
			if existing.HasQuickDisk != newFlavor.HasQuickDisk {
				log.Printf("Flavor %s update Quick Disk status: %t->%t\n", key, existing.HasQuickDisk, newFlavor.HasQuickDisk)
				existing.HasQuickDisk = newFlavor.HasQuickDisk
				changed = true
			}
			if changed {
				// Update the flavor in the DB.
				// log.Printf("Flavor %v is updated\n", existing)
				err := gp.Session.UpdateFlavor(existing)
				if err != nil {
					log.Printf("Error updating flavor %v: %v\n", existing, err)
					return err
				}
			}
		}
	}

	// Any remaining new flavors are added.
	for key, f := range newMap {
		log.Printf("new Flavor %s\n", key)
		if err := gp.Session.AddFlavor(f); err != nil {
			return err
		}
	}

	return gp.Session.Commit()
	//return nil
}

// UpdateFlavorMetrics compares and updates flavor metrics (primary keys: Provider, FlavorName, RegionName).
func (gp *GenericProvider) UpdateFlavorMetrics(newMetrics []*FlavorMetrics) error {
	err := gp.Session.Begin()
	if err != nil {
		log.Printf("Error beginning transaction: %v\n", err)
		return err
	}
	defer gp.Session.Rollback()

	// Build a map of existing flavors for quick lookup
	existingFlavors, err := gp.Session.QueryFlavors(gp.ProviderID)
	if err != nil {
		return err
	}
	flavorExists := make(map[string]bool)
	for _, f := range existingFlavors {
		key := fmt.Sprintf("%d|%s", f.ProviderID, f.Name)
		flavorExists[key] = true
	}

	newMap := make(map[string]*FlavorMetrics)
	for _, fm := range newMetrics {
		if fm.Cost == 0 {
			// Skip zero-cost metrics (might be dangerous to keep them)
			log.Printf("Remove metrics %v associated with no cost.", fm)
			continue
		}
		key := fmt.Sprintf("%d|%s|%s", fm.ProviderID, fm.FlavorName, fm.RegionName)
		newMap[key] = fm
	}

	existingMetrics, err := gp.Session.QueryFlavorMetrics(int(gp.ProviderID))
	if err != nil {
		return err
	}

	for _, existing := range existingMetrics {
		key := fmt.Sprintf("%d|%s|%s", existing.ProviderID, existing.FlavorName, existing.RegionName)
		flavorKey := fmt.Sprintf("%d|%s", existing.ProviderID, existing.FlavorName)
		if newMetric, ok := newMap[key]; !ok {
			// Metric missing in new list
			if flavorExists[flavorKey] {
				// Flavor still exists, mark metric as Available = false if not already
				if existing.Available {
					log.Printf("FlavorMetrics %s marked as unavailable\n", key)
					existing.Available = false
					if err := gp.Session.UpdateFlavorMetrics(existing); err != nil {
						return err
					}
				}
			} else {
				// Flavor gone, delete metric
				log.Printf("FlavorMetrics %s is removed (flavor deleted)\n", key)
				if err := gp.Session.DeleteFlavorMetrics(existing); err != nil {
					return err
				}
			}
		} else {
			// Metric found in new list, ensure Available = true and update if needed
			delete(newMap, key)
			changed := false
			if existing.Cost != newMetric.Cost {
				log.Printf("FlavorMetrics %s update Cost: %f->%f\n", key, existing.Cost, newMetric.Cost)
				existing.Cost = newMetric.Cost
				changed = true
			}
			if existing.Eviction != newMetric.Eviction {
				log.Printf("FlavorMetrics %s update Eviction: %d->%d\n", key, existing.Eviction, newMetric.Eviction)
				existing.Eviction = newMetric.Eviction
				changed = true
			}
			if !existing.Available {
				log.Printf("FlavorMetrics %s marked as available\n", key)
				existing.Available = true
				changed = true
			}
			if changed {
				if err := gp.Session.UpdateFlavorMetrics(existing); err != nil {
					return err
				}
			}
		}
	}

	// Any remaining new metrics are added with Available = true
	for key, fm := range newMap {
		log.Printf("new FlavorMetrics %s\n", key)
		fm.Available = true
		if err := gp.Session.AddFlavorMetrics(fm); err != nil {
			return err
		}
	}

	return gp.Session.Commit()
}

func (gp *GenericProvider) Close() error {
	return gp.Session.Close()
}
