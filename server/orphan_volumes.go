package server

import (
	"log"
	"time"

	"github.com/scitq/scitq/server/providers/openstack"
)

// orphanVolumeCleanupInterval drives the orphan-volume sweep. 10 min
// is far longer than any legitimate volume-create → attach window
// (seconds), so we never race with a volume that was mid-attach when
// we polled; also cheap — each tick is one Cinder list call per OVH
// region, filtered client-side on the scitq_worker metadata key.
const orphanVolumeCleanupInterval = 10 * time.Minute

// startOrphanVolumeCleanup launches the background goroutine that
// finds Cinder volumes tagged with scitq_worker metadata but whose
// worker row is gone (hard-deleted or soft-deleted long enough that
// the row has been purged) and destroys them.
//
// Rationale: Delete already tries to detach+destroy the extra volume
// on worker termination, but OVH's control plane does occasionally
// leave volumes dangling (same incident class as Lucie's 2026-05-04
// stuck-D worker rows). A dangling volume is a continuous paid cost,
// so a periodic sweep is required even though it should normally
// have nothing to do.
//
// Non-OpenStack providers are skipped (the type-assertion returns
// false), so this costs nothing on an Azure-only deployment.
func (s *taskQueueServer) startOrphanVolumeCleanup() {
	go func() {
		ticker := time.NewTicker(orphanVolumeCleanupInterval)
		defer ticker.Stop()
		// One sweep immediately on startup — if the server was down
		// while a Delete was in flight, the orphan is already there
		// waiting to be found.
		s.sweepOrphanVolumes()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.sweepOrphanVolumes()
			}
		}
	}()
}

// sweepOrphanVolumes runs one pass of the orphan-volume cleanup. For
// each registered OpenStack provider, enumerate every region, build
// the set of live worker names (`worker.deleted_at IS NULL` wins; we
// include soft-deleted-recently to avoid racing an in-flight Delete),
// ask the provider for orphans, and destroy each one.
func (s *taskQueueServer) sweepOrphanVolumes() {
	// Live names cutoff: a worker whose row is still around AT ALL is
	// a "live name" for our purposes — the explicit Delete path will
	// handle it when it runs. Only names that have been fully purged
	// from the worker table count as orphans. In practice soft-deletes
	// linger forever today, so this is almost always just "every row".
	liveNames := make(map[string]struct{})
	rows, err := s.db.Query(`SELECT worker_name FROM worker`)
	if err != nil {
		log.Printf("⚠️ orphan-volume sweep: list workers: %v", err)
		return
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		liveNames[name] = struct{}{}
	}
	rows.Close()

	for pid, provider := range s.providers {
		osp, ok := provider.(*openstack.Provider)
		if !ok {
			continue
		}
		regions, err := s.listProviderRegions(pid)
		if err != nil {
			log.Printf("⚠️ orphan-volume sweep: regions for provider %d: %v", pid, err)
			continue
		}
		for _, region := range regions {
			orphans, err := osp.ListOrphanVolumes(region, liveNames)
			if err != nil {
				log.Printf("⚠️ orphan-volume sweep: list %d/%s: %v", pid, region, err)
				continue
			}
			for _, v := range orphans {
				log.Printf("🧹 orphan-volume: deleting %s (name=%s worker=%s region=%s size=%dGB)",
					v.ID, v.Name, v.WorkerName, v.Region, v.SizeGB)
				if err := osp.DeleteOrphanVolume(region, v.ID); err != nil {
					log.Printf("⚠️ orphan-volume: delete %s failed: %v", v.ID, err)
				}
			}
		}
	}
}

// listProviderRegions returns every region name registered for the
// given provider. The orphan-volume sweep enumerates regions this way
// because the OpenStack API is region-scoped — one Cinder client per
// region, and OVH deployments regularly span GRA/SBG/BHS.
func (s *taskQueueServer) listProviderRegions(providerID int32) ([]string, error) {
	rows, err := s.db.Query(`SELECT region_name FROM region WHERE provider_id = $1`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}
