package server

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/scitq/scitq/server/providers"
)

// cleanupOrphanVMs scans provider VM lists for VMs matching our naming pattern
// that have no active worker (orphan) or match a soft-deleted worker (zombie).
func cleanupOrphanVMs(db *sql.DB, providerMap map[int32]providers.Provider, serverName string) error {
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(serverName) + `worker\d+$`)

	// Get distinct (provider_id, region_name) pairs from active or recently deleted workers
	rows, err := db.Query(`
		SELECT DISTINCT r.provider_id, r.region_name
		FROM worker w
		JOIN region r ON r.region_id = w.region_id
		WHERE w.deleted_at IS NULL
		   OR w.deleted_at > now() - interval '1 day'
	`)
	if err != nil {
		return fmt.Errorf("query provider/region pairs: %w", err)
	}
	defer rows.Close()

	type provRegion struct {
		providerID int32
		region     string
	}
	var targets []provRegion
	for rows.Next() {
		var pr provRegion
		if err := rows.Scan(&pr.providerID, &pr.region); err != nil {
			return fmt.Errorf("scan provider/region: %w", err)
		}
		targets = append(targets, pr)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate provider/region: %w", err)
	}

	for _, pr := range targets {
		provider, ok := providerMap[pr.providerID]
		if !ok {
			continue
		}

		vms, err := provider.List(pr.region)
		if err != nil {
			log.Printf("⚠️ orphan cleanup: failed to list VMs for provider %d region %s: %v", pr.providerID, pr.region, err)
			continue
		}

		for vmName := range vms {
			if !pattern.MatchString(vmName) {
				continue
			}

			var workerID sql.NullInt32
			var deletedAt sql.NullTime
			err := db.QueryRow(
				`SELECT worker_id, deleted_at FROM worker WHERE worker_name = $1 ORDER BY worker_id DESC LIMIT 1`,
				vmName,
			).Scan(&workerID, &deletedAt)

			if err == sql.ErrNoRows {
				// Orphan: no worker row at all
				log.Printf("🧹 orphan VM %s in region %s (no worker row), deleting", vmName, pr.region)
				if derr := provider.Delete(vmName, pr.region); derr != nil {
					log.Printf("⚠️ failed to delete orphan VM %s: %v", vmName, derr)
				}
			} else if err != nil {
				log.Printf("⚠️ orphan cleanup: query worker for VM %s: %v", vmName, err)
			} else if deletedAt.Valid {
				// Zombie: matches a soft-deleted worker
				log.Printf("🧹 zombie VM %s in region %s (worker %d deleted at %s), deleting",
					vmName, pr.region, workerID.Int32, deletedAt.Time.Format(time.RFC3339))
				if derr := provider.Delete(vmName, pr.region); derr != nil {
					log.Printf("⚠️ failed to delete zombie VM %s: %v", vmName, derr)
				}
			}
			// else: active worker, normal — skip
		}
	}

	return nil
}

// startOrphanCleanup launches the periodic orphan/zombie VM cleanup goroutine.
func (s *taskQueueServer) startOrphanCleanup() {
	go func() {
		// First run after 2 minutes
		time.Sleep(2 * time.Minute)
		if err := cleanupOrphanVMs(s.db, s.providers, s.cfg.Scitq.ServerName); err != nil {
			log.Printf("⚠️ initial orphan cleanup error: %v", err)
		}

		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := cleanupOrphanVMs(s.db, s.providers, s.cfg.Scitq.ServerName); err != nil {
					log.Printf("⚠️ orphan cleanup error: %v", err)
				}
			}
		}
	}()
}
