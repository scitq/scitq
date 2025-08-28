package server

import (
	"context"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// Job represents a task to be executed.
type Job struct {
	JobID         uint32
	WorkerID      uint32
	WorkerName    string
	ProviderID    uint32
	Region        string
	Flavor        string
	Action        rune // "C", "D", "R"
	Retry         int
	Timeout       time.Duration
	FromRecruiter bool
}

// Start initializes the job queue.
func (s *taskQueueServer) startJobQueue() {
	s.semaphore = make(chan struct{}, defaultJobConcurrency)
}

// AddJob adds a new job and processes it immediately.
func (s *taskQueueServer) addJob(job Job) {
	go s.processJobWithTimeout(context.Background(), job)
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
		log.Printf("⚠️ Job %d timed out", job.JobID)
		// Handle timeout (e.g., mark job as failed)
		if err := s.updateJobStatus(job.JobID, "F"); err != nil {
			log.Printf("⚠️ Failed to update job status to failed: %v", err)
		}
		if job.Retry > 0 {
			job.Retry--
			s.addJob(job) // Retry job
		}
	case err := <-done:
		if err != nil {
			log.Printf("⚠️ Failed to process job %d: %v", job.JobID, err)
			if job.Retry > 0 {
				job.Retry--
				s.addJob(job) // Retry job
			} else {
				// Update job status to failed
				if err := s.updateJobStatus(job.JobID, "F"); err != nil {
					log.Printf("⚠️ Failed to update job status to failed: %v", err)
				}
			}
		} else {
			// Update job status to success
			if err := s.updateJobStatus(job.JobID, "S"); err != nil {
				log.Printf("⚠️ Failed to update job status to success: %v", err)
			} else {
				log.Printf("✅ Job %d completed successfully", job.JobID)
			}
		}
	}
}

// processJob executes the job and updates the database.
func (s *taskQueueServer) processJob(job Job) error {

	// Implement job processing logic here (create, delete, restart)
	switch job.Action {
	case 'C': // Create
		// Create the worker
		IPaddress, err := s.providers[job.ProviderID].Create(job.WorkerName, job.Flavor, job.Region, job.JobID)
		if err != nil {
			return fmt.Errorf("failed to create worker %s: %v", job.WorkerName, err)
		}
		_, err = s.db.Exec("UPDATE worker SET ipv4=$1, status='I' WHERE worker_id=$2", IPaddress, job.WorkerID)
		if err != nil {
			return fmt.Errorf("worker created but db update failed %s: %v", job.WorkerName, err)
		}
	case 'D': // Delete
		// Delete the worker
		log.Printf("🗑️ Deleting worker %s : %s", job.WorkerName, job.Region)
		err := s.providers[job.ProviderID].Delete(job.WorkerName, job.Region)
		if err != nil {
			return fmt.Errorf("failed to delete worker %s: %v", job.WorkerName, err)
		}
		// Update the worker state
		// Update the database
		var provider string
		var cpu int32
		var mem float32

		err = s.db.QueryRow(`
			DELETE FROM worker
			USING flavor f, provider p
			WHERE worker.worker_id = $1
			  AND worker.flavor_id = f.flavor_id
			  AND f.provider_id = p.provider_id
			RETURNING p.provider_name || '.' || p.config_name AS provider,
					  f.cpu,
					  f.mem
		`, job.WorkerID).Scan(&provider, &cpu, &mem)

		if err != nil {
			return fmt.Errorf("failed to delete worker %d with quota data: %v", job.WorkerID, err)
		}
		// Update quota manager
		s.qm.RegisterDelete(job.Region, provider, cpu, mem)
		// Update watchdog
		s.watchdog.WorkerDeleted(job.WorkerID)
	case 'R': // Restart
		return s.providers[job.ProviderID].Restart(job.WorkerName, job.Region)
	default:
		return fmt.Errorf("unknown action: %c", job.Action)
	}
	return nil
}

// updateJobStatus updates the job status in the database.
func (s *taskQueueServer) updateJobStatus(jobID uint32, status string) error {
	_, err := s.db.Exec("UPDATE job SET status=$1,modified_at=NOW() WHERE job_id=$2", status, jobID)
	return err
}
