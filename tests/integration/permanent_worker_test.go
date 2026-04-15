package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestPermanentWorkerProtection verifies that:
// 1. A permanent worker is NOT deleted by the watchdog when idle
// 2. Toggling permanent off causes the watchdog to attempt deletion
func TestPermanentWorkerProtection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Start server with very short idle timeout so the watchdog acts quickly
	override := &config.Config{}
	override.Scitq.IdleTimeout = 2
	override.Scitq.NewWorkerIdleTimeout = 2

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)
	c.Attr.Token = token

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Start a real worker client (registers with local provider)
	cleanupClient, _ := startClientForTest(t, serverAddr, "perm-test-worker", token, 1)
	defer cleanupClient()

	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.Name == "perm-test-worker" {
				workerID = w.WorkerId
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "worker should register")

	// Mark it permanent
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{
		WorkerId:    workerID,
		IsPermanent: proto.Bool(true),
	})
	require.NoError(t, err)
	require.True(t, getWorker(t, ctx, qc, workerID).IsPermanent)

	// Wait longer than idle timeout — permanent worker should survive
	time.Sleep(5 * time.Second)

	w := getWorker(t, ctx, qc, workerID)
	require.Equal(t, "R", w.Status, "permanent worker should still be Running (not deleted)")

	// Toggle permanent OFF
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{
		WorkerId:    workerID,
		IsPermanent: proto.Bool(false),
	})
	require.NoError(t, err)
	require.False(t, getWorker(t, ctx, qc, workerID).IsPermanent)

	// Wait for watchdog to attempt deletion — the worker status should change
	// (deletion creates a job which changes worker status, even if the actual
	// provider delete fails in the test environment)
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.WorkerId == workerID {
				// Worker status changes from R to something else when deletion is triggered
				return w.Status != "R"
			}
		}
		return true // worker gone entirely
	}, 20*time.Second, 500*time.Millisecond, fmt.Sprintf("worker %d should be targeted for deletion after permanent toggled off", workerID))
}
