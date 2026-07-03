package integration_test

// Property-parity smoke test for the task-transitions pilot.
//
// See specs/task_transitions.md. The invariant this test locks in:
// after any sequence of API operations that mutate task state, the
// in-memory step aggregator (`s.stats.data`) is byte-identical to a
// fresh Reconcile from the same DB (`NewStepStatsAgg(db)`).
//
// This is a MINIMAL smoke — three ops (submit, mark accepted, mark
// succeeded) on one task in one workflow. The full property test
// with a randomised op alphabet, retry paths, and DeleteWorkflow is
// the follow-up. Getting the smoke green first proves the seam works
// and the aggregator dirty-tracking + Reconcile-parity mechanism is
// in place.

import (
	"context"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	srv "github.com/scitq/scitq/server"
	"github.com/stretchr/testify/require"
)

// assertAggregatorParity fails the test if the live aggregator's
// primary counters differ from a fresh Reconcile from the same DB.
// Retries a couple times because there's a small race between the
// UpdateTaskStatus WS emit and the flush cycle — either would settle
// the aggregator, but the test is asserting eventual convergence
// within a short window rather than instant.
func assertAggregatorParity(t *testing.T, msg string) {
	t.Helper()
	liveAgg := srv.StatsAggForTest()
	db := srv.StatsDBForTest()
	require.NotNil(t, liveAgg, "test seam not wired: StatsAggForTest returned nil")
	require.NotNil(t, db, "test seam not wired: StatsDBForTest returned nil")

	// Small settle window — the delta path holds s.stats.mu across
	// its aggregator + WS work, but Reconcile also takes the lock.
	// Two attempts with a short sleep between is enough for the
	// serialisation to converge in the smoke scenario.
	var lastDiff string
	for attempt := 0; attempt < 3; attempt++ {
		fresh, err := srv.NewStepStatsAgg(db)
		require.NoError(t, err)
		live := liveAgg.SnapshotCounters()
		freshSnap := fresh.SnapshotCounters()
		lastDiff = diffCounters(live, freshSnap)
		if lastDiff == "" {
			return // parity holds
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("aggregator drift after %s:\n%s", msg, lastDiff)
}

// diffCounters returns "" when live == fresh, otherwise a
// human-readable diff. Keys present in one and not the other are
// treated as mismatches; per-counter differences are listed with the
// live vs fresh values.
func diffCounters(live, fresh map[int32]map[int32]srv.StepAggSnapshot) string {
	// Collect all keys from both sides.
	keys := map[[2]int32]bool{}
	for wfID, steps := range live {
		for stepID := range steps {
			keys[[2]int32{wfID, stepID}] = true
		}
	}
	for wfID, steps := range fresh {
		for stepID := range steps {
			keys[[2]int32{wfID, stepID}] = true
		}
	}
	var out string
	for k := range keys {
		wfID, stepID := k[0], k[1]
		l, lok := live[wfID][stepID]
		f, fok := fresh[wfID][stepID]
		if !lok {
			out += fmt.Sprintf("  step (wf=%d step=%d) present in fresh only: %+v\n", wfID, stepID, f)
			continue
		}
		if !fok {
			out += fmt.Sprintf("  step (wf=%d step=%d) present in live only: %+v\n", wfID, stepID, l)
			continue
		}
		if l != f {
			out += fmt.Sprintf("  step (wf=%d step=%d) mismatch:\n    live:  %+v\n    fresh: %+v\n", wfID, stepID, l, f)
		}
	}
	return out
}

// TestTransitionsAggregatorParity_Smoke exercises the smallest
// meaningful transition sequence (P → C → S on one task) and asserts
// aggregator parity after each step. Meant to fail loudly if a
// future change to the delta path (UpdateTaskStatus, retry, etc.)
// re-introduces a drift like the 2026-07-02 counter-drift bugs.
func TestTransitionsAggregatorParity_Smoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Create workflow + step.
	wfName := fmt.Sprintf("parity-smoke-%d", time.Now().UnixNano())
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "parity-step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// The empty-workflow state is trivially in parity — both sides
	// have {wfID: {stepID: zero}} or {} depending on how EnsureStep
	// treats it. Not asserting here to avoid tying the smoke to
	// bootstrap-order details.

	// Submit one task in P.
	shell := "sh"
	taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo hello", Shell: &shell, Container: "bare",
		Status: "P", StepId: &stepID,
	})
	require.NoError(t, err)
	taskID := taskResp.TaskId

	assertAggregatorParity(t, "SubmitTask(P)")

	// P → A via UpdateTaskStatus. No worker attached — we go via
	// the RPC directly to isolate the transition path from the assign
	// loop.
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId: taskID, NewStatus: "A",
	})
	require.NoError(t, err)
	assertAggregatorParity(t, "UpdateTaskStatus(P→A)")

	// A → R.
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId: taskID, NewStatus: "R",
	})
	require.NoError(t, err)
	assertAggregatorParity(t, "UpdateTaskStatus(A→R)")

	// R → S. Note: UpdateTaskStatus to S/F waits up to 5s for any
	// active log stream to close. This task never streamed logs, so
	// the wait resolves immediately.
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId: taskID, NewStatus: "S",
	})
	require.NoError(t, err)
	assertAggregatorParity(t, "UpdateTaskStatus(R→S)")

	// Guard: workflow ID is used above; keep the reference alive so
	// the go compiler doesn't flag it as unused during pilot iteration.
	_ = wfID
}
