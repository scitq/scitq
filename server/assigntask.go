package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"path"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/scitq/scitq/fetch"
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

	// 1️⃣ Promote W→P first: catch tasks whose prerequisites just became
	// terminal (e.g. a skip-if-exists or reuse hit on a prereq committed in
	// a concurrent tx). Must happen before the pending-task count below,
	// otherwise the early-return-when-count==0 path rolls back the promotion
	// and the dependent task stays stuck in W forever — observable as the
	// "✅ promoted task N W→P" log line printing on every tick without the
	// task ever leaving W.
	s.promoteWaitingTasks(tx)

	// 2️⃣ Count pending tasks eligible for scheduling (includes any tasks
	// just promoted by promoteWaitingTasks above).
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
		// Nothing to assign, but the promotion done above must still be
		// persisted — commit before letting the deferred Rollback fire.
		if err := tx.Commit(); err != nil {
			log.Printf("⚠️ Failed to commit W→P promotions: %v", err)
		}
		return
	}

	// Opportunistic reuse: check pending tasks with reuse_key against task_reuse store (DB-only, no I/O)
	reuseEvents := s.reuseCheckTasks(tx)

	// Skip-if-exists: check all pending tasks with the flag before worker assignment
	s.skipExistingTasks(tx)

	// 2️⃣ Fetch workers and their capacities
	workerCapacityRows, err := tx.Query(`
		SELECT w.worker_id, COALESCE(w.step_id,0), w.concurrency+w.prefetch-COALESCE(SUM(t.weight),0) AS capacity
		FROM worker w
		LEFT JOIN task t ON (t.worker_id=w.worker_id AND t.status IN ('A','C','D','O','R'))
		WHERE w.status='R' AND w.deleted_at IS NULL
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
		// Commit any skip-if-exists changes before returning
		if err := tx.Commit(); err != nil {
			log.Printf("⚠️ Failed to commit skip-if-exists changes: %v", err)
		}
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
	// Collect (workflowID, stepID) pairs whose stats we need to adjust, so we
	// can apply them *after* tx.Commit succeeds. Mutating stats before commit
	// and then having the tx roll back is a known drift source (Pending can
	// go negative). Same for the WebSocket deltas — don't broadcast P→A until
	// the DB actually shows A.
	type assignedEvent struct {
		taskID     int32
		workerID   int32
		workflowID int32
		stepID     int32
	}
	var assigned []assignedEvent

	for stepID, _ := range stepSlots {
		// Match step_id
		for _, workerID := range stepWorkerMap[stepID] {
			// pick slot tasks in step pending task pool
			available := workerCapacity[workerID]
			taskCount := min(available, len(stepPendingTasks[stepID]))
			pendingTasks := stepPendingTasks[stepID][:taskCount]

			// AND status = 'P' guard: without it, a task that another
			// goroutine already moved to A would still appear in RETURNING,
			// and we'd wrongly decrement Pending again.
			rows, err := tx.Query(`
				UPDATE task t
				SET status = 'A', worker_id = $1
				WHERE task_id = ANY($2)
				  AND status = 'P'
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
					assigned = append(assigned, assignedEvent{
						taskID:     tid,
						workerID:   workerID,
						workflowID: workflowID.Int32,
						stepID:     stepID.Int32,
					})
				}
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
		// Commit failed — do NOT apply the buffered stats/WS updates.
		return
	}

	// Commit succeeded — now it's safe to adjust in-memory stats and broadcast.
	for _, a := range assigned {
		s.stats.Adjust(a.workflowID, a.stepID, func(agg *StepAgg) {
			agg.Pending--
			agg.Accepted++
		})
		ws.EmitWS("step-stats", a.workflowID, "delta", map[string]any{
			"workflowId": a.workflowID,
			"stepId":     a.stepID,
			"taskId":     a.taskID,
			"oldStatus":  "P",
			"newStatus":  "A",
		})
		ws.EmitWS("task", a.taskID, "status", map[string]any{
			"oldStatus": "P",
			"status":    "A",
			"workerId":  a.workerID,
		})
	}
	// Apply reuse-hit events from reuseCheckTasks (P→S) post-commit.
	for _, e := range reuseEvents {
		s.stats.Adjust(e.WorkflowID, e.StepID, func(agg *StepAgg) {
			agg.Pending--
			agg.Succeeded++
		})
		ws.EmitWS("step-stats", e.WorkflowID, "delta", map[string]any{
			"workflowId": e.WorkflowID,
			"stepId":     e.StepID,
			"taskId":     e.TaskID,
			"oldStatus":  "P",
			"newStatus":  "S",
		})
		ws.EmitWS("task", e.TaskID, "status", struct {
			TaskId int32  `json:"taskId"`
			Status string `json:"status"`
		}{TaskId: e.TaskID, Status: "S"})
	}
}

