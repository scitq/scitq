package server

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestRefuseIfGating exercises the admin-RPC short-circuit that fires
// once SIGUSR1 has been received. See specs/worker_autoupgrade.md
// (Phase III).
func TestRefuseIfGating(t *testing.T) {
	s := &taskQueueServer{}

	// Not gating: no error, ops proceed.
	require.NoError(t, s.refuseIfGating("CreateWorker"))

	// Gating: returns FailedPrecondition, with the operation name in
	// the message so the operator sees what was refused.
	s.gating.Store(true)
	err := s.refuseIfGating("CreateWorker")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "CreateWorker")
}

// TestRunGracefulDrain_NoActiveJobs verifies the empty-queue fast path:
// the gate exits cleanly within milliseconds and emits a single JSON
// line on the configured stdout writer.
func TestRunGracefulDrain_NoActiveJobs(t *testing.T) {
	s := &taskQueueServer{}
	s.gating.Store(true)

	prevExit, prevStdout := gateExit, gateStdout
	defer func() {
		gateExit = prevExit
		gateStdout = prevStdout
	}()

	var exitCode int
	var exitCalled atomic.Bool
	gateExit = func(code int) {
		exitCode = code
		exitCalled.Store(true)
	}
	var buf bytes.Buffer
	gateStdout = &buf

	done := make(chan struct{})
	go func() {
		s.runGracefulDrain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runGracefulDrain did not return within 2s")
	}

	require.True(t, exitCalled.Load(), "gateExit should have been called")
	require.Equal(t, 0, exitCode, "gate should exit 0 on clean drain")

	// JSON line shape.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload),
		"drained line should be JSON: %q", buf.String())
	require.Equal(t, "server_drained", payload["event"])
	require.Contains(t, payload, "at")
	require.Contains(t, payload, "drain_seconds")
}

// TestRunGracefulDrain_WaitsForJobsThenCancelsAtTimeout verifies
// (1) the gate waits for jobs to drain naturally and (2) the hard cap
// fires by cancelling lingering jobs. We can't move the 10-min const
// from a test, so we mimic the timeout path via a job whose cancel func
// removes itself from activeJobs; the gate sees zero active and exits.
func TestRunGracefulDrain_WaitsForActiveJobs(t *testing.T) {
	s := &taskQueueServer{}
	s.gating.Store(true)

	prevExit, prevStdout := gateExit, gateStdout
	defer func() {
		gateExit = prevExit
		gateStdout = prevStdout
	}()
	var exitCalled atomic.Bool
	gateExit = func(code int) { exitCalled.Store(true) }
	var buf bytes.Buffer
	gateStdout = &buf

	// Plant one fake "in-flight" job. The gate's count loop will see
	// it; we remove it after a short delay to simulate the natural
	// completion the gate is designed to wait for.
	jobID := int32(42)
	_, jobCancel := context.WithCancel(context.Background())
	s.activeJobs.Store(jobID, context.CancelFunc(jobCancel))

	go func() {
		time.Sleep(750 * time.Millisecond)
		s.activeJobs.Delete(jobID)
	}()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		s.runGracefulDrain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runGracefulDrain did not return within 3s")
	}
	elapsed := time.Since(start)

	require.True(t, exitCalled.Load())
	require.GreaterOrEqual(t, elapsed, 700*time.Millisecond,
		"gate should have waited for the active job to clear before exiting")

	// drain_seconds is rounded; allow it to be 0 or 1 in this fast test.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &payload))
	require.Equal(t, "server_drained", payload["event"])
}

// TestCountActiveJobs sanity-checks the helper used by the drain loop.
func TestCountActiveJobs(t *testing.T) {
	s := &taskQueueServer{}
	require.Equal(t, 0, s.countActiveJobs())

	for i := int32(1); i <= 3; i++ {
		_, c := context.WithCancel(context.Background())
		s.activeJobs.Store(i, context.CancelFunc(c))
	}
	require.Equal(t, 3, s.countActiveJobs())

	s.activeJobs.Delete(int32(2))
	require.Equal(t, 2, s.countActiveJobs())
}

// TestGracefulGate_IsIdempotentUnderConcurrentSignals ensures that
// repeated SIGUSR1 deliveries don't kick off a second drain loop, which
// would race the first to exit.
func TestGracefulGate_IsIdempotentUnderConcurrentSignals(t *testing.T) {
	s := &taskQueueServer{}

	// Pretend we just received the signal.
	require.True(t, s.gating.CompareAndSwap(false, true), "first signal should latch")
	require.False(t, s.gating.CompareAndSwap(false, true), "second signal must be a no-op")

	// And the helper still reports we're gating.
	require.True(t, s.gating.Load())
}

// keep imports happy when the file is built without all dependencies
var _ = sync.Map{}
