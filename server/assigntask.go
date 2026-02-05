package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"
	ws "github.com/scitq/scitq/server/websocket"
)

const DefaultAssignTrigger int32 = 500 // 5 sec

func (s *taskQueueServer) waitForAssignEvents(context context.Context) {
	for {
		s.assignPendingTasks()

		for counter := int32(0); counter <= s.assignTrigger; counter++ {
			time.Sleep(10 * time.Millisecond)
		}

		s.assignTrigger = DefaultAssignTrigger
		select {
		case <-context.Done():
			return
		default:
		}
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

	// 1️⃣ Count pending tasks eligible for scheduling
	var pendingTaskCount int
	err = tx.QueryRow(`
		SELECT COUNT(*)
		FROM task t
		LEFT JOIN step s ON s.step_id = t.step_id
		LEFT JOIN workflow w ON w.workflow_id = s.workflow_id
		WHERE t.status = 'P'
		  AND (t.step_id IS NULL OR t.step_id = 0 OR w.status = 'R')
	`).Scan(&pendingTaskCount)
	if err != nil {
		log.Printf("⚠️ Failed to count pending tasks: %v", err)
		return
	}
	if pendingTaskCount == 0 {
		return
	}

	// 2️⃣ Fetch workers and their capacities
	workerCapacityRows, err := tx.Query(`
		SELECT w.worker_id, COALESCE(w.step_id,0), w.concurrency+w.prefetch-COALESCE(SUM(t.weight),0) AS capacity
		FROM worker w
		LEFT JOIN task t ON (t.worker_id=w.worker_id AND t.status IN ('A','C','D','O','R'))
		WHERE w.status='R'
		GROUP BY w.worker_id, w.step_id, w.concurrency, w.prefetch
		HAVING COALESCE(SUM(t.weight),0) < (w.concurrency + w.prefetch)
	`)
	if err != nil {
		log.Printf("⚠️ Failed to fetch workers: %v", err)
		return
	}
	//defer workerCapacityRows.Close()

	workerCapacity := map[int32]int{}    // worker_id -> available slots
	stepWorkerMap := map[int32][]int32{} // step_id -> list of worker_id
	stepSlots := map[int32]int{}         // step_id -> total available slots
	for workerCapacityRows.Next() {
		var workerID, stepID int32
		var capacity float64
		if err := workerCapacityRows.Scan(&workerID, &stepID, &capacity); err != nil {
			log.Printf("⚠️ Failed to scan worker row: %v", err)
			continue
		}

		// Prevent rounding issues from leaving near-integer capacities unused.
		rounded := int(math.Floor(capacity + 0.01))
		if rounded <= 0 {
			continue
		}

		workerCapacity[workerID] = rounded
		stepWorkerMap[stepID] = append(stepWorkerMap[stepID], workerID)
		stepSlots[stepID] += rounded
	}
	workerCapacityRows.Close()

	if len(stepWorkerMap) == 0 {
		log.Printf("No steps with worker with capacity to take new tasks")
		return
	}

	// Fetch pending tasks (was // 5️⃣ Assign new pending tasks)
	queryParts := []string{}
	args := []interface{}{}
	argIndex := 1

	for stepID, slots := range stepSlots {
		part := fmt.Sprintf(`
			(SELECT t.task_id, COALESCE(t.step_id,0)
			FROM task t
			LEFT JOIN step s ON s.step_id = t.step_id
			LEFT JOIN workflow w ON w.workflow_id = s.workflow_id
			WHERE t.status = 'P'
			  AND COALESCE(t.step_id,0) = $%d
			  AND (t.step_id IS NULL OR t.step_id = 0 OR w.status = 'R')
			ORDER BY t.created_at
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

	stepPendingTasks := make(map[int32][]int32) // step_id -> list of pending task_id
	for taskRows.Next() {
		var taskID int32
		var stepID int32
		if err := taskRows.Scan(&taskID, &stepID); err == nil {
			stepPendingTasks[stepID] = append(stepPendingTasks[stepID], taskID)
		}
	}
	taskRows.Close()

	// Assign tasks
	for stepID, _ := range stepSlots {
		// Match step_id
		for _, workerID := range stepWorkerMap[stepID] {
			// pick slot tasks in step pending task pool
			available := workerCapacity[workerID]
			taskCount := min(available, len(stepPendingTasks[stepID]))
			pendingTasks := stepPendingTasks[stepID][:taskCount]

			rows, err := tx.Query(`
				UPDATE task t
				SET status = 'A', worker_id = $1
				WHERE task_id = ANY($2)
				RETURNING task_id, step_id, (
				SELECT workflow_id FROM step s WHERE s.step_id = t.step_id
				)
			`, workerID, pq.Array(pendingTasks))
			if err != nil {
				log.Printf("\u26a0\ufe0f Failed to assign tasks to worker %d: %v", workerID, err)
				continue
			}

			var taskNum int
			for rows.Next() {
				taskNum++
				var tid int32
				var stepID, workflowID sql.NullInt32
				if err := rows.Scan(&tid, &stepID, &workflowID); err != nil {
					continue
				}
				if stepID.Valid && workflowID.Valid {
					agg := s.stats.data[workflowID.Int32][stepID.Int32]
					agg.Pending--
					agg.Accepted++
					ws.EmitWS("step-stats", workflowID.Int32, "delta", map[string]any{
						"workflowId": workflowID.Int32,
						"stepId":     stepID.Int32,
						"taskId":     tid,
						"oldStatus":  "P",
						"newStatus":  "A",
					})
				}
				ws.EmitWS("task", tid, "status", map[string]any{
					"oldStatus": "P",
					"status":    "A",
					"workerId":  workerID,
				})
			}
			rows.Close()
			log.Printf("\u2705 Assigned %d tasks to worker %d", taskNum, workerID)

			stepPendingTasks[stepID] = stepPendingTasks[stepID][taskCount:]
			if len(stepPendingTasks[stepID]) == 0 {
				rows.Close()
				break
			}

		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit task assignment: %v", err)
	}
}
