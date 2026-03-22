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

func TestTaskStdoutStderrLogs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	// Create workflow + step + worker + task
	wfID := createWorkflowViaCLI(t, c, "wf-logs")
	stepID := createStepViaCLI(t, c, wfID, "step1")
	workerID := registerWorkerForTest(t, ctx, qc, "logs-worker", 1)
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	taskID := submitTaskForStep(t, ctx, qc, stepID, "echo hello", "alpine", 0)

	// Start workflow so task gets assigned
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskID).Status == "A"
	}, 6*time.Second, 200*time.Millisecond)

	// Send stdout and stderr logs via gRPC (simulating a worker)
	logStream, err := qc.SendTaskLogs(ctx)
	require.NoError(t, err)

	require.NoError(t, logStream.Send(&pb.TaskLog{TaskId: taskID, LogType: "stdout", LogText: "hello from stdout"}))
	require.NoError(t, logStream.Send(&pb.TaskLog{TaskId: taskID, LogType: "stdout", LogText: "second stdout line"}))
	require.NoError(t, logStream.Send(&pb.TaskLog{TaskId: taskID, LogType: "stderr", LogText: "warning from stderr"}))
	_, err = logStream.CloseAndRecv()
	require.NoError(t, err)

	// Mark task as succeeded so streams terminate
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: taskID, NewStatus: "S"})
	require.NoError(t, err)

	// Give a brief moment for log files to be flushed
	time.Sleep(200 * time.Millisecond)

	// Test: scitq task stdout
	out, err = runCLICommand(c, []string{"task", "stdout", "--id", fmt.Sprintf("%d", taskID)})
	require.NoError(t, err)
	require.Contains(t, out, "hello from stdout")
	require.Contains(t, out, "second stdout line")
	require.NotContains(t, out, "warning from stderr")

	// Test: scitq task stderr
	out, err = runCLICommand(c, []string{"task", "stderr", "--id", fmt.Sprintf("%d", taskID)})
	require.NoError(t, err)
	require.Contains(t, out, "warning from stderr")
	require.NotContains(t, out, "hello from stdout")

	// Test: scitq task logs (both streams, colored)
	out, err = runCLICommand(c, []string{"task", "logs", "--id", fmt.Sprintf("%d", taskID)})
	require.NoError(t, err)
	require.Contains(t, out, "hello from stdout")
	require.Contains(t, out, "warning from stderr")
}
