package server

import (
	"log"
	"sync"
	"time"

	"github.com/lib/pq"
)

const DefaultAssignTrigger uint32 = 500 // 5 sec

func (s *taskQueueServer) waitForAssignEvents() {
	for {
		log.Printf("⚡ Before assignPendingTasks")
		s.assignPendingTasks()
		log.Printf("⚡ After assignPendingTasks")

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
	log.Printf("TX1: starting transaction")
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to begin transaction: %v", err)
		return
	}
	log.Printf("TX2: Transaction started")
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
	log.Printf("TX3: Got pending task number")

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
	log.Printf("TX4: Got pending task list for all workers")

	// 3️⃣ Reconcile weight memory
	s.workerWeightMemory.Range(func(workerIDRaw, taskMapRaw any) bool {
		workerID := workerIDRaw.(uint32)
		taskMap := taskMapRaw.(*sync.Map)

		taskMap.Range(func(taskIDRaw, weightRaw any) bool {
			taskID := taskIDRaw.(uint32)

			if _, exists := dbTaskPresent[taskID]; !exists {
				// Task disappeared from DB -> remove from memory
				taskMap.Delete(taskID)
				log.Printf("⚠️ Task %d removed from worker %d memory (not found in DB)", taskID, workerID)
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
	log.Printf("TX5: fix weight memory")

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
	log.Printf("TX6: new tasks added in weight memory")

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
	totalAvailableSlots := 0

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
			totalAvailableSlots += int(capacity - used)
		}
	}
	workerCapacityRows.Close()
	log.Printf("TX7: worker capacity acquired")

	if len(workerStatusMap) == 0 {
		return
	}

	// Fetch pending tasks (was // 5️⃣ Assign new pending tasks)
	taskRows, err := tx.Query(`
		SELECT task_id, step_id
		FROM task
		WHERE status = 'P'
		ORDER BY created_at ASC
		LIMIT $1
	`, totalAvailableSlots)
	if err != nil {
		log.Printf("\u26a0\ufe0f Failed to fetch pending tasks: %v", err)
		return
	}
	log.Printf("TX7b: Query to fetch task passed")

	type taskCandidate struct {
		taskID uint32
		stepID *uint32
	}
	var candidates []taskCandidate
	for taskRows.Next() {
		var tid uint32
		var stepID *uint32
		if err := taskRows.Scan(&tid, &stepID); err == nil {
			candidates = append(candidates, taskCandidate{taskID: tid, stepID: stepID})
		}
	}
	taskRows.Close()

	log.Printf("TX7c: Got %d pending tasks", len(candidates))

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

	log.Printf("TX8: new tasks selected")

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

	log.Printf("TX9: worker updated")

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit task assignment: %v", err)
	}
}
