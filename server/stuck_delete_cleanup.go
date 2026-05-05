package server

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"google.golang.org/protobuf/proto"
)

// stuckDeleteCleanupInterval is how often the janitor sweeps the DB
// for workers that look like the cc_v5 / E32bds_v5 incident on
// 2026-05-04: status='D' (operator or watchdog asked for delete),
// deleted_at IS NULL (the deletion never finalized in DB), and no
// active 'D' job either (so it's not still in flight, retries are
// exhausted or never were queued).
const stuckDeleteCleanupInterval = 5 * time.Minute

// stuckDeleteMinAge is the grace period before a stuck-D row is
// eligible. Avoids racing with a healthy in-progress delete that
// just hasn't reached COMMIT yet.
const stuckDeleteMinAge = 2 * time.Minute

// cleanupStuckDeletes is the periodic body. For each candidate
// worker, we ask the provider whether the VM is actually still
// there. If gone, we soft-delete via the DeleteWorker(undeployed=true)
// path so quota / watchdog state stay consistent. If still there,
// we leave it — the operator may want to re-trigger the regular
// delete, which now has the per-call timeout from #56 to keep it
// from hanging forever.
//
// This is the long-tail counterpart to:
//   - jobqueue.go:180-187 — the synchronous list-check fallback that
//     fires inside the delete job when provider.Delete returns an
//     error; covers the "delete attempt fired, then errored" case.
//   - orphan_cleanup.go — sweeps the *opposite* direction (cloud-side
//     VMs with no DB row).
//
// See specs / 2026-05-04 incident note in
// memory/project_recruitment_followups.md.
func (s *taskQueueServer) cleanupStuckDeletes() error {
	// Pick rows where:
	//   - status='D' and deleted_at IS NULL (the wedged shape)
	//   - not permanent (we never auto-reap permanent workers)
	//   - no 'D' job in flight (P/R) — would be racing the regular path
	//   - either no 'D' job exists at all (manual SQL pushed the row to
	//     'D' without queueing a job), or the latest 'D' job has been
	//     in a terminal state (F/X/S) for at least stuckDeleteMinAge,
	//     so we don't pre-empt a job that just transitioned.
	graceSec := fmt.Sprintf("%d seconds", int(stuckDeleteMinAge.Seconds()))
	rows, err := s.db.Query(`
		SELECT w.worker_id, w.worker_name, r.region_name, r.provider_id
		FROM worker w
		JOIN region r ON r.region_id = w.region_id
		WHERE w.status = 'D'
		  AND w.deleted_at IS NULL
		  AND NOT w.is_permanent
		  AND NOT EXISTS (
		      SELECT 1 FROM job j
		       WHERE j.worker_id = w.worker_id
		         AND j.action = 'D'
		         AND j.status IN ('P','R')
		  )
		  AND (
		      NOT EXISTS (
		          SELECT 1 FROM job j
		           WHERE j.worker_id = w.worker_id AND j.action = 'D'
		      )
		      OR (
		          SELECT max(modified_at) FROM job j
		           WHERE j.worker_id = w.worker_id AND j.action = 'D'
		      ) < now() - $1::interval
		  )
	`, graceSec)
	if err != nil {
		return fmt.Errorf("query stuck-D workers: %w", err)
	}
	defer rows.Close()

	type stuck struct {
		workerID   int32
		workerName string
		region     string
		providerID int32
	}
	var candidates []stuck
	for rows.Next() {
		var c stuck
		if err := rows.Scan(&c.workerID, &c.workerName, &c.region, &c.providerID); err != nil {
			return fmt.Errorf("scan stuck-D row: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate stuck-D rows: %w", err)
	}
	if len(candidates) == 0 {
		return nil
	}

	// Group by (provider, region) so we make one List call per pair
	// instead of one per worker.
	type key struct {
		providerID int32
		region     string
	}
	byPair := make(map[key][]stuck)
	for _, c := range candidates {
		byPair[key{c.providerID, c.region}] = append(byPair[key{c.providerID, c.region}], c)
	}

	for k, group := range byPair {
		provider, ok := s.providers[k.providerID]
		if !ok {
			log.Printf("⚠️ stuck-delete janitor: provider %d not registered, skipping %d worker(s)", k.providerID, len(group))
			continue
		}
		instances, err := provider.List(k.region)
		if err != nil {
			log.Printf("⚠️ stuck-delete janitor: List(%s) on provider %d failed: %v (will retry next tick)", k.region, k.providerID, err)
			continue
		}
		for _, c := range group {
			if _, present := instances[c.workerName]; present {
				// VM is genuinely there; the regular delete path has
				// to handle this. The janitor only soft-deletes
				// confirmed-absent VMs, never the reverse — that
				// would leak cost.
				log.Printf("ℹ️ stuck-delete janitor: worker %d (%s) is still present at provider; leaving for normal delete to retry", c.workerID, c.workerName)
				continue
			}
			log.Printf("🧹 stuck-delete janitor: worker %d (%s) confirmed absent at provider; soft-deleting", c.workerID, c.workerName)
			_, derr := s.DeleteWorker(context.Background(), &pb.WorkerDeletion{
				WorkerId:   c.workerID,
				Undeployed: proto.Bool(true),
			})
			if derr != nil {
				log.Printf("⚠️ stuck-delete janitor: DeleteWorker(undeployed) for %d failed: %v", c.workerID, derr)
				continue
			}
			// Audit-trail event so an operator scrolling worker_events
			// sees why the row went from D-stuck to truly deleted.
			if _, err := s.db.Exec(`
				INSERT INTO worker_event (worker_id, event_class, level, message, details_json)
				VALUES ($1, 'lifecycle', 'I', $2, $3::jsonb)
			`, c.workerID,
				"stuck-delete janitor recovered abandoned worker",
				fmt.Sprintf(`{"worker_name":"%s","region":"%s","provider_id":%d,"reason":"VM confirmed absent via provider.List"}`,
					c.workerName, c.region, c.providerID)); err != nil {
				log.Printf("⚠️ stuck-delete janitor: failed to record audit event for worker %d: %v", c.workerID, err)
			}
		}
	}
	return nil
}

// startStuckDeleteCleanup launches the periodic janitor goroutine.
// First run is staggered ~3 minutes after server start so the regular
// delete path has a chance to do its own thing on a fresh boot.
func (s *taskQueueServer) startStuckDeleteCleanup() {
	// Expose the trigger to test_seams.go so integration tests can
	// fire one tick synchronously instead of waiting on the 5-min
	// schedule.
	stuckDeleteCleanupTrigger = s.cleanupStuckDeletes
	go func() {
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(3 * time.Minute):
		}
		if err := s.cleanupStuckDeletes(); err != nil {
			log.Printf("⚠️ stuck-delete janitor (initial): %v", err)
		}
		ticker := time.NewTicker(stuckDeleteCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.cleanupStuckDeletes(); err != nil {
					log.Printf("⚠️ stuck-delete janitor: %v", err)
				}
			}
		}
	}()
}

