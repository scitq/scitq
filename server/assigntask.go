package server

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const DefaultAssignTrigger uint32 = 500 // 5 sec

func (s *taskQueueServer) waitForAssignEvents() {
	for {
		s.assignPendingTasks()

		for counter := uint32(0); counter <= s.assignTrigger; counter++ {
			time.Sleep(10 * time.Millisecond)
		}

		s.assignTrigger = DefaultAssignTrigger
	}
}

func (s *taskQueueServer) triggerAssign() {
	s.assignTrigger = 0
}

func (s *taskQueueServer) assignPendingTasks() {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return
	}
	defer tx.Rollback()

	// 1️⃣ Count pending tasks
	var pendingTaskCount int
	err = tx.QueryRow(`SELECT COUNT(*) FROM task WHERE status = 'P'`).Scan(&pendingTaskCount)
	if err != nil {
		log.Printf("⚠️ Failed to count pending tasks: %v", err)
		return
	}
	if pendingTaskCount == 0 {
		return
	}

	// 2️⃣ Fetch actual assigned tasks
	workerTaskRows, err := tx.Query(`
		SELECT worker_id, task_id
		FROM task
		WHERE status IN ('A', 'C', 'R')
	`)
	if err != nil {
		log.Printf("⚠️ Failed to fetch assigned tasks: %v", err)
		return
	}

	// Build maps
	dbAssignments := make(map[uint32][]uint32) // worker_id → list of task_id
	dbTaskPresent := make(map[uint32]uint32)   // task_id → worker_id (reverse lookup)

	for workerTaskRows.Next() {
		var workerID, taskID uint32
		if err := workerTaskRows.Scan(&workerID, &taskID); err == nil {
			dbAssignments[workerID] = append(dbAssignments[workerID], taskID)
			dbTaskPresent[taskID] = workerID
		}
	}
	workerTaskRows.Close()

	// 3️⃣ Reconcile weight memory
	s.workerWeightMemory.Range(func(workerIDRaw, taskMapRaw any) bool {
		workerID := workerIDRaw.(uint32)
		taskMap := taskMapRaw.(*sync.Map)

		taskMap.Range(func(taskIDRaw, weightRaw any) bool {
			taskID := taskIDRaw.(uint32)

			if _, exists := dbTaskPresent[taskID]; !exists {
				// Task disappeared from DB -> remove from memory
				taskMap.Delete(taskID)
				// log.Printf("⚠️ Task %d removed from worker %d memory (not found in DB)", taskID, workerID)
			}
			return true
		})

		// After cleanup: if empty, remove whole worker
		isEmpty := true
		taskMap.Range(func(_, _ any) bool {
			isEmpty = false
			return false
		})
		if isEmpty {
			s.workerWeightMemory.Delete(workerID)
		}

		return true
	})

	// Add missing tasks into memory
	for workerID, taskIDs := range dbAssignments {
		val, _ := s.workerWeightMemory.LoadOrStore(workerID, &sync.Map{})
		taskMap := val.(*sync.Map)

		for _, taskID := range taskIDs {
			_, exists := taskMap.Load(taskID)
			if !exists {
				// Task missing in memory -> add with weight 1.0 and warn
				taskMap.Store(taskID, 1.0)
				log.Printf("⚠️ Task %d assigned to worker %d missing in memory, added with weight 1.0", taskID, workerID)
			}
		}
	}

	// 4️⃣ Fetch workers and their capacities
	workerCapacityRows, err := tx.Query(`
		SELECT worker_id, step_id, concurrency, prefetch
		FROM worker
	`)
	if err != nil {
		log.Printf("⚠️ Failed to fetch workers: %v", err)
		return
	}
	//defer workerCapacityRows.Close()

	type workerStatus struct {
		TotalCapacity float64
		UsedCapacity  float64
		StepID        *uint32
	}

	workerStatusMap := map[uint32]workerStatus{}

	for workerCapacityRows.Next() {
		var workerID uint32
		var stepID *uint32
		var concurrency, prefetch uint32
		if err := workerCapacityRows.Scan(&workerID, &stepID, &concurrency, &prefetch); err != nil {
			log.Printf("⚠️ Failed to scan worker row: %v", err)
			continue
		}

		capacity := float64(concurrency + prefetch)
		used := 0.0

		if val, ok := s.workerWeightMemory.Load(workerID); ok {
			weightMap := val.(*sync.Map)
			weightMap.Range(func(_, v any) bool {
				used += v.(float64)
				return true
			})
		}

		if used < capacity {
			workerStatusMap[workerID] = workerStatus{
				TotalCapacity: capacity,
				UsedCapacity:  used,
				StepID:        stepID,
			}
		}
	}
	workerCapacityRows.Close()

	if len(workerStatusMap) == 0 {
		return
	}

	stepSlots := map[uint32]int{} // step_id -> available slots
	for _, status := range workerStatusMap {
		if status.StepID != nil {
			stepSlots[*status.StepID] += int(status.TotalCapacity - status.UsedCapacity)
		}
	}

	if len(stepSlots) == 0 {
		log.Printf("No steps with worker with capacity to take new tasks")
		return
	}

	// Fetch pending tasks (was // 5️⃣ Assign new pending tasks)
	queryParts := []string{}
	args := []interface{}{}
	argIndex := 1

	for stepID, slots := range stepSlots {
		part := fmt.Sprintf(`
			(SELECT task_id, step_id
			FROM task
			WHERE status = 'P' AND step_id = $%d
			ORDER BY created_at
			LIMIT %d)
		`, argIndex, slots)
		queryParts = append(queryParts, part)
		args = append(args, stepID)
		argIndex++
	}

	finalQuery := strings.Join(queryParts, "\nUNION ALL\n")

	taskRows, err := tx.Query(finalQuery, args...)
	if err != nil {
		log.Printf("\u26a0\ufe0f Failed to fetch pending tasks: %v", err)
		return
	}

	type taskCandidate struct {
		taskID uint32
		stepID *uint32
	}
	var candidates []taskCandidate
	for taskRows.Next() {
		var tid uint32
		var stepID *uint32
		var stepIDproxy sql.NullInt32
		if err := taskRows.Scan(&tid, &stepIDproxy); err == nil {
			if stepIDproxy.Valid {
				s := uint32(stepIDproxy.Int32)
				stepID = &s
			} else {
				stepID = nil
			}
			candidates = append(candidates, taskCandidate{taskID: tid, stepID: stepID})
		}
	}
	taskRows.Close()

	// Assign tasks in memory
	assignments := make(map[uint32][]uint32) // workerID -> list of taskID
	for _, candidate := range candidates {
		for workerID, status := range workerStatusMap {
			// Match step_id
			if (status.StepID == nil && candidate.stepID == nil) || (status.StepID != nil && candidate.stepID != nil && *status.StepID == *candidate.stepID) {
				if int(status.TotalCapacity-status.UsedCapacity) > 0 {
					assignments[workerID] = append(assignments[workerID], candidate.taskID)
					status.UsedCapacity++
					workerStatusMap[workerID] = status
					break
				}
			}
		}
	}

	// Perform DB update
	for workerID, taskIDs := range assignments {
		if len(taskIDs) == 0 {
			continue
		}
		_, err := tx.Exec(`
			UPDATE task SET status = 'A', worker_id = $1
			WHERE task_id = ANY($2)
		`, workerID, pq.Array(taskIDs))
		if err != nil {
			log.Printf("\u26a0\ufe0f Failed to assign tasks to worker %d: %v", workerID, err)
			continue
		}
		// Update memory
		val, _ := s.workerWeightMemory.LoadOrStore(workerID, &sync.Map{})
		weightMap := val.(*sync.Map)
		for _, tid := range taskIDs {
			weightMap.Store(tid, 1.0)
		}
		log.Printf("\u2705 Assigned %d tasks to worker %d", len(taskIDs), workerID)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit task assignment: %v", err)
	}
}
