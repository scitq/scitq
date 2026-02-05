package integration_test

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

func TestWorkflowStatusTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)
	c.Attr.Token = token

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// --- Workflow 1: P -> R -> S ---
	wfID := createWorkflowViaCLI(t, c, "wf-status-s")
	stepID := createStepViaCLI(t, c, wfID, "step1")

	workerID := registerWorkerForTest(t, ctx, qc, "wf-status-worker", 1)
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	taskID := submitTaskForStep(t, ctx, qc, stepID, "echo ok", "alpine", 0)

	// Ensure task is not assigned while workflow is Pending.
	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, taskID)
		return task.Status == "P"
	}, 6*time.Second, 200*time.Millisecond, "task should remain P while workflow is Pending")

	// Move workflow to Running (CLI) -> assignment should happen.
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, taskID)
		return task.Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "task should be assigned after workflow becomes Running")

	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    taskID,
		NewStatus: "S",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getWorkflowStatus(t, ctx, qc, wfID) == "S"
	}, 6*time.Second, 200*time.Millisecond, "workflow should become S after task succeeds")

	// --- Workflow 2: P -> R -> F ---
	wfID2 := createWorkflowViaCLI(t, c, "wf-status-f")
	stepID2 := createStepViaCLI(t, c, wfID2, "step1")

	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID2)})
	require.NoError(t, err)

	taskID2 := submitTaskForStep(t, ctx, qc, stepID2, "false", "alpine", 0)

	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID2), "--status", "R"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, taskID2)
		return task.Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "task should be assigned after workflow becomes Running")

	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    taskID2,
		NewStatus: "F",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getWorkflowStatus(t, ctx, qc, wfID2) == "F"
	}, 6*time.Second, 200*time.Millisecond, "workflow should become F after task fails")
}

func createWorkflowViaCLI(t *testing.T, c cli.CLI, name string) int32 {
	t.Helper()
	out, err := runCLICommand(c, []string{"workflow", "create", "--name", name})
	require.NoError(t, err)
	return parseIDFromOutput(t, out)
}

func createStepViaCLI(t *testing.T, c cli.CLI, workflowID int32, name string) int32 {
	t.Helper()
	out, err := runCLICommand(c, []string{"step", "create", "--workflow-id", fmt.Sprintf("%d", workflowID), "--name", name})
	require.NoError(t, err)
	return parseIDFromOutput(t, out)
}

func parseIDFromOutput(t *testing.T, out string) int32 {
	t.Helper()
	re := regexp.MustCompile(`ID\s+(\d+)`)
	m := re.FindStringSubmatch(out)
	if len(m) < 2 {
		t.Fatalf("failed to parse ID from CLI output: %q", out)
	}
	id, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	return int32(id)
}

func registerWorkerForTest(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, name string, concurrency int32) int32 {
	t.Helper()
	resp, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{
		Name:        name,
		Concurrency: &concurrency,
	})
	require.NoError(t, err)
	require.NotZero(t, resp.WorkerId)
	return resp.WorkerId
}

func submitTaskForStep(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, stepID int32, command, container string, retry int32) int32 {
	t.Helper()
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		StepId:    &stepID,
		Command:   command,
		Container: container,
		Status:    "P",
		Retry:     &retry,
	})
	require.NoError(t, err)
	require.NotZero(t, task.TaskId)
	return task.TaskId
}

func getTask(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, taskID int32) *pb.Task {
	t.Helper()
	lt, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
	require.NoError(t, err)
	for _, tk := range lt.Tasks {
		if tk.TaskId == taskID {
			return tk
		}
	}
	t.Fatalf("task %d not found", taskID)
	return nil
}

func getWorkflowStatus(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, workflowID int32) string {
	t.Helper()
	res, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
	require.NoError(t, err)
	for _, wf := range res.Workflows {
		if wf.WorkflowId == workflowID {
			return wf.Status
		}
	}
	t.Fatalf("workflow %d not found", workflowID)
	return ""
}