// skipExistingTasks checks all pending tasks with skip_if_exists=true and promotes
// them to S if their output already contains files.
func (s *taskQueueServer) skipExistingTasks(tx *sql.Tx) {
	// Find pending tasks with skip_if_exists=true and a non-empty output/publish path
	rows, err := tx.Query(`
		SELECT task_id, COALESCE(publish, output), step_id
		FROM task
		WHERE status = 'P'
		  AND skip_if_exists = TRUE
		  AND COALESCE(publish, output) IS NOT NULL
		  AND COALESCE(publish, output) <> ''
	`)
	if err != nil {
		log.Printf("⚠️ skip-if-exists: failed to query: %v", err)
		return
	}
	defer rows.Close()

	type candidate struct {
		taskID    int32
		checkPath string
		stepID    sql.NullInt32
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.taskID, &c.checkPath, &c.stepID); err != nil {
			continue
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return
	}

	skipped := map[int32]bool{}
	for _, c := range candidates {
		checkPath := c.checkPath
		// Support glob patterns: extract dir and pattern
		var globPattern string
		base := path.Base(checkPath)
		if strings.ContainsAny(base, "*?") {
			globPattern = base
			checkPath = path.Dir(checkPath) + "/"
		}

		files, err := fetch.List(s.rcloneRemotes, checkPath)
		if err != nil || len(files) == 0 {
			continue // output doesn't exist, run normally
		}

		// If glob pattern specified, filter files
		if globPattern != "" {
			matched := false
			for _, f := range files {
				name := path.Base(f)
				if ok, _ := path.Match(globPattern, name); ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Output exists — skip to S
		_, err = tx.Exec(`UPDATE task SET status = 'S' WHERE task_id = $1`, c.taskID)
		if err != nil {
			log.Printf("⚠️ skip-if-exists: failed to update task %d: %v", c.taskID, err)
			continue
		}
		log.Printf("⏭️ Task %d skipped (output exists at %s)", c.taskID, c.checkPath)
		skipped[c.taskID] = true

		// Emit WS event
		ws.EmitWS("task", c.taskID, "status", struct {
			TaskId int32  `json:"taskId"`
			Status string `json:"status"`
		}{TaskId: c.taskID, Status: "S"})
	}

	// Promote W→P for dependents whose prerequisites are now all S
	if len(skipped) > 0 {
		for taskID := range skipped {
			rows, err := tx.Query(`
				SELECT d.dependent_task_id
				FROM task_dependencies d
				JOIN task t ON d.dependent_task_id = t.task_id
				WHERE d.prerequisite_task_id = $1
				  AND t.status = 'W'
			`, taskID)
			if err != nil {
				continue
			}
			var depIDs []int32
			for rows.Next() {
				var depID int32
				if rows.Scan(&depID) == nil {
					depIDs = append(depIDs, depID)
				}
			}
			rows.Close()

			for _, depID := range depIDs {
				var allDone bool
				tx.QueryRow(`
					SELECT NOT EXISTS (
						SELECT 1
						FROM task_dependencies d
						JOIN task t ON d.prerequisite_task_id = t.task_id
						WHERE d.dependent_task_id = $1
						  AND NOT (t.status = 'S' OR (t.status = 'F' AND d.accept_failure AND t.retry = 0))
					)
				`, depID).Scan(&allDone)
				if allDone {
					tx.Exec(`UPDATE task SET status = 'P' WHERE task_id = $1 AND status = 'W'`, depID)
					log.Printf("✅ skip-if-exists: promoted task %d to P (dependencies resolved)", depID)
				}
			}
		}
		s.triggerAssign()
	}
}

// promoteWaitingTasks promotes W→P for tasks whose prerequisites are all terminal.
// This catches tasks that were submitted after a concurrent reuse/skip tx committed.
func (s *taskQueueServer) promoteWaitingTasks(tx *sql.Tx) {
	// Scan W tasks whose prerequisites are all done.
	//
	// The previous version bounded this with `created_at > NOW() - 30s`
	// as an optimisation, on the assumption that any race window between
	// task submission and prerequisite completion was "a few seconds".
	// In practice the reuse-hit redirect can race past that window —
	// observed: a task whose prerequisite was reuse-hit just after submit
	// stayed W with stale input until manually retried, then inherited
	// the same stale input. Removing the time bound makes this loop a
	// genuine safety net rather than an opportunistic catch-up.
	//
	// The workflow-status filter (only `R`unning workflows) keeps us
	// from auto-promoting tasks in suspended (`Z`) or completed
	// workflows, which the old time bound used to mask incidentally.
	rows, err := tx.Query(`
		SELECT t.task_id
		FROM task t
		LEFT JOIN step s ON s.step_id = t.step_id
		LEFT JOIN workflow w ON w.workflow_id = s.workflow_id
		WHERE t.status = 'W'
		  AND (t.step_id IS NULL OR t.step_id = 0 OR w.status = 'R')
		  AND NOT EXISTS (
			SELECT 1
			FROM task_dependencies d
			JOIN task dep ON d.prerequisite_task_id = dep.task_id
			WHERE d.dependent_task_id = t.task_id
			  AND NOT (dep.status = 'S' OR (dep.status = 'F' AND d.accept_failure AND dep.retry = 0))
		  )
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	var toPromote []int32
	for rows.Next() {
		var tid int32
		if rows.Scan(&tid) == nil {
			toPromote = append(toPromote, tid)
		}
	}

	for _, tid := range toPromote {
		s.redirectReuseInputs(tx, tid)
		tx.Exec(`UPDATE task SET status = 'P' WHERE task_id = $1 AND status = 'W'`, tid)
		log.Printf("✅ promoted task %d W→P (prerequisites resolved)", tid)
	}
	if len(toPromote) > 0 {
		s.triggerAssign()
	}
}

// reuseHitEvent describes a task that actually transitioned P→S via reuse
// (i.e. the UPDATE affected a row). The caller should apply the in-memory
// stats adjustment and broadcast the WS event only after the outer
// transaction has been committed successfully — otherwise a rollback would
// leave counters skewed (the classic source of negative Pending).
type reuseHitEvent struct {
	TaskID     int32
	WorkflowID int32
	StepID     int32
}

// reuseCheckTasks checks pending tasks with a reuse_key and promotes them to S
// if a matching entry exists in task_reuse (opportunistic reuse). Returns the
// set of tasks whose UPDATE actually fired (status was still 'P'); the caller
// is responsible for applying stats/WS changes post-commit.
func (s *taskQueueServer) reuseCheckTasks(tx *sql.Tx) []reuseHitEvent {
	var events []reuseHitEvent
	// Filter on consume_reuse so non-opportunistic workflows don't pick up
	// cached results — they still *contribute* (reuse_key is populated
	// regardless when the step is reuse-eligible) but they won't *consume*.
	// The reuse_key IS NOT NULL check stays as a no-match guard; with the
	// consume_reuse=true filter it's redundant in practice but cheap.
	rows, err := tx.Query(`
		SELECT t.task_id, t.reuse_key, t.step_id, tr.output_path, tr.task_id AS original_task_id
		FROM task t
		JOIN task_reuse tr ON t.reuse_key = tr.reuse_key
		WHERE t.status = 'P'
		  AND t.consume_reuse = true
		  AND t.reuse_key IS NOT NULL
	`)
	if err != nil {
		log.Printf("⚠️ reuse-check: failed to query: %v", err)
		return events
	}
	defer rows.Close()

	type reuseHit struct {
		taskID         int32
		reuseKey       string
		stepID         sql.NullInt32
		outputPath     string
		originalTaskID int32
	}
	var hits []reuseHit
	for rows.Next() {
		var h reuseHit
		if err := rows.Scan(&h.taskID, &h.reuseKey, &h.stepID, &h.outputPath, &h.originalTaskID); err != nil {
			continue
		}
		hits = append(hits, h)
	}

	if len(hits) == 0 {
		return events
	}

	reused := map[int32]bool{}
	for _, h := range hits {
		// Save original output before overwriting
		var originalOutput sql.NullString
		tx.QueryRow(`SELECT output FROM task WHERE task_id = $1`, h.taskID).Scan(&originalOutput)

		res, err := tx.Exec(`UPDATE task SET status = 'S', reuse_hit = true, reuse_original_output = output, output = $2 WHERE task_id = $1 AND status = 'P'`, h.taskID, h.outputPath)
		if err != nil {
			log.Printf("⚠️ reuse-check: failed to update task %d: %v", h.taskID, err)
			continue
		}
		// RowsAffected guard: if the task was no longer 'P' (raced with another
		// path), the UPDATE hits zero rows. Without this check we'd still do
		// Pending--;Succeeded++ below, which is the main source of negative
		// Pending counters observed on long workflows.
		if n, _ := res.RowsAffected(); n == 0 {
			log.Printf("ℹ️ reuse-check: task %d already transitioned, skipping", h.taskID)
			continue
		}
		log.Printf("♻️ reuse hit for task %d (key=%s…), reusing output from task %d", h.taskID, h.reuseKey[:12], h.originalTaskID)
		reused[h.taskID] = true

		// Defer the stats/WS update until after the outer tx.Commit — until
		// then the change isn't durable and shouldn't be reflected in-memory.
		if h.stepID.Valid {
			var wfID int32
			tx.QueryRow(`SELECT workflow_id FROM step WHERE step_id = $1`, h.stepID.Int32).Scan(&wfID)
			if wfID != 0 {
				events = append(events, reuseHitEvent{
					TaskID:     h.taskID,
					WorkflowID: wfID,
					StepID:     h.stepID.Int32,
				})
			}
		}

		// Redirect dependent tasks' inputs: replace old output prefix with reused output prefix
		if originalOutput.Valid && originalOutput.String != h.outputPath {
			oldPrefix := strings.TrimSuffix(originalOutput.String, "/")
			newPrefix := strings.TrimSuffix(h.outputPath, "/")
			res, redirectErr := tx.Exec(`
				UPDATE task SET input = array(
					SELECT REPLACE(elem, $2, $3)
					FROM unnest(input) AS elem
				)
				FROM task_dependencies d
				WHERE d.prerequisite_task_id = $1
				  AND task.task_id = d.dependent_task_id
				  AND task.status IN ('W','P')
				  AND EXISTS (SELECT 1 FROM unnest(task.input) e WHERE e LIKE $2 || '%')
			`, h.taskID, oldPrefix, newPrefix)
			if redirectErr != nil {
				log.Printf("⚠️ reuse: input redirect failed for task %d: %v", h.taskID, redirectErr)
			} else {
				n, _ := res.RowsAffected()
				log.Printf("♻️ reuse: redirected %d dependent inputs: %s → %s", n, oldPrefix, newPrefix)
			}
		}

		// Note: the "task status=S" WS event is emitted post-commit (see caller
		// applying reuseEvents) so clients only see the state change once it's
		// durable in the DB.
	}

	// Promote W→P for dependents (same logic as skipExistingTasks)
	if len(reused) > 0 {
		for taskID := range reused {
			depRows, err := tx.Query(`
				SELECT d.dependent_task_id
				FROM task_dependencies d
				JOIN task t ON d.dependent_task_id = t.task_id
				WHERE d.prerequisite_task_id = $1
				  AND t.status = 'W'
			`, taskID)
			if err != nil {
				continue
			}
			var depIDs []int32
			for depRows.Next() {
				var depID int32
				if depRows.Scan(&depID) == nil {
					depIDs = append(depIDs, depID)
				}
			}
			depRows.Close()

			for _, depID := range depIDs {
				var allDone bool
				tx.QueryRow(`
					SELECT NOT EXISTS (
						SELECT 1
						FROM task_dependencies d
						JOIN task t ON d.prerequisite_task_id = t.task_id
						WHERE d.dependent_task_id = $1
						  AND NOT (t.status = 'S' OR (t.status = 'F' AND d.accept_failure AND t.retry = 0))
					)
				`, depID).Scan(&allDone)
				if allDone {
					// Before promoting, fix inputs from reuse-hit prerequisites
					s.redirectReuseInputs(tx, depID)
					tx.Exec(`UPDATE task SET status = 'P' WHERE task_id = $1 AND status = 'W'`, depID)
					log.Printf("♻️ reuse: promoted task %d to P (dependencies resolved)", depID)
				}
			}
		}
		s.triggerAssign()
	}
	return events
}

// redirectReuseInputs fixes input paths of a dependent task when its prerequisites
// are reuse hits with changed output paths. Called at W→P promotion time,
// when all tasks exist in the DB.
//
// Implementation note: lib/pq does not allow another statement on the same
// connection while a result set is still being iterated — running tx.Exec
// inside `for rows.Next()` silently no-ops or aborts the tx for any prereq
// past the first. Symptom (observed): a dependent with multiple reuse-hit
// prereqs (e.g. `compile` with fastp.Nr10, fastp.Nr11, biomscope.Nr10, …)
// got at most ONE input redirected, the rest stayed pointing at the
// non-cached workspace, and the task ran with stale paths.
//
// Fix: drain rows into a slice first, close the cursor, then issue UPDATEs.
func (s *taskQueueServer) redirectReuseInputs(tx *sql.Tx, depTaskID int32) {
	type redirect struct{ oldPrefix, newPrefix string }
	var redirects []redirect
	rows, err := tx.Query(`
		SELECT t.reuse_original_output, t.output
		FROM task_dependencies d
		JOIN task t ON d.prerequisite_task_id = t.task_id
		WHERE d.dependent_task_id = $1
		  AND t.reuse_hit = true
		  AND t.reuse_original_output IS NOT NULL
		  AND t.reuse_original_output <> t.output
	`, depTaskID)
	if err != nil {
		return
	}
	for rows.Next() {
		var origOutput, newOutput string
		if rows.Scan(&origOutput, &newOutput) != nil {
			continue
		}
		redirects = append(redirects, redirect{
			oldPrefix: strings.TrimSuffix(origOutput, "/"),
			newPrefix: strings.TrimSuffix(newOutput, "/"),
		})
	}
	rows.Close()

	for _, r := range redirects {
		res, err := tx.Exec(`
			UPDATE task SET input = array(
				SELECT REPLACE(elem, $1, $2)
				FROM unnest(input) AS elem
			)
			WHERE task_id = $3
			  AND EXISTS (SELECT 1 FROM unnest(input) e WHERE e LIKE $1 || '%')
		`, r.oldPrefix, r.newPrefix, depTaskID)
		if err != nil {
			log.Printf("⚠️ reuse: input redirect failed for dependent task %d: %v", depTaskID, err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("♻️ reuse: redirected task %d inputs: %s → %s", depTaskID, r.oldPrefix, r.newPrefix)
		}
	}
}

// redirectReuseInputsDB is like redirectReuseInputs but uses s.db directly (no transaction).
// Same drain-then-exec pattern as the tx variant — see its comment for why.
func (s *taskQueueServer) redirectReuseInputsDB(ctx context.Context, depTaskID int32) {
	type redirect struct{ oldPrefix, newPrefix string }
	var redirects []redirect
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.reuse_original_output, t.output
		FROM task_dependencies d
		JOIN task t ON d.prerequisite_task_id = t.task_id
		WHERE d.dependent_task_id = $1
		  AND t.reuse_hit = true
		  AND t.reuse_original_output IS NOT NULL
		  AND t.reuse_original_output <> t.output
	`, depTaskID)
	if err != nil {
		return
	}
	for rows.Next() {
		var origOutput, newOutput string
		if rows.Scan(&origOutput, &newOutput) != nil {
			continue
		}
		redirects = append(redirects, redirect{
			oldPrefix: strings.TrimSuffix(origOutput, "/"),
			newPrefix: strings.TrimSuffix(newOutput, "/"),
		})
	}
	rows.Close()

	for _, r := range redirects {
		res, err := s.db.ExecContext(ctx, `
			UPDATE task SET input = array(
				SELECT REPLACE(elem, $1, $2)
				FROM unnest(input) AS elem
			)
			WHERE task_id = $3
			  AND EXISTS (SELECT 1 FROM unnest(input) e WHERE e LIKE $1 || '%')
		`, r.oldPrefix, r.newPrefix, depTaskID)
		if err != nil {
			log.Printf("⚠️ reuse: input redirect failed for dependent task %d: %v", depTaskID, err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("♻️ reuse: redirected task %d inputs: %s → %s", depTaskID, r.oldPrefix, r.newPrefix)
		}
	}
}

func (s *taskQueueServer) assignSingleTask(taskID int32) (int32, sql.NullInt32, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, sql.NullInt32{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var stepID sql.NullInt32
	var workflowID sql.NullInt32
	err = tx.QueryRow(`
		SELECT t.step_id, s.workflow_id
		FROM task t
		LEFT JOIN step s ON s.step_id = t.step_id
		WHERE t.task_id = $1 AND t.status = 'P'
	`, taskID).Scan(&stepID, &workflowID)
	if err == sql.ErrNoRows {
		return 0, sql.NullInt32{}, fmt.Errorf("task %d not pending", taskID)
	}
	if err != nil {
		return 0, sql.NullInt32{}, fmt.Errorf("failed to load task %d: %w", taskID, err)
	}
	if !stepID.Valid {
		return 0, workflowID, fmt.Errorf("task %d has no step_id", taskID)
	}

	var workerID sql.NullInt32
	err = tx.QueryRow(`
		WITH candidate AS (
			SELECT w.worker_id
			FROM worker w
			LEFT JOIN task t ON t.worker_id = w.worker_id AND t.status IN ('A','C','D','O','R')
			WHERE w.status = 'R' AND w.deleted_at IS NULL AND w.step_id = $2
			GROUP BY w.worker_id, w.concurrency, w.prefetch
			HAVING COALESCE(SUM(t.weight),0) < (w.concurrency + w.prefetch)
			ORDER BY (w.concurrency + w.prefetch - COALESCE(SUM(t.weight),0)) DESC, w.worker_id
			LIMIT 1
		),
		updated AS (
			UPDATE task
			SET status = 'A', worker_id = (SELECT worker_id FROM candidate)
			WHERE task_id = $1 AND status = 'P'
			  AND (SELECT worker_id FROM candidate) IS NOT NULL
			RETURNING worker_id, step_id
		)
		SELECT worker_id FROM updated
	`, taskID, stepID.Int32).Scan(&workerID)
	if err == sql.ErrNoRows {
		return 0, workflowID, fmt.Errorf("no compatible worker available for task %d", taskID)
	}
	if err != nil {
		return 0, workflowID, fmt.Errorf("failed to assign task %d: %w", taskID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, workflowID, fmt.Errorf("failed to commit task assignment: %w", err)
	}

	if workflowID.Valid {
		s.stats.Adjust(workflowID.Int32, stepID.Int32, func(agg *StepAgg) {
			agg.Pending--
			agg.Accepted++
		})
		ws.EmitWS("step-stats", workflowID.Int32, "delta", map[string]any{
			"workflowId": workflowID.Int32,
			"stepId":     stepID.Int32,
			"taskId":     taskID,
			"oldStatus":  "P",
			"newStatus":  "A",
		})
	}
	ws.EmitWS("task", taskID, "status", map[string]any{
		"oldStatus": "P",
		"status":    "A",
		"workerId":  workerID.Int32,
	})
	return workerID.Int32, workflowID, nil
}
