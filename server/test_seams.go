package server

// Test seams for behaviour the production code couldn't otherwise be
// exercised against without terminating the test binary or polluting
// shared output. These are exported so integration tests in
// `tests/integration` (a separate package) can override them. They are
// not part of the supported runtime API; the rest of the codebase
// should never read them.

import (
	"database/sql"
	"io"
)

// GateExit returns the current exit hook used by the SIGUSR1 graceful
// drain. Tests use this to swap in a recorder, then restore on cleanup.
func GateExit() func(int) { return gateExit }

// SetGateExit replaces the SIGUSR1 graceful-drain exit hook. Default is
// `os.Exit`. Tests must restore the previous value via t.Cleanup().
func SetGateExit(f func(int)) { gateExit = f }

// GateStdout / SetGateStdout: same pattern for the stdout writer the
// gate uses to emit its drained-line JSON. Tests substitute a buffer.
func GateStdout() io.Writer        { return gateStdout }
func SetGateStdout(w io.Writer)    { gateStdout = w }

// TriggerStuckDeleteCleanup runs one synchronous tick of the stuck-delete
// janitor. The janitor's regular schedule (~5 min) is too slow for
// tests; this seam lets the integration test fire it on demand. Returns
// nil if the server hasn't started its janitor yet (the trigger is only
// armed inside startStuckDeleteCleanup).
func TriggerStuckDeleteCleanup() error {
	if stuckDeleteCleanupTrigger == nil {
		return nil
	}
	return stuckDeleteCleanupTrigger()
}

// stuckDeleteCleanupTrigger is wired in startStuckDeleteCleanup so the
// test can run the janitor synchronously.
var stuckDeleteCleanupTrigger func() error

// currentStatsAgg / currentStatsDB are pointers to the live server's
// in-memory aggregator and its DB handle. Populated in newTaskQueueServer
// so integration tests can read the aggregator state and compare it
// against a fresh Reconcile from the same DB. See
// specs/task_transitions.md — this is the seam that makes the
// property-style parity test possible.
var (
	currentStatsAgg *StepStatsAgg
	currentStatsDB  *sql.DB
)

// StatsAggForTest returns the live server's aggregator or nil if no
// server has been started this process lifetime.
func StatsAggForTest() *StepStatsAgg { return currentStatsAgg }

// StatsDBForTest returns the DB handle the live server is using.
func StatsDBForTest() *sql.DB { return currentStatsDB }

// registerTestStats is called from newTaskQueueServer to wire the seams.
// Idempotent overwrite — the last server started wins, which matches
// how the other seams behave.
func registerTestStats(agg *StepStatsAgg, db *sql.DB) {
	currentStatsAgg = agg
	currentStatsDB = db
}

// StepAggSnapshot is a value-type copy of a StepAgg's status counters,
// exported so integration tests can compare the live aggregator state
// with a fresh Reconcile without holding the aggregator mutex.
// Excludes RunningTasks (timestamps drift at ns precision between
// delta path and SQL) and duration Accumulators (compared separately
// when the test cares) — this is the "primary counters" projection
// the property test's invariant is built on.
type StepAggSnapshot struct {
	Waiting      int32
	Pending      int32
	Accepted     int32
	Running      int32
	Uploading    int32
	Succeeded    int32
	Failed       int32
	ReallyFailed int32
	Retrying     int32
}

// SnapshotCounters returns a deep-copied snapshot of every step's
// primary counters. Test uses this on both the live aggregator and a
// fresh NewStepStatsAgg(db) and compares them.
func (a *StepStatsAgg) SnapshotCounters() map[int32]map[int32]StepAggSnapshot {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[int32]map[int32]StepAggSnapshot, len(a.data))
	for wfID, steps := range a.data {
		copied := make(map[int32]StepAggSnapshot, len(steps))
		for stepID, agg := range steps {
			copied[stepID] = StepAggSnapshot{
				Waiting:      agg.Waiting,
				Pending:      agg.Pending,
				Accepted:     agg.Accepted,
				Running:      agg.Running,
				Uploading:    agg.Uploading,
				Succeeded:    agg.Succeeded,
				Failed:       agg.Failed,
				ReallyFailed: agg.ReallyFailed,
				Retrying:     agg.Retrying,
			}
		}
		out[wfID] = copied
	}
	return out
}
