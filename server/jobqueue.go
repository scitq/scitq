package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/providers"
	ws "github.com/scitq/scitq/server/websocket"
	"google.golang.org/protobuf/proto"
)

// Job represents a task to be executed.
type Job struct {
	JobID         int32
	WorkerID      int32
	WorkerName    string
	ProviderID    int32
	ProviderName  string
	Region        string
	Flavor        string
	// HasGPU is the recruited flavor's has_gpu signal (looked up from
	// the flavor catalog at job-creation time). The provider's Create
	// uses it to swap to the operator's `gpu_image` config when set.
	// false for CPU-only flavors and when the catalog row is missing.
	HasGPU        bool
	Action        rune // "C", "D", "R"
	Retry         int
	Timeout       time.Duration
	FromRecruiter bool
}

// Start initializes the job queue.
func (s *taskQueueServer) startJobQueue() {
	s.semaphore = make(chan struct{}, defaultJobConcurrency)
	s.activeJobs = sync.Map{}
}

// AddJob adds a new job and processes it immediately.
func (s *taskQueueServer) addJob(job Job) {
	ctx, cancel := context.WithCancel(context.Background())
	s.activeJobs.Store(job.JobID, cancel)
	go func() {
		defer s.activeJobs.Delete(job.JobID)
		s.processJobWithTimeout(ctx, job)
	}()
}

// processJobWithTimeout processes the job with a timeout.
func (s *taskQueueServer) processJobWithTimeout(ctx context.Context, job Job) {
	// Acquire a slot in the semaphore
	s.semaphore <- struct{}{}
	defer func() { <-s.semaphore }()

	// Update job status to running
	if err := s.updateJobStatus(job.JobID, "R"); err != nil {
		log.Printf("⚠️ Failed to update job status to running: %v", err)
	}

	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()

	// Channel to receive the result or timeout
	done := make(chan error, 1)

	// Goroutine to execute the job
	go func() {
		done <- s.processJob(job)
	}()

	select {
	case <-ctx.Done():
		if ctx.Err() == context.Canceled {
			log.Printf("🛑 Job %d cancelled", job.JobID)
			if err := s.updateJobStatus(job.JobID, "X"); err != nil {
				log.Printf("⚠️ Failed to update job status to cancelled: %v", err)
			}
		} else {
			log.Printf("⚠️ Job %d timed out", job.JobID)
			s.failJob(job.JobID, ctx.Err())
			if job.Retry > 0 {
				job.Retry--
				s.addJob(job) // Retry job
			}
		}
	case err := <-done:
		if err != nil {
			log.Printf("⚠️ Failed to process job %d: %v", job.JobID, err)
			class := classifyProviderError(err)

			// Sentinel errors with side-effects (quota / blacklist
			// learning) are handled first; they're always non-retryable.
			switch {
			case errors.Is(err, providers.ErrInstanceLimitReached):
				log.Printf("🚫 Job %d: instance limit reached for %s/%s — not retrying", job.JobID, job.Region, job.ProviderName)
				s.qm.RegisterInstanceLimit(s.db, job.Region, job.ProviderName)
				s.failJobWithClass(job.JobID, class, err)
			case errors.Is(err, providers.ErrUnsupportedFlavor):
				log.Printf("🚫 Job %d: flavor %s unsupported in %s/%s — blacklisting, not retrying", job.JobID, job.Flavor, job.Region, job.ProviderName)
				// In-memory blacklist (covers the rest of this server's
				// lifetime).
				s.qm.BlacklistFlavor(job.Region, job.ProviderName, job.Flavor)
				// Persist so the decision survives a restart. Provider
				// returning ErrUnsupportedFlavor is the one signal we
				// can confidently auto-disable on — Azure won't change
				// its mind on a "VM size unavailable in this region"
				// answer. Operator can re-enable via
				// `scitq flavor enable` if they want to retry.
				s.persistFlavorBlacklist(job.WorkerID, job.ProviderName, job.Region, job.Flavor)
				s.failJobWithClass(job.JobID, class, err)
			case isNonRetryable(class):
				// 2026-05-05 incident: auth errors persist until the
				// operator rotates credentials. Retrying just floods
				// the log and the job table with identical failures
				// for hours. Mark F immediately so the UI banner +
				// `scitq job list` light up within seconds.
				log.Printf("🚫 Job %d: %s error is non-retryable (%s) — marking F immediately", job.JobID, class, err.Error())
				s.failJobWithClass(job.JobID, class, err)
			case job.Retry > 0:
				job.Retry--
				s.addJob(job) // Retry job
			default:
				s.failJobWithClass(job.JobID, class, err)
			}
		} else {
			if err := s.updateJobStatus(job.JobID, "S"); err != nil {
				log.Printf("⚠️ Failed to update job status to success: %v", err)
			} else {
				log.Printf("✅ Job %d completed successfully", job.JobID)
			}
		}
	}
}

