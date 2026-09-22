package server

import (
	"testing"
	"time"
)

// TestSnapshot_RunningRun_DistinguishesSubSecondStarts verifies that
// two running tasks whose start times differ by less than one second
// produce distinguishable min/max in the snapshot. The pre-fix code
// truncated run_started_at to unix seconds via time.Unix(sec, 0),
// which collapsed near-simultaneous starts to identical durations
// and made the UI show min=max=avg for whole assignment bursts.
func TestSnapshot_RunningRun_DistinguishesSubSecondStarts(t *testing.T) {
	agg := NewStepAgg()
	agg.Running = 2
	base := time.Now().Add(-30 * time.Second)
	agg.RunningTasks[1] = base
	agg.RunningTasks[2] = base.Add(500 * time.Millisecond)

	now := base.Add(60 * time.Second) // 60 s after task 1, 59.5 s after task 2
	snap := snapshotFromAgg(1, agg, now)

	if snap.RunningRun.Count != 2 {
		t.Fatalf("expected 2 running samples, got %d", snap.RunningRun.Count)
	}
	if snap.RunningRun.Min == snap.RunningRun.Max {
		t.Fatalf("min and max should differ (sub-second precision), got both = %v",
			snap.RunningRun.Min)
	}
	// Sanity: min ≈ 59.5, max ≈ 60.0.
	if snap.RunningRun.Min < 59.0 || snap.RunningRun.Min > 60.0 {
		t.Errorf("min out of range: %v", snap.RunningRun.Min)
	}
	if snap.RunningRun.Max < 59.5 || snap.RunningRun.Max > 60.5 {
		t.Errorf("max out of range: %v", snap.RunningRun.Max)
	}
}

// TestSnapshot_RunningRun_IdenticalStartsCollapse: the flip side —
// when two tasks really did start at the exact same instant (via a
// synthesized batch, say a test), min == max is the correct answer.
// This protects against a future "spread ranges artificially" fix
// that would mask real behaviour.
func TestSnapshot_RunningRun_IdenticalStartsCollapse(t *testing.T) {
	agg := NewStepAgg()
	agg.Running = 3
	sameStart := time.Now().Add(-10 * time.Second)
	agg.RunningTasks[1] = sameStart
	agg.RunningTasks[2] = sameStart
	agg.RunningTasks[3] = sameStart

	snap := snapshotFromAgg(1, agg, sameStart.Add(30*time.Second))
	if snap.RunningRun.Min != snap.RunningRun.Max {
		t.Fatalf("identical starts should collapse to min==max, got min=%v max=%v",
			snap.RunningRun.Min, snap.RunningRun.Max)
	}
}

// TestFlushLoop_HeartbeatMarksRunningStepsDirty verifies the
// running-duration heartbeat: after runningHeartbeatInterval elapses,
// steps with running tasks are added to the dirty set (whether or
// not their state actually changed) so the next flush re-emits their
// RunningRun. Without this, the UI's "Running: 6m57s" stays frozen
// at the value from the last state transition.
func TestFlushLoop_HeartbeatMarksRunningStepsDirty(t *testing.T) {
	a := &StepStatsAgg{
		data: make(map[int32]map[int32]*StepAgg),
	}
	// Seed one step with a running task; the heartbeat should mark it.
	sagg := NewStepAgg()
	sagg.Running = 1
	sagg.RunningTasks[42] = time.Now().Add(-1 * time.Minute)
	a.data[100] = map[int32]*StepAgg{200: sagg}

	// Force lastRunningTick to the past so the heartbeat fires
	// immediately when we call the helper directly.
	a.lastRunningTick = time.Now().Add(-2 * runningHeartbeatInterval)

	a.mu.Lock()
	marked := a.markRunningStepsDirtyLocked()
	a.mu.Unlock()

	if marked != 1 {
		t.Fatalf("expected 1 step marked, got %d", marked)
	}
	if _, ok := a.dirty[stepKey{workflowID: 100, stepID: 200}]; !ok {
		t.Fatal("step should be in dirty set after heartbeat")
	}
}

// TestFlushLoop_HeartbeatSkipsIdleSteps: a step with no running tasks
// is never touched by the heartbeat. Zero traffic on idle steps
// remains the invariant the snapshot-dispatch design relies on.
func TestFlushLoop_HeartbeatSkipsIdleSteps(t *testing.T) {
	a := &StepStatsAgg{
		data: make(map[int32]map[int32]*StepAgg),
	}
	// Step exists but has no running tasks — a completed step, for
	// example, or one that only holds pending/queued tasks.
	sagg := NewStepAgg()
	sagg.Succeeded = 5
	a.data[100] = map[int32]*StepAgg{200: sagg}

	a.mu.Lock()
	marked := a.markRunningStepsDirtyLocked()
	a.mu.Unlock()

	if marked != 0 {
		t.Fatalf("idle step should not be marked, got %d", marked)
	}
	if len(a.dirty) != 0 {
		t.Fatalf("dirty set should be empty, got %d entries", len(a.dirty))
	}
}
