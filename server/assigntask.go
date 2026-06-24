package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/scitq/scitq/fetch"
	ws "github.com/scitq/scitq/server/websocket"
)

const DefaultAssignTrigger int32 = 500 // 5 sec

// workerCaps holds the per-worker flavor caps used by fitsWorker. NaN
// on any dimension means "unset — bypass the check" (legacy workers
// without a flavor row trivially accept any task). Hoisted to
// package scope so the no-fit diagnostic helpers (whyDoesNotFit,
// maybeWarnNoFit) below can share the type with assignPendingTasks.
type workerCaps struct{ cpu, mem, disk float64 }

// taskMins holds the per-task minimum resource requirements. NaN
// means "unset — task always fits" (legacy tasks without min_*).
// Same hoist rationale as workerCaps.
type taskMins struct{ cpu, mem, disk float64 }

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

	// 2️⃣ Fetch workers and their capacities + their flavor caps. The flavor
	// columns (f.cpu/mem/disk) drive the per-task fit check below: a task
	// with min_cpu/min_mem/min_disk set won't be assigned to a worker
	// whose flavor can't satisfy it (typically a retry whose curve
	// stepped past the worker's headroom — see addition_from_nextflow.md A).
	// Permanent / local workers without a flavor row come back with NULL
	// caps and bypass the fit check (today's behaviour).
	workerCapacityRows, err := tx.Query(`
		SELECT w.worker_id, COALESCE(w.step_id,0),
		       w.concurrency+w.prefetch-COALESCE(SUM(t.weight),0) AS capacity,
		       f.cpu, f.mem, f.disk
		FROM worker w
		LEFT JOIN flavor f ON f.flavor_id = w.flavor_id
		LEFT JOIN task t ON (t.worker_id=w.worker_id AND t.status IN ('A','C','D','O','R') AND NOT t.hidden)
		WHERE w.status='R' AND w.deleted_at IS NULL
		GROUP BY w.worker_id, w.step_id, w.concurrency, w.prefetch, f.cpu, f.mem, f.disk
		HAVING COALESCE(SUM(t.weight),0) < (w.concurrency + w.prefetch)
	`)
	if err != nil {
		log.Printf("⚠️ Failed to fetch workers: %v", err)
		return
	}
	//defer workerCapacityRows.Close()

	// Worker flavor caps used for per-task fit checks. NULL caps (unset on
	// the row) become NaN here so the fit check trivially passes — exactly
	// what we want for legacy workers that pre-date the per-task resource
	// fields.
	workerCapacity := map[int32]int{} // worker_id -> available slots
	workerFlavorCaps := map[int32]workerCaps{}
	stepWorkerMap := map[int32][]int32{} // step_id -> list of worker_id
	for workerCapacityRows.Next() {
		var workerID, stepID int32
		var capacity float64
		var fCPU, fMem, fDisk sql.NullFloat64
		if err := workerCapacityRows.Scan(&workerID, &stepID, &capacity, &fCPU, &fMem, &fDisk); err != nil {
			log.Printf("⚠️ Failed to scan worker row: %v", err)
			continue
		}

		// Prevent rounding issues from leaving near-integer capacities unused.
		rounded := int(math.Floor(capacity + 0.01))
		if rounded <= 0 {
			continue
		}

		workerCapacity[workerID] = rounded
		caps := workerCaps{
			cpu:  math.NaN(),
			mem:  math.NaN(),
			disk: math.NaN(),
		}
		if fCPU.Valid {
			caps.cpu = fCPU.Float64
		}
		if fMem.Valid {
			caps.mem = fMem.Float64
		}
		if fDisk.Valid {
			caps.disk = fDisk.Float64
		}
		workerFlavorCaps[workerID] = caps
		stepWorkerMap[stepID] = append(stepWorkerMap[stepID], workerID)
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

	// Assignment: each worker fetches its own per-fit set via SQL
	// predicate (smallest-worker-first sort below routes small-fitting
	// tasks to small workers before big workers can claim them).
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

	// fitsWorker returns true iff `task` (by its min_cpu/min_mem/min_disk)
	// can run on `caps`. NaN on either side bypasses the check for that
	// dimension \u2014 legacy tasks without min_* set always fit, legacy
	// workers without a flavor row always accept. Spec:
	// addition_from_nextflow.md A (per-attempt resource curves).
	fitsWorker := func(req taskMins, caps workerCaps) bool {
		if !math.IsNaN(req.cpu) && !math.IsNaN(caps.cpu) && req.cpu > caps.cpu {
			return false
		}
		if !math.IsNaN(req.mem) && !math.IsNaN(caps.mem) && req.mem > caps.mem {
			return false
		}
		if !math.IsNaN(req.disk) && !math.IsNaN(caps.disk) && req.disk > caps.disk {
			return false
		}
		return true
	}

	// Sort workers within each step ascending by flavor caps (mem,
	// then cpu, then disk). NaN-in-dim treated as +Inf (unbounded).
	// Routing smallest workers first gives them first pick of
	// small-fitting tasks \u2014 without this, a big worker can sweep up
	// tasks that would also fit a small worker, starving the small
	// one (alpha2 incident 2026-06-24 step 73888: littlebrother +
	// bigbrother at 15.6 GB sat idle while bioit at 250 GB ground
	// through mem=40 tasks, even though 2 mem=15 tasks existed
	// deeper in the queue). NaN-cap workers (legacy / permanent
	// without a flavor row) sort last: they accept any task, so they
	// drain leftovers.
	cmpDim := func(x, y float64) int {
		xUnb := math.IsNaN(x)
		yUnb := math.IsNaN(y)
		if xUnb != yUnb {
			if xUnb {
				return 1
			}
			return -1
		}
		if xUnb {
			return 0
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	}
	for stepID := range stepWorkerMap {
		wkrs := stepWorkerMap[stepID]
		sort.SliceStable(wkrs, func(i, j int) bool {
			a := workerFlavorCaps[wkrs[i]]
			b := workerFlavorCaps[wkrs[j]]
			if c := cmpDim(a.mem, b.mem); c != 0 {
				return c < 0
			}
			if c := cmpDim(a.cpu, b.cpu); c != 0 {
				return c < 0
			}
			if c := cmpDim(a.disk, b.disk); c != 0 {
				return c < 0
			}
			return false
		})
	}

	// Per-worker fetch + assign. Each worker SELECTs only tasks at
	// the step that fit ITS flavor (predicate baked into SQL),
	// oldest first, LIMIT = its own slots. Smaller workers iterate
	// first (sort above), so they claim small-fitting tasks before
	// larger workers see them. Tasks claimed by an earlier worker
	// flip P->A inside this same tx, so the next worker's SELECT
	// skips them without any in-memory bookkeeping.
	for stepID, workerIDs := range stepWorkerMap {
		for _, workerID := range workerIDs {
			available := workerCapacity[workerID]
			if available <= 0 {
				continue
			}
			caps := workerFlavorCaps[workerID]

			fetchArgs := []interface{}{stepID}
			argIdx := 2
			var conds []string
			if !math.IsNaN(caps.cpu) {
				conds = append(conds, fmt.Sprintf("(t.min_cpu IS NULL OR t.min_cpu <= $%d)", argIdx))
				fetchArgs = append(fetchArgs, caps.cpu)
				argIdx++
			}
			if !math.IsNaN(caps.mem) {
				conds = append(conds, fmt.Sprintf("(t.min_mem IS NULL OR t.min_mem <= $%d)", argIdx))
				fetchArgs = append(fetchArgs, caps.mem)
				argIdx++
			}
			if !math.IsNaN(caps.disk) {
				conds = append(conds, fmt.Sprintf("(t.min_disk IS NULL OR t.min_disk <= $%d)", argIdx))
				fetchArgs = append(fetchArgs, caps.disk)
				argIdx++
			}
			whereExtra := ""
			if len(conds) > 0 {
				whereExtra = "AND " + strings.Join(conds, " AND ")
			}
			fetchQuery := fmt.Sprintf(`
				SELECT t.task_id
				FROM task t
				LEFT JOIN step s ON s.step_id = t.step_id
				LEFT JOIN workflow w ON w.workflow_id = s.workflow_id
				WHERE t.status = 'P'
				  AND COALESCE(t.step_id,0) = $1
				  AND (t.step_id IS NULL OR t.step_id = 0 OR w.status = 'R')
				  %s
				ORDER BY t.created_at
				LIMIT %d
			`, whereExtra, available)

			taskRows, err := tx.Query(fetchQuery, fetchArgs...)
			if err != nil {
				log.Printf("\u26a0\ufe0f Failed to fetch pending tasks for worker %d: %v", workerID, err)
				continue
			}
			var fittingTasks []int32
			for taskRows.Next() {
				var tid int32
				if err := taskRows.Scan(&tid); err == nil {
					fittingTasks = append(fittingTasks, tid)
				}
			}
			taskRows.Close()

			if len(fittingTasks) == 0 {
				// Worker has capacity but no task at this step
				// fits. Sample one remaining P task at the step to
				// compute the bottleneck dimension for the no-fit
				// warning. If the step has no P tasks at all
				// (e.g. a smaller worker just drained them in this
				// same tick), skip the warning entirely.
				var sampleCpu, sampleMem, sampleDisk sql.NullFloat64
				err := tx.QueryRow(`
					SELECT t.min_cpu, t.min_mem, t.min_disk
					FROM task t
					LEFT JOIN step s ON s.step_id = t.step_id
					LEFT JOIN workflow w ON w.workflow_id = s.workflow_id
					WHERE t.status = 'P'
					  AND COALESCE(t.step_id,0) = $1
					  AND (t.step_id IS NULL OR t.step_id = 0 OR w.status = 'R')
					ORDER BY t.created_at
					LIMIT 1
				`, stepID).Scan(&sampleCpu, &sampleMem, &sampleDisk)
				if err == nil {
					req := taskMins{cpu: math.NaN(), mem: math.NaN(), disk: math.NaN()}
					if sampleCpu.Valid {
						req.cpu = sampleCpu.Float64
					}
					if sampleMem.Valid {
						req.mem = sampleMem.Float64
					}
					if sampleDisk.Valid {
						req.disk = sampleDisk.Float64
					}
					if !fitsWorker(req, caps) {
						s.maybeWarnNoFit(workerID, stepID, whyDoesNotFit(req, caps))
					}
				}
				continue
			}

			// AND status = 'P' guard: without it, a task that another
			// goroutine already moved to A would still appear in RETURNING,
			// and we'd wrongly decrement Pending again.
			upRows, err := tx.Query(`
				UPDATE task t
				SET status = 'A', worker_id = $1
				WHERE task_id = ANY($2)
				  AND status = 'P'
				RETURNING task_id, step_id, (
				SELECT workflow_id FROM step s WHERE s.step_id = t.step_id
				)
			`, workerID, pq.Array(fittingTasks))
			if err != nil {
				log.Printf("\u26a0\ufe0f Failed to assign tasks to worker %d: %v", workerID, err)
				continue
			}
			var taskNum int
			for upRows.Next() {
				taskNum++
				var tid int32
				var sid, wfid sql.NullInt32
				if err := upRows.Scan(&tid, &sid, &wfid); err != nil {
					continue
				}
				if sid.Valid && wfid.Valid {
					assigned = append(assigned, assignedEvent{
						taskID:     tid,
						workerID:   workerID,
						workflowID: wfid.Int32,
						stepID:     sid.Int32,
					})
				}
			}
			upRows.Close()
			log.Printf("\u2705 Assigned %d tasks to worker %d", taskNum, workerID)
			if taskNum > 0 {
				s.clearNoFitMemo(workerID)
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
//
// The check is one-shot per task: after a task has been examined (whether
// or not it was skipped), `skip_checked` is set TRUE so the next assign
// tick doesn't re-list the same remote path. Without this, a workflow
// with N pending non-skippable tasks generates N rclone listings every
// ~5 seconds for the entire run.
func (s *taskQueueServer) skipExistingTasks(tx *sql.Tx) {
	// Find pending tasks that haven't been skip-checked yet and have a
	// non-empty output/publish path.
	rows, err := tx.Query(`
		SELECT task_id, COALESCE(publish, output), step_id
		FROM task
		WHERE status = 'P'
		  AND skip_if_exists = TRUE
		  AND skip_checked = FALSE
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

	// Collect every candidate's ID so we can stamp skip_checked at the end
	// in one batched UPDATE. Tasks that get skipped will move to S below;
	// the flag is still set on them for consistency (harmless since the
	// status filter excludes non-P rows on the next tick).
	checkedIDs := make([]int32, 0, len(candidates))
	for _, c := range candidates {
		checkedIDs = append(checkedIDs, c.taskID)
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

	// Stamp every examined task as skip-checked in one shot. After this,
	// the WHERE clause above filters them out on subsequent ticks.
	if len(checkedIDs) > 0 {
		if _, err := tx.Exec(
			`UPDATE task SET skip_checked = TRUE WHERE task_id = ANY($1::int[])`,
			pq.Array(checkedIDs),
		); err != nil {
			log.Printf("⚠️ skip-if-exists: failed to stamp skip_checked: %v", err)
		}
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
	//
	// Also exclude skip_if_exists tasks: those are resource-driven
	// (RESOURCE_ROOT, host catalogs, etc.) and must be evaluated against
	// the *current* publish target via skipExistingTasks. A prior task with
	// the same reuse_key may have run under a different resource context
	// or against a publish target that has since been evicted — reusing
	// its cached `output` silently breaks downstream dependents that read
	// from `publish`, not `output`.
	//
	// Same-workspace-root constraint: only accept a cached output whose
	// scheme+authority matches the current task's output. Without this, a
	// reuse hit can silently redirect downstream inputs across cloud regions
	// (e.g. azswed:// → s3://), turning a free intra-region read into a
	// paid cross-region egress on every dependent task. The substring regex
	// extracts the `scheme://authority` prefix (everything up to the first
	// path '/' after `://`); equality there means same backend root.
	//
	// `COALESCE(..., '')` so paths without a scheme (local file paths used
	// in integration tests, and any future on-host workspace) collapse to
	// '' on both sides — two local paths match each other, but a local path
	// never matches a remote URI.
	rows, err := tx.Query(`
		SELECT t.task_id, t.reuse_key, t.step_id, tr.output_path, tr.task_id AS original_task_id
		FROM task t
		JOIN task_reuse tr ON t.reuse_key = tr.reuse_key
		WHERE t.status = 'P'
		  AND t.consume_reuse = true
		  AND t.reuse_key IS NOT NULL
		  AND t.skip_if_exists = false
		  AND t.output IS NOT NULL
		  AND tr.output_path IS NOT NULL
		  AND COALESCE(substring(tr.output_path FROM '^[^:]+://[^/]+'), '')
		      = COALESCE(substring(t.output FROM '^[^:]+://[^/]+'), '')
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
	var minCpu, minMem, minDisk sql.NullFloat64
	err = tx.QueryRow(`
		SELECT t.step_id, s.workflow_id, t.min_cpu, t.min_mem, t.min_disk
		FROM task t
		LEFT JOIN step s ON s.step_id = t.step_id
		WHERE t.task_id = $1 AND t.status = 'P'
	`, taskID).Scan(&stepID, &workflowID, &minCpu, &minMem, &minDisk)
	if err == sql.ErrNoRows {
		return 0, sql.NullInt32{}, fmt.Errorf("task %d not pending", taskID)
	}
	if err != nil {
		return 0, sql.NullInt32{}, fmt.Errorf("failed to load task %d: %w", taskID, err)
	}
	if !stepID.Valid {
		return 0, workflowID, fmt.Errorf("task %d has no step_id", taskID)
	}

	// Per-task fit check: a candidate worker's flavor must satisfy the
	// task's min_cpu/min_mem/min_disk (NULL on either side bypasses the
	// check for that dimension — legacy tasks/workers always fit). Spec:
	// addition_from_nextflow.md A.
	var workerID sql.NullInt32
	err = tx.QueryRow(`
		WITH candidate AS (
			SELECT w.worker_id
			FROM worker w
			LEFT JOIN flavor f ON f.flavor_id = w.flavor_id
			LEFT JOIN task t ON t.worker_id = w.worker_id AND t.status IN ('A','C','D','O','R') AND NOT t.hidden
			WHERE w.status = 'R' AND w.deleted_at IS NULL AND w.step_id = $2
			  AND ($3::double precision IS NULL OR f.cpu  IS NULL OR f.cpu  >= $3)
			  AND ($4::double precision IS NULL OR f.mem  IS NULL OR f.mem  >= $4)
			  AND ($5::double precision IS NULL OR f.disk IS NULL OR f.disk >= $5)
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
	`, taskID, stepID.Int32, minCpu, minMem, minDisk).Scan(&workerID)
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

// ---- No-fit diagnostic ------------------------------------------------------
//
// When the assignment loop visits a worker that has free capacity at a step
// where pending tasks exist BUT every task fails the resource-fit check
// (task.min_cpu/min_mem/min_disk > worker.flavor.cpu/mem/disk), the worker
// is silently skipped. From the operator's perspective the worker shows
// as attached, healthy, and idle while tasks pile up — usually because
// they manually attached an under-spec'd worker (the case that lit this
// up in production: bigbrother with 15.6 GB attached to a step demanding
// 20 GB). emitNoFitWarning makes that visible by writing a `W`-level
// worker_event with the offending requirement vs. the worker's actual
// caps, so the UI's warning icon lights up and `list_worker_events`
// surfaces an actionable line.

// noFitMemo is the per-worker throttle record. We re-emit if the
// step changes (operator moved the worker, problem may be different),
// if the message text changes (different bottleneck — mem vs cpu), or
// after noFitRewarnInterval has passed (so a long-running mismatch
// keeps reminding the operator without flooding).
type noFitMemo struct {
	stepID       int32
	reason       string
	lastWarnedAt time.Time
}

const noFitRewarnInterval = 15 * time.Minute

// whyDoesNotFit returns a human-readable string describing why a task
// with the given resource requirements doesn't fit a worker with the
// given flavor caps. Returns "" if the task actually fits — caller
// guards on fitsWorker == false before calling. Mirrors the same
// dimension-by-dimension comparison the fit check itself does;
// NaN-on-either-side semantics (legacy task without min_*, or legacy
// worker without flavor row) report a less specific reason.
func whyDoesNotFit(req taskMins, caps workerCaps) string {
	if !math.IsNaN(req.cpu) && !math.IsNaN(caps.cpu) && req.cpu > caps.cpu {
		return fmt.Sprintf("task needs cpu=%g, worker has cpu=%g", req.cpu, caps.cpu)
	}
	if !math.IsNaN(req.mem) && !math.IsNaN(caps.mem) && req.mem > caps.mem {
		return fmt.Sprintf("task needs mem=%g GB, worker has mem=%g GB", req.mem, caps.mem)
	}
	if !math.IsNaN(req.disk) && !math.IsNaN(caps.disk) && req.disk > caps.disk {
		return fmt.Sprintf("task needs disk=%g GB, worker has disk=%g GB", req.disk, caps.disk)
	}
	return "no fitting task for this worker (resource curves don't intersect)"
}

// recordNoFit consults and updates the per-worker throttle memo.
// Returns true iff the caller should actually emit a warning now —
// i.e. this is a fresh condition (no prior memo), a different step
// or reason from last time (operator moved the worker, or a
// different resource dimension is now the bottleneck), or the
// re-warn interval has elapsed. Separated from the DB-writing
// caller (maybeWarnNoFit) so the state-machine logic is unit-
// testable without a Postgres dependency.
func (s *taskQueueServer) recordNoFit(workerID, stepID int32, reason string, now time.Time) bool {
	if v, ok := s.noFitMemos.Load(workerID); ok {
		memo := v.(*noFitMemo)
		// Same step, same reason, recent warning → suppress.
		if memo.stepID == stepID && memo.reason == reason && now.Sub(memo.lastWarnedAt) < noFitRewarnInterval {
			return false
		}
	}
	s.noFitMemos.Store(workerID, &noFitMemo{
		stepID:       stepID,
		reason:       reason,
		lastWarnedAt: now,
	})
	return true
}

// maybeWarnNoFit emits a throttled worker_event when a worker can't
// accommodate any of its step's pending tasks. See noFitMemo for the
// throttling rules. Best-effort: a failed insert logs but does not
// disturb the assignment loop.
func (s *taskQueueServer) maybeWarnNoFit(workerID, stepID int32, reason string) {
	if !s.recordNoFit(workerID, stepID, reason, time.Now()) {
		return
	}
	details := fmt.Sprintf(`{"step_id": %d, "reason": %q}`, stepID, reason)
	if _, err := s.db.Exec(`
		INSERT INTO worker_event (worker_id, event_class, level, message, details_json)
		VALUES ($1, 'fit', 'W', $2, $3::jsonb)
	`, workerID, "worker has capacity but no pending task at step fits its flavor: "+reason, details); err != nil {
		log.Printf("⚠️ Failed to record no-fit event for worker %d: %v", workerID, err)
	}
}

// clearNoFitMemo drops the throttle record for a worker that just
// successfully picked up a task. Without this, the operator's
// resource-curve adjustment (or a freshly-recruited bigger flavor)
// would not produce a fresh "now fits" → "no longer fits" warning
// later, because the memo would still be in the cooldown window.
func (s *taskQueueServer) clearNoFitMemo(workerID int32) {
	s.noFitMemos.Delete(workerID)
}