// failJob is the convenience form for cases where we don't yet know
// the class; it classifies the error and delegates.
func (s *taskQueueServer) failJob(jobID int32, err error) {
	s.failJobWithClass(jobID, classifyProviderError(err), err)
}

// failJobWithClass marks a job F and persists the provider error class
// + raw message so the UI / CLI / banner can surface them. errMsg is
// truncated to 4 KiB to keep the column from growing unbounded on
// very chatty providers.
func (s *taskQueueServer) failJobWithClass(jobID int32, class string, err error) {
	const maxLen = 4096
	msg := ""
	if err != nil {
		msg = err.Error()
		if len(msg) > maxLen {
			msg = msg[:maxLen] + "...[truncated]"
		}
	}
	if _, dbErr := s.db.Exec(`
		UPDATE job
		   SET status        = 'F',
		       modified_at   = NOW(),
		       error_class   = NULLIF($2, ''),
		       error_message = NULLIF($3, '')
		 WHERE job_id = $1
	`, jobID, class, msg); dbErr != nil {
		log.Printf("⚠️ Failed to update job %d to F (class=%q): %v", jobID, class, dbErr)
		return
	}
	// Mirror the WS notification updateJobStatus emits — same field
	// names and types so the UI's existing job.updated handler can pick
	// up errorClass / errorMessage as optional extras without needing
	// to know about a second message shape.
	failed := "F"
	ws.EmitWS("job", jobID, "updated", struct {
		JobId        int32     `json:"jobId"`
		Status       *string   `json:"status,omitempty"`
		ErrorClass   *string   `json:"errorClass,omitempty"`
		ErrorMessage *string   `json:"errorMessage,omitempty"`
		ModifiedAt   time.Time `json:"modifiedAt"`
	}{
		JobId:        jobID,
		Status:       &failed,
		ErrorClass:   nullIfEmpty(class),
		ErrorMessage: nullIfEmpty(msg),
		ModifiedAt:   time.Now(),
	})
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// persistFlavorBlacklist sets flavor_region.available=false for a
// (provider, region, flavor) that the cloud provider has hard-rejected
// (ErrUnsupportedFlavor). Reuses the same column the operator's
// `scitq flavor disable` flips, so re-enabling is the same one-line
// CLI. Best-effort: any DB error is logged but doesn't block the job
// failure path. See specs/recruitment-followups for the rationale —
// this is the *only* error class we auto-blacklist on, by design.
//
// providerName is the canonical "providerName.configName" form (e.g.
// "azure.primary") that's stored on the Job struct.
func (s *taskQueueServer) persistFlavorBlacklist(workerID int32, providerName, region, flavorName string) {
	parts := strings.SplitN(providerName, ".", 2)
	if len(parts) != 2 {
		log.Printf("⚠️ persistFlavorBlacklist: unexpected provider name %q (want providerName.configName)", providerName)
		return
	}
	pName, cName := parts[0], parts[1]

	res, err := s.db.Exec(`
		UPDATE flavor_region
		   SET available = false
		 WHERE flavor_id IN (
		       SELECT f.flavor_id FROM flavor f
		       JOIN provider p ON f.provider_id = p.provider_id
		        WHERE f.flavor_name = $1
		          AND p.provider_name = $2
		          AND p.config_name   = $3
		 )
		   AND region_id IN (
		       SELECT r.region_id FROM region r
		       JOIN provider p ON r.provider_id = p.provider_id
		        WHERE r.region_name = $4
		          AND p.provider_name = $2
		          AND p.config_name   = $3
		 )
		   AND available = true
	`, flavorName, pName, cName, region)
	if err != nil {
		log.Printf("⚠️ persistFlavorBlacklist: %s in %s/%s: %v", flavorName, providerName, region, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Already disabled (or row absent). Nothing to do.
		return
	}
	log.Printf("🚫 Auto-disabled flavor %s in %s/%s (%d row(s) — provider rejected as unsupported)",
		flavorName, providerName, region, n)
	// Audit-trail event tied to the worker that surfaced the rejection.
	// 'I' level: the recruiter / operator can re-enable the flavor any
	// time, so this isn't an error — it's a *decision* the system made.
	if workerID > 0 {
		if _, evErr := s.db.Exec(`
			INSERT INTO worker_event (worker_id, event_class, level, message, details_json)
			VALUES ($1, 'lifecycle', 'I', $2, $3::jsonb)
		`, workerID,
			fmt.Sprintf("auto-disabled flavor %s in %s/%s (provider rejected as unsupported)", flavorName, providerName, region),
			fmt.Sprintf(`{"flavor":"%s","provider":"%s","region":"%s","reason":"unsupported_flavor","action":"set flavor_region.available=false"}`,
				flavorName, providerName, region)); evErr != nil {
			log.Printf("⚠️ persistFlavorBlacklist: failed to record audit event for worker %d: %v", workerID, evErr)
		}
	}
}

// processJob executes the job and updates the database.
func (s *taskQueueServer) processJob(job Job) error {

	// Implement job processing logic here (create, delete, restart)
	switch job.Action {
	case 'C': // Create
		// Create the worker
		IPaddress, err := s.providers[job.ProviderID].Create(job.WorkerName, job.Flavor, job.Region, job.HasGPU, job.JobID)
		if err != nil {
			return fmt.Errorf("failed to create worker %s: %w", job.WorkerName, err)
		}
		_, err = s.db.Exec("UPDATE worker SET ipv4=$1, status='I' WHERE worker_id=$2 AND deleted_at IS NULL", IPaddress, job.WorkerID)
		if err != nil {
			return fmt.Errorf("worker created but db update failed %s: %v", job.WorkerName, err)
		}
	case 'D': // Delete
		// Cancel any pending 'C' jobs for the same worker
		s.activeJobs.Range(func(key, value interface{}) bool {
			cancelFunc, ok := value.(context.CancelFunc)
			if !ok {
				return true
			}
			jobID, ok := key.(int32)
			if !ok {
				return true
			}
			// We need to find the job details for this jobID to check WorkerID and Action
			// Since we don't have direct access here, let's assume we have a method to get job details by jobID:
			// For this example, assume s.getJobByID(jobID) returns (Job, bool)
			if pendingJob, found := s.getJobByID(jobID); found {
				if pendingJob.WorkerID == job.WorkerID && pendingJob.Action == 'C' {
					log.Printf("🛑 Cancelling creation job %d for worker %d", jobID, job.WorkerID)
					cancelFunc()
					// Mark the job as cancelled in DB
					if err := s.updateJobStatus(jobID, "X"); err != nil {
						log.Printf("⚠️ Failed to mark job %d as cancelled: %v", jobID, err)
					}
				}
			}
			return true
		})

		// Delete the worker
		log.Printf("🗑️ Deleting worker %s : %s", job.WorkerName, job.Region)

		provider := s.providers[job.ProviderID]
		if provider == nil {
			return fmt.Errorf("provider %d not found for worker %s", job.ProviderID, job.WorkerName)
		}

		err := provider.Delete(job.WorkerName, job.Region)
		if err != nil {
			// Delete failed — check if the instance is actually gone at provider level
			instances, listErr := provider.List(job.Region)
			if listErr == nil {
				if _, stillExists := instances[job.WorkerName]; !stillExists {
					log.Printf("♻️ worker %s delete returned error but instance is gone at provider level, proceeding", job.WorkerName)
					err = nil // instance is gone, proceed with DB cleanup
				}
			}
			if err != nil {
				return fmt.Errorf("failed to delete worker %s: %v", job.WorkerName, err)
			}
		}
		// Provider undeploy succeeded, now finalize deletion in DB via server.DeleteWorker with undeployed=true
		if _, derr := s.DeleteWorker(context.Background(), &pb.WorkerDeletion{WorkerId: job.WorkerID, Undeployed: proto.Bool(true)}); derr != nil {
			return fmt.Errorf("provider undeployed worker %d but DB deletion failed: %v", job.WorkerID, derr)
		}
	case 'R': // Restart
		return s.providers[job.ProviderID].Restart(job.WorkerName, job.Region)
	default:
		return fmt.Errorf("unknown action: %c", job.Action)
	}
	return nil
}

// updateJobStatus updates the job status in the database.
func (s *taskQueueServer) updateJobStatus(jobID int32, status string) error {
	_, err := s.db.Exec("UPDATE job SET status=$1,modified_at=NOW() WHERE job_id=$2", status, jobID)

	// Emit a normalized job.updated event, now including modifiedAt
	ws.EmitWS("job", jobID, "updated", struct {
		JobId      int32     `json:"jobId"`
		Status     *string   `json:"status,omitempty"`
		ModifiedAt time.Time `json:"modifiedAt"`
	}{
		JobId:      jobID,
		Status:     &status,
		ModifiedAt: time.Now(),
	})

	return err
}
