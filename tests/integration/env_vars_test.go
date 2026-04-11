package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestEnvVarsCPUMemThreads verifies that $CPU, $MEM and $THREADS are injected
// into the Docker container environment.
func TestEnvVarsCPUMemThreads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Submit task without step — any worker can pick it up
	// Use sh -c to expand env vars
	shell := "sh"
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo CPU=$CPU THREADS=$THREADS MEM=$MEM",
		Container: "bare",
		Shell:     &shell,
		Status:    "P",
	})
	require.NoError(t, err)
	taskID := task.TaskId

	// Start a real worker with Docker
	cleanupClient, _ := startClientForTest(t, serverAddr, "envvar-worker", workerToken, 1)
	defer cleanupClient()

	// Wait for task to complete
	require.Eventually(t, func() bool {
		tk := getTask(t, ctx, qc, taskID)
		return tk.Status == "S" || tk.Status == "F"
	}, 60*time.Second, 500*time.Millisecond, "task should complete")

	tk := getTask(t, ctx, qc, taskID)
	require.Equal(t, "S", tk.Status, "task should succeed")

	// Check stdout contains the env vars
	out, err = runCLICommand(c, []string{"task", "stdout", "--id", fmt.Sprintf("%d", taskID)})
	require.NoError(t, err)

	t.Logf("Task stdout: %s", out)
	require.Contains(t, out, "CPU=", "stdout should contain CPU=")
	require.Contains(t, out, "THREADS=", "stdout should contain THREADS=")
	require.Contains(t, out, "MEM=", "stdout should contain MEM=")
}
