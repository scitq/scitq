package server

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/scitq/scitq/server/config"
)

// aggregateFlavorStats processes soft-deleted workers that haven't been aggregated yet,
// writing eviction and deploy stats into the flavor_stats table.
func aggregateFlavorStats(db *sql.DB, providerConfig map[string]config.ProviderConfig) error {
	rows, err := db.Query(`
		SELECT w.worker_id, w.flavor_id, w.region_id, w.created_at, w.deleted_at,
		       w.status AS final_status,
		       COALESCE(p.provider_name || '.' || p.config_name, '') AS provider_full,
		       EXISTS (
		           SELECT 1 FROM job j
		           WHERE j.worker_id = w.worker_id AND j.action = 'D' AND j.status = 'S'
		       ) AS was_cleanly_deleted,
		       (SELECT j2.status FROM job j2
		        WHERE j2.worker_id = w.worker_id AND j2.action = 'C'
		        ORDER BY j2.job_id DESC LIMIT 1) AS create_job_status,
		       (SELECT j2.created_at FROM job j2
		        WHERE j2.worker_id = w.worker_id AND j2.action = 'C'
		        ORDER BY j2.job_id DESC LIMIT 1) AS create_job_time
		FROM worker w
		LEFT JOIN flavor f ON f.flavor_id = w.flavor_id
		LEFT JOIN provider p ON f.provider_id = p.provider_id
		WHERE w.deleted_at IS NOT NULL
		  AND w.aggregated_at IS NULL
		  AND w.flavor_id IS NOT NULL
		  AND w.region_id IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("query unaggregated workers: %w", err)
	}
	defer rows.Close()

	type workerRow struct {
		workerID         int32
		flavorID         int32
		regionID         int32
		createdAt        time.Time
		deletedAt        time.Time
		finalStatus      string
		providerFull     string
		wasCleanDeleted  bool
		createJobStatus  sql.NullString
		createJobTime    sql.NullTime
	}

	var workers []workerRow
	for rows.Next() {
		var w workerRow
		if err := rows.Scan(&w.workerID, &w.flavorID, &w.regionID, &w.createdAt, &w.deletedAt,
			&w.finalStatus, &w.providerFull, &w.wasCleanDeleted,
			&w.createJobStatus, &w.createJobTime); err != nil {
			return fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate workers: %w", err)
	}

	if len(workers) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, w := range workers {
		// Distribute worker-hours across hour_of_week buckets
		distributeWorkerHours(tx, w.flavorID, w.regionID, w.createdAt, w.deletedAt)

		// Eviction detection
		if isEviction(w.finalStatus, w.wasCleanDeleted, w.providerFull, providerConfig) {
			bucket := hourOfWeek(w.deletedAt)
			upsertFlavorStat(tx, w.flavorID, w.regionID, bucket, 0, 1, 0, 0)
		}

		// Deploy stats
		if w.createJobStatus.Valid && w.createJobTime.Valid {
			bucket := hourOfWeek(w.createJobTime.Time)
			failed := 0
			if w.createJobStatus.String == "F" {
				failed = 1
			}
			upsertFlavorStat(tx, w.flavorID, w.regionID, bucket, 0, 0, 1, failed)
		}

		// Mark as aggregated
		if _, err := tx.Exec("UPDATE worker SET aggregated_at = now() WHERE worker_id = $1", w.workerID); err != nil {
			return fmt.Errorf("mark aggregated worker %d: %w", w.workerID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit aggregation: %w", err)
	}

	log.Printf("📊 Aggregated flavor stats for %d workers", len(workers))
	return nil
}

// distributeWorkerHours distributes the alive duration of a worker across hour_of_week buckets.
func distributeWorkerHours(tx *sql.Tx, flavorID, regionID int32, start, end time.Time) {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return
	}

	cursor := start
	for cursor.Before(end) {
		bucket := hourOfWeek(cursor)
		nextHour := cursor.Truncate(time.Hour).Add(time.Hour)
		if nextHour.After(end) {
			nextHour = end
		}
		hours := nextHour.Sub(cursor).Hours()
		if hours > 0 {
			upsertFlavorStat(tx, flavorID, regionID, bucket, hours, 0, 0, 0)
		}
		cursor = nextHour
	}
}

// hourOfWeek returns 0-167 for the current hour-of-week (Monday 00:00 UTC = 0).
func hourOfWeek(t time.Time) int {
	t = t.UTC()
	weekday := int(t.Weekday())
	// Convert Sunday=0..Saturday=6 to Monday=0..Sunday=6
	weekday = (weekday + 6) % 7
	return weekday*24 + t.Hour()
}

// upsertFlavorStat increments the given columns in the flavor_stats table.
func upsertFlavorStat(tx *sql.Tx, flavorID, regionID int32, hourOfWeek int, workerHours float64, evictions, deployAttempts, deployFailures int) {
	_, err := tx.Exec(`
		INSERT INTO flavor_stats (flavor_id, region_id, hour_of_week, worker_hours, evictions, deploy_attempts, deploy_failures)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (flavor_id, region_id, hour_of_week) DO UPDATE SET
			worker_hours = flavor_stats.worker_hours + EXCLUDED.worker_hours,
			evictions = flavor_stats.evictions + EXCLUDED.evictions,
			deploy_attempts = flavor_stats.deploy_attempts + EXCLUDED.deploy_attempts,
			deploy_failures = flavor_stats.deploy_failures + EXCLUDED.deploy_failures
	`, flavorID, regionID, hourOfWeek, workerHours, evictions, deployAttempts, deployFailures)
	if err != nil {
		log.Printf("⚠️ upsert flavor_stats (flavor=%d region=%d hour=%d): %v", flavorID, regionID, hourOfWeek, err)
	}
}

// isEviction returns true if the worker was likely evicted (spot instance reclaimed).
func isEviction(finalStatus string, wasCleanDeleted bool, providerFull string, providerConfig map[string]config.ProviderConfig) bool {
	if wasCleanDeleted {
		return false
	}
	// Only spot-capable providers can evict
	cfg, ok := providerConfig[providerFull]
	if !ok {
		return false
	}
	azCfg, ok := cfg.(*config.AzureConfig)
	if !ok {
		return false
	}
	if !azCfg.UseSpot {
		return false
	}
	// Worker was lost (not cleanly deleted) on a spot provider
	return finalStatus == "F" || finalStatus == "L" || finalStatus == "O"
}

// pruneWorkers hard-deletes soft-deleted workers older than retentionDays that have been aggregated.
// FK cascades on job and worker_event handle cleanup.
func pruneWorkers(db *sql.DB, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	result, err := db.Exec(`
		DELETE FROM worker
		WHERE deleted_at IS NOT NULL
		  AND aggregated_at IS NOT NULL
		  AND deleted_at < now() - ($1 || ' days')::interval
	`, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("prune workers: %w", err)
	}
	return result.RowsAffected()
}

// startFlavorStatsJobs launches the periodic aggregation and prune goroutines.
func (s *taskQueueServer) startFlavorStatsJobs() {
	// Aggregation: hourly
	go func() {
		// Run once at startup after a short delay
		time.Sleep(30 * time.Second)
		if err := aggregateFlavorStats(s.db, s.providerConfig); err != nil {
			log.Printf("⚠️ initial aggregation error: %v", err)
		}

		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := aggregateFlavorStats(s.db, s.providerConfig); err != nil {
					log.Printf("⚠️ aggregation error: %v", err)
				}
			}
		}
	}()

	// Prune: daily
	go func() {
		// Run once at startup after a short delay
		time.Sleep(1 * time.Minute)
		n, err := pruneWorkers(s.db, s.cfg.Scitq.WorkerRetention)
		if err != nil {
			log.Printf("⚠️ initial prune error: %v", err)
		} else if n > 0 {
			log.Printf("🧹 pruned %d old workers", n)
		}

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				n, err := pruneWorkers(s.db, s.cfg.Scitq.WorkerRetention)
				if err != nil {
					log.Printf("⚠️ prune error: %v", err)
				} else if n > 0 {
					log.Printf("🧹 pruned %d old workers", n)
				}
			}
		}
	}()
}
