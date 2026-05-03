package integration_test

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	srv "github.com/scitq/scitq/server"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestServerGracefulGate_EndToEnd exercises the SIGUSR1 path through
// the live gRPC stack: send the signal to the test process, watch the
// admin RPCs flip from accepting to refusing, and verify the worker
// ping reflects the gating flag. The test stubs out gateExit so we
// don't actually terminate the test binary.
//
// See specs/worker_autoupgrade.md (Phase III).
func TestServerGracefulGate_EndToEnd(t *testing.T) {
	// NOT t.Parallel(): this test installs a process-wide SIGUSR1
	// handler (signal.Notify is a global registry) and overrides the
	// package-level gateExit / gateStdout. Running it concurrently
	// with other tests that also rely on os.Stdout swapping
	// (captureOutput) would cross-contaminate.

	ctx := context.Background()

	prevExit, prevStdout := srv.GateExit(), srv.GateStdout()
	t.Cleanup(func() {
		srv.SetGateExit(prevExit)
		srv.SetGateStdout(prevStdout)
	})

	exited := make(chan int, 1)
	srv.SetGateExit(func(code int) {
		// Don't actually os.Exit — would kill the test binary. Just
		// record the call so the test can assert on it.
		select {
		case exited <- code:
		default:
		}
	})

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	adm := adminCLI(t, serverAddr, adminUser, adminPassword)
	defer adm.Close()

	// Sanity-check the pre-gate state: a worker can ping (in-process
	// quick path), CreateRecruiter is permitted (no gate yet).
	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()
	wid := registerWorker(t, serverAddr, workerToken, "wu-gate-baseline")
	pingResp, err := wk.Client.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{WorkerId: wid})
	require.NoError(t, err)
	require.False(t, pingResp.GetServerUpgradeInProgress(), "no gate yet, flag must be false")

	// Send SIGUSR1 to ourselves. The handler installed by Serve() picks
	// it up, sets s.gating = true, and starts the drain goroutine.
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGUSR1))

	// Wait for the gate to flip — observable via either the ping
	// response or a refused admin RPC.
	require.Eventually(t, func() bool {
		resp, err := wk.Client.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{WorkerId: wid})
		return err == nil && resp.GetServerUpgradeInProgress()
	}, 2*time.Second, 50*time.Millisecond, "ping should report server_upgrade_in_progress=true once gating")

	// Admin operations now refused with FailedPrecondition.
	_, err = adm.Client.CreateRecruiter(ctx, &pb.Recruiter{
		StepId:      9999,
		Rank:        0,
		Protofilter: "cpu>=1",
	})
	require.Error(t, err, "CreateRecruiter should be refused while gating")
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code(), "admin RPCs must return FailedPrecondition while gating")

	// And the gate eventually calls our stubbed exit (no in-flight
	// admin jobs in this test, so the drain finishes quickly).
	select {
	case code := <-exited:
		require.Equal(t, 0, code, "gate must exit 0")
	case <-time.After(3 * time.Second):
		t.Fatalf("gate did not call exit within 3s — drain likely stuck")
	}
}
