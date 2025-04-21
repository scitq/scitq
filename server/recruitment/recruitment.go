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

func RunRecruitmentLoop(ctx context.Context, db *sql.DB, tracker *RecruiterTracker) error {
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
	)
	SELECT r.step_id, r.rank, r.timeout, r.worker_flavor, r.worker_provider, r.worker_region,
	       r.worker_concurrency, r.worker_prefetch, r.maximum_worker, r.rounds,
	       COALESCE(p.pending_tasks, 0) as pending_tasks,
	       COALESCE(a.active, 0) as active_workers
	FROM recruiter r
	JOIN step s ON r.step_id = s.step_id
	LEFT JOIN pending_by_step p ON r.step_id = p.step_id
	LEFT JOIN active_workers a ON r.step_id = a.step_id;
	`

	type rowData struct {
		RecruiterEntry
		PendingTasks  int
		ActiveWorkers int
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
			&r.Concurrency, &r.Prefetch, &r.MaxWorkers, &r.Rounds, &r.PendingTasks, &r.ActiveWorkers)
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

		// Always attempt recycling logic here
		fmt.Printf("Recycling check for step %d (%d pending tasks, %d active workers)\n", r.StepID, r.PendingTasks, r.ActiveWorkers)
		// triggerRecycling(r.StepID)

		if needed <= 0 {
			continue
		}

		lastSeen, ok := tracker.LastSeen(r.StepID)
		if !ok || now.Sub(lastSeen) >= time.Duration(r.TimeoutSeconds)*time.Second {
			tracker.UpdateSeen(r.StepID)
			for i := 0; i < needed; i++ {
				fmt.Printf("Would create worker for step %d with flavor %s (%d/%d needed)\n",
					r.StepID, r.Flavor, i+1, needed)
				// enqueueCreateWorker(...)
			}
		} else {
			fmt.Printf("Timeout not reached for step %d — skipping creation but recycling still active\n", r.StepID)
		}
	}

	return nil
}
