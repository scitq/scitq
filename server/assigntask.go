package server

import (
	"database/sql"
	"log"
	"sync"

	"github.com/lib/pq"
)

func (s *taskQueueServer) assignPendingTasks() {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return
	}

	// 1️⃣ Count pending tasks
	var pendingTaskCount int
	err = tx.QueryRow(`SELECT COUNT(*) FROM task WHERE status = 'P'`).Scan(&pendingTaskCount)
	if err != nil {
		log.Printf("⚠️ Failed to count pending tasks: %v", err)
		tx.Rollback()
		return
	}
	if pendingTaskCount == 0 {
		tx.Rollback()
		return
	}

	// 2️⃣ Fetch worker capacity and running tasks
	rows, err := tx.Query(`
		SELECT w.worker_id, w.step_id, w.concurrency, w.prefetch, COUNT(t.task_id) as assigned
		FROM worker w
		LEFT JOIN task t ON t.worker_id = w.worker_id AND t.status IN ('A', 'C', 'R')
		GROUP BY w.worker_id, w.concurrency, w.prefetch
	`)
	if err != nil {
		log.Printf("⚠️ Failed to fetch workers: %v", err)
		tx.Rollback()
		return
	}

	type workerStatus struct {
		TotalCapacity float64
		UsedCapacity  float64
		StepID        *uint32
	}
	workerStatusMap := map[uint32]workerStatus{}

	for rows.Next() {
		var workerID uint32
		var stepID *uint32
		var concurrency, prefetch, assigned int
		if err := rows.Scan(&workerID, &stepID, &concurrency, &prefetch, &assigned); err != nil {
			log.Printf("⚠️ Failed to scan worker row: %v", err)
			continue
		}

		capacity := float64(concurrency + prefetch)
		used := 0.0

		if val, ok := s.workerWeightMemory.Load(int(workerID)); ok {
			weightMap := val.(*sync.Map)
			weightMap.Range(func(_, v any) bool {
				used += v.(float64)
				return true
			})
		} else {
			used = float64(assigned)
		}

		if used < capacity {
			workerStatusMap[workerID] = workerStatus{TotalCapacity: capacity, UsedCapacity: used, StepID: stepID}
		}
	}
	rows.Close()

	if len(workerStatusMap) == 0 {
		tx.Rollback()
		return
	}

	// 3️⃣ Assign tasks
	for workerID, status := range workerStatusMap {
		if pendingTaskCount == 0 {
			break
		}
		available := int(status.TotalCapacity - status.UsedCapacity)
		tasksToAssign := min(available, pendingTaskCount)

		var rows *sql.Rows
		var err error
		if status.StepID != nil {
			rows, err = tx.Query(`
				SELECT task_id FROM task
				WHERE status = 'P' AND step_id = $1
				ORDER BY created_at ASC
				LIMIT $2
			`, *status.StepID, tasksToAssign)
		} else {
			rows, err = tx.Query(`
				SELECT task_id FROM task
				WHERE status = 'P' AND step_id IS NULL
				ORDER BY created_at ASC
				LIMIT $1
			`, tasksToAssign)
		}

		if err != nil {
			log.Printf("⚠️ Failed to fetch tasks for worker %d: %v", workerID, err)
			continue
		}

		var taskIDs []int
		for rows.Next() {
			var tid int
			if err := rows.Scan(&tid); err == nil {
				taskIDs = append(taskIDs, tid)
			}
		}
		rows.Close()

		if len(taskIDs) == 0 {
			continue
		}

		_, err = tx.Exec(`
			UPDATE task SET status = 'A', worker_id = $1
			WHERE task_id = ANY($2)
		`, workerID, pq.Array(taskIDs))

		if err != nil {
			log.Printf("⚠️ Failed to assign tasks to worker %d: %v", workerID, err)
			continue
		}

		// Register weight 1.0 for newly assigned tasks
		mapped, _ := s.workerWeightMemory.LoadOrStore(int(workerID), &sync.Map{})
		weightMap := mapped.(*sync.Map)
		for _, tid := range taskIDs {
			weightMap.Store(tid, 1.0)
		}

		log.Printf("✅ Assigned %d tasks to worker %d", len(taskIDs), workerID)
		pendingTaskCount -= len(taskIDs)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit task assignment: %v", err)
	}
}

func (s *taskQueueServer) triggerAssign() {
	select {
	case s.assignTrigger <- struct{}{}:
	default:
		// Already triggered, no need to push again
	}
}
