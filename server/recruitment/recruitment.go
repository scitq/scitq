package recruitment

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

type RecruiterEntry struct {
	StepID         int
	Rank           int
	TimeoutSeconds int
	Flavor         string
	Provider       string
	Region         string
	Concurrency    int
	Prefetch       int
	MaxWorkers     int
	Rounds         int
	LastAttempt    time.Time
}

type RecruiterTracker struct {
	lastSeen sync.Map // map[int]time.Time keyed by step_id
}

func NewRecruiterTracker() *RecruiterTracker {
	return &RecruiterTracker{lastSeen: sync.Map{}}
}

func (rt *RecruiterTracker) LastSeen(stepID int) (time.Time, bool) {
	v, ok := rt.lastSeen.Load(stepID)
	if !ok {
		return time.Time{}, false
	}
	return v.(time.Time), true
}

func (rt *RecruiterTracker) UpdateSeen(stepID int) {
	rt.lastSeen.Store(stepID, time.Now())
}

type WorkerCreator interface {
	CreateWorker(flavor, provider, region string, concurrency, prefetch int) error
}

func RunRecruitmentLoop(ctx context.Context, db *sql.DB, tracker *RecruiterTracker, creator WorkerCreator, qm *QuotaManager) error {
	q := `
	WITH pending_by_step AS (
		SELECT step_id, COUNT(*) AS pending_tasks
		FROM task
		WHERE status = 'P'
		GROUP BY step_id
	),
	active_workers AS (
		SELECT step_id, COUNT(*) AS active
		FROM worker
		WHERE status IN ('R','P')
		GROUP BY step_id
	),
	recyclable_workers AS (
		SELECT w.worker_id, w.step_id, w.workflow_id
		FROM worker w
		JOIN step s ON s.step_id = w.step_id
		LEFT JOIN task t ON t.worker_id = w.worker_id
		GROUP BY w.worker_id, w.step_id, w.workflow_id, w.concurrency, s.workflow_id, w.recyclable_scope
		HAVING COUNT(*) FILTER (WHERE t.status = 'P') = 0
		   AND COUNT(*) FILTER (WHERE t.status = 'R') < w.concurrency
		   AND (
			 w.recyclable_scope = 'G'
			 OR (w.recyclable_scope = 'W' AND w.workflow_id = s.workflow_id)
		   )
	),
	recyclable_map AS (
		SELECT step_id, array_agg(worker_id) AS worker_ids
		FROM recyclable_workers
		GROUP BY step_id
	),
	recyclable_count AS (
		SELECT step_id, COUNT(*) AS recyclable
		FROM recyclable_workers
		GROUP BY step_id
	)
	SELECT r.step_id, r.rank, r.timeout, r.worker_flavor, r.worker_provider, r.worker_region,
	       r.worker_concurrency, r.worker_prefetch, r.maximum_worker, r.rounds,
	       COALESCE(p.pending_tasks, 0) as pending_tasks,
	       COALESCE(a.active, 0) as active_workers,
	       COALESCE(rc.recyclable, 0) as recyclable_workers,
	       COALESCE(rm.worker_ids, ARRAY[]::int[]) as recyclable_worker_ids
	FROM recruiter r
	JOIN step s ON r.step_id = s.step_id
	LEFT JOIN pending_by_step p ON r.step_id = p.step_id
	LEFT JOIN active_workers a ON r.step_id = a.step_id
	LEFT JOIN recyclable_count rc ON r.step_id = rc.step_id
	LEFT JOIN recyclable_map rm ON r.step_id = rm.step_id;
	`

	type rowData struct {
		RecruiterEntry
		PendingTasks        int
		ActiveWorkers       int
		RecyclableWorkers   int
		RecyclableWorkerIDs []int
	}

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("failed to query recruiter table: %w", err)
	}
	defer rows.Close()

	now := time.Now()

	for rows.Next() {
		r := rowData{}
		err := rows.Scan(&r.StepID, &r.Rank, &r.TimeoutSeconds, &r.Flavor, &r.Provider, &r.Region,
			&r.Concurrency, &r.Prefetch, &r.MaxWorkers, &r.Rounds, &r.PendingTasks, &r.ActiveWorkers,
			&r.RecyclableWorkers, &r.RecyclableWorkerIDs)
		if err != nil {
			return fmt.Errorf("failed to scan recruiter row: %w", err)
		}

		if r.PendingTasks == 0 || r.Rounds <= 0 || r.Concurrency <= 0 {
			continue
		}

		expected := r.PendingTasks / (r.Concurrency * r.Rounds)
		if expected == 0 {
			expected = 1
		}
		if r.MaxWorkers > 0 && expected > r.MaxWorkers {
			expected = r.MaxWorkers
		}

		needed := expected - r.ActiveWorkers

		fmt.Printf("Recycling check for step %d (%d pending tasks, %d active workers, %d recyclable workers)\n",
			r.StepID, r.PendingTasks, r.ActiveWorkers, r.RecyclableWorkers)
		if len(r.RecyclableWorkerIDs) > 0 {
			for _, wid := range r.RecyclableWorkerIDs {
				fmt.Printf("Would recycle worker %d to step %d (new concurrency: %d)\n", wid, r.StepID, r.Concurrency)
				// updateWorkerStep(wid, r.StepID, r.Concurrency, r.Prefetch)
			}
			tracker.UpdateSeen(r.StepID)
			needed -= len(r.RecyclableWorkerIDs)
		}

		if needed <= 0 {
			continue
		}

		lastSeen, ok := tracker.LastSeen(r.StepID)
		if !ok || now.Sub(lastSeen) >= time.Duration(r.TimeoutSeconds)*time.Second {
			tracker.UpdateSeen(r.StepID)
			for i := 0; i < needed; i++ {
				if !qm.CanLaunch(r.Region, r.Provider, int32(r.Concurrency), float32(r.Prefetch)) {
					fmt.Printf("⚠️ Quota exceeded for step %d (%s/%s)\n", r.StepID, r.Provider, r.Region)
					continue
				}
				fmt.Printf("Would create worker for step %d with flavor %s (%d/%d needed)\n",
					r.StepID, r.Flavor, i+1, needed)
				err := creator.CreateWorker(r.Flavor, r.Provider, r.Region, r.Concurrency, r.Prefetch)
				if err != nil {
					fmt.Printf("⚠️ Failed to create worker: %v\n", err)
				}
			}
		} else {
			fmt.Printf("Timeout not reached for step %d — skipping creation but recycling still active\n", r.StepID)
		}
	}

	return nil
}
