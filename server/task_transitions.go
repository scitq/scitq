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
