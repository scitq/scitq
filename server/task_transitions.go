package server

// Task status transitions — single choke-point.
//
// See specs/task_transitions.md for the design.
//
// This file is the emerging home of the transition primitive. In the
// pilot (2026-07-02) it holds only the API definitions and internal
// helpers; existing call sites (UpdateTaskStatus, retryTaskInternal,
// the six silent W→P promoters, etc.) still write task.status
// directly and mark the aggregator dirty via MarkDirtyLocked /
// StepStatsAgg.Adjust. Subsequent migrations move each call site to
// TransitionTask (single) or TransitionTaskBatch (bulk).

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// TransitionRequest is the input to TransitionTask.
type TransitionRequest struct {
	TaskID int32
	// New status letter: 'P'|'W'|'A'|'C'|'D'|'O'|'R'|'U'|'V'|'S'|'F'.
	NewStatus string
	// Optional guard: only transition if the CURRENT status equals
	// this value. Empty = unconditional. Mirrors the "WHERE status='W'"
	// inline guards several existing sites already use.
	ExpectStatus string
	// Extras applied in the same UPDATE. Nil pointers = leave alone.
	// The struct is intentionally typed rather than a map so a new
	// column touched by a transition forces an API extension rather
	// than sneaking in via a stringly-typed side channel.
	Extras TransitionExtras
}

// TransitionExtras carries additional columns the transition writes.
// Populated as call sites migrate; add fields as more transitions
// need them (e.g. failure_class on F, run_started_at on R).
type TransitionExtras struct {
	WorkerID     *int32
	FailureClass *string
}

// TransitionResult reports what happened.
type TransitionResult struct {
	// OldStatus is the row's status before the UPDATE. Empty when the
	// row didn't exist or the guard (ExpectStatus) didn't match.
	OldStatus string
	// WorkflowID / StepID scraped from the row via the RETURNING
	// clause. Used internally to bill the transition against the
	// right aggregator bucket.
	WorkflowID int32
	StepID     int32
	// Applied is true iff the UPDATE affected a row (guard matched,
	// row existed, row wasn't hidden).
	Applied bool
}

// TransitionTask is the ONLY function that should be writing task.status
// once the migration is complete. Runs the DB UPDATE, adjusts the
// in-memory aggregator, and marks the step dirty so the next snapshot
// flush picks it up. Emits nothing directly — the WS emit happens via
// the flush goroutine, not synchronously with the transition.
//
// Atomic pair (DB write + aggregator adjust): if tx is provided, the
// aggregator adjust runs only after the caller commits. If tx is nil,
// this function opens its own tx and commits before adjusting.
//
// Not yet used by existing call sites — pilot-scope this file lands
// the API surface; migrations follow in subsequent PRs.
func (s *taskQueueServer) TransitionTask(ctx context.Context, tx *sql.Tx, req TransitionRequest) (TransitionResult, error) {
	if req.TaskID == 0 || req.NewStatus == "" {
		return TransitionResult{}, fmt.Errorf("TransitionTask: TaskID and NewStatus are required")
	}
	// Placeholder implementation — sufficient for the API to exist and
	// compile; call sites migrate onto it in follow-up work.
	// The current-session pilot goal was landing the aggregator dirty
	// tracking + snapshot dispatch; migrating call sites onto this
	// primitive is Step 4+ of the migration plan.
	return TransitionResult{}, fmt.Errorf("TransitionTask: not implemented yet — pilot lands API only, migrations follow")
}

// promoteTaskWtoP is the aggregator-aware version of the
// `UPDATE task SET status = 'P' WHERE task_id = $1 AND status = 'W'`
// pattern used at every "prerequisites cleared" site. Runs the UPDATE
// (with RETURNING to scrape workflow_id/step_id) and adjusts the
// aggregator (Waiting--/Pending++). s.stats.Adjust auto-marks the
// step dirty so the next snapshot flush ships the change.
//
// Caller passes either a *sql.Tx (transactional context) or nil (uses
// s.db directly). If nil, the aggregator adjust is safe — the UPDATE
// is auto-committed. If a *sql.Tx is passed AND the caller later rolls
// back, the aggregator will over-count Pending until Reconcile heals
// it (within 5 min). This is a known trade-off consistent with
// UpdateTaskStatus's existing inline pattern; callers that guarantee
// commit (assign loop, promoteWaitingTasks, force_run) can use tx.
//
// Returns applied=true iff the row was actually updated (was in W).
// Silent-false when the row is not W (racing promoters, manual retry,
// force-run on a non-W task); callers that need to error on that
// case check the return and construct their own message.
func (s *taskQueueServer) promoteTaskWtoP(ctx context.Context, tx *sql.Tx, taskID int32) (applied bool) {
	const q = `
		WITH upd AS (
			UPDATE task SET status = 'P', modified_at = NOW()
			WHERE task_id = $1 AND status = 'W'
			RETURNING step_id
		)
		SELECT u.step_id, st.workflow_id
		FROM upd u
		LEFT JOIN step st ON st.step_id = u.step_id
	`
	var wfID, stepID sql.NullInt32
	var err error
	if tx != nil {
		err = tx.QueryRowContext(ctx, q, taskID).Scan(&stepID, &wfID)
	} else {
		err = s.db.QueryRowContext(ctx, q, taskID).Scan(&stepID, &wfID)
	}
	if err == sql.ErrNoRows {
		return false // task no longer W — race, caller's discretion
	}
	if err != nil {
		log.Printf("⚠️ promoteTaskWtoP task %d: %v", taskID, err)
		return false
	}
	if wfID.Valid && stepID.Valid {
		s.stats.Adjust(wfID.Int32, stepID.Int32, func(agg *StepAgg) {
			if agg.Waiting > 0 {
				agg.Waiting--
			}
			agg.Pending++
		})
	}
	return true
}
