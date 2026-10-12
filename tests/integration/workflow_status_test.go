package integration_test

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
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
	token := extractToken(out)
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

// TestWorkflowStatusLeafStepForgivesUpstreamFailure covers the leaf-step
// success rule. A fan-in task declared with accept_failure consumes a
// failed upstream task. If the fan-in itself succeeds, every leaf-step
// task is S and the workflow is S even though an upstream task is F.
func TestWorkflowStatusLeafStepForgivesUpstreamFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfID := createWorkflowViaCLI(t, c, "wf-leaf-forgives")
	upstreamStep := createStepViaCLI(t, c, wfID, "upstream")
	faninStep := createStepViaCLI(t, c, wfID, "fanin")

	workerID := registerWorkerForTest(t, ctx, qc, "wf-leaf-worker", 2)
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", upstreamStep)})
	require.NoError(t, err)

	// Upstream task on step 1 — will fail.
	up, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		StepId:    &upstreamStep,
		Command:   "false",
		Container: "alpine",
		Status:    "P",
		TaskName:  strPtr("upstream"),
	})
	require.NoError(t, err)

	// Fan-in task on step 2 — depends on upstream, accepts failure, will succeed.
	fanin, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		StepId:        &faninStep,
		Command:       "echo collected",
		Container:     "alpine",
		Status:        "W",
		Dependency:    []int32{up.TaskId},
		AcceptFailure: true,
		TaskName:      strPtr("fanin"),
	})
	require.NoError(t, err)

	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	// Fail the upstream task.
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, up.TaskId).Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "upstream task should be assigned")
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: up.TaskId, NewStatus: "F"})
	require.NoError(t, err)

	// Fan-in should unlock (accept_failure) and be assignable on its step.
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", faninStep)})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, fanin.TaskId).Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "fan-in should unlock after upstream F (accept_failure)")

	// Fan-in succeeds — the single leaf task is S, workflow should be S.
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: fanin.TaskId, NewStatus: "S"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getWorkflowStatus(t, ctx, qc, wfID) == "S"
	}, 6*time.Second, 200*time.Millisecond, "workflow should be S: leaf fan-in succeeded, upstream F was accepted")
}

// TestWorkflowStatusLeafStepFailureStillFailsWorkflow is the negative
// counterpart: if the fan-in itself fails, the leaf set is not all S
// and the workflow must be F even though an accept_failure edge exists.
func TestWorkflowStatusLeafStepFailureStillFailsWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfID := createWorkflowViaCLI(t, c, "wf-leaf-f")
	upstreamStep := createStepViaCLI(t, c, wfID, "upstream")
	faninStep := createStepViaCLI(t, c, wfID, "fanin")

	workerID := registerWorkerForTest(t, ctx, qc, "wf-leaf-f-worker", 2)
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", upstreamStep)})
	require.NoError(t, err)

	up, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		StepId:    &upstreamStep,
		Command:   "false",
		Container: "alpine",
		Status:    "P",
		TaskName:  strPtr("upstream"),
	})
	require.NoError(t, err)

	fanin, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		StepId:        &faninStep,
		Command:       "false",
		Container:     "alpine",
		Status:        "W",
		Dependency:    []int32{up.TaskId},
		AcceptFailure: true,
		TaskName:      strPtr("fanin"),
	})
	require.NoError(t, err)

	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, up.TaskId).Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "upstream task should be assigned")
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: up.TaskId, NewStatus: "F"})
	require.NoError(t, err)

	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", faninStep)})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, fanin.TaskId).Status == "A"
	}, 6*time.Second, 200*time.Millisecond, "fan-in should unlock")

	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: fanin.TaskId, NewStatus: "F"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getWorkflowStatus(t, ctx, qc, wfID) == "F"
	}, 6*time.Second, 200*time.Millisecond, "workflow should be F: the leaf fan-in itself failed")
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

func TestFreeWorkersOnWorkflowCompletion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Create workflow + step, register a worker on the step
	wfID := createWorkflowViaCLI(t, c, "wf-free-workers")
	stepID := createStepViaCLI(t, c, wfID, "step1")
	workerID := registerWorkerForTest(t, ctx, qc, "free-worker", 1)
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	// Worker should have scope 'W' (default)
	w := getWorker(t, ctx, qc, workerID)
	require.Equal(t, "W", w.RecyclableScope, "new worker should have workflow scope")

	// Submit task, start workflow, succeed the task
	taskID := submitTaskForStep(t, ctx, qc, stepID, "echo ok", "alpine", 0)
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskID).Status == "A"
	}, 6*time.Second, 200*time.Millisecond)

	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: taskID, NewStatus: "S"})
	require.NoError(t, err)

	// Workflow should complete and worker should be freed to global scope
	require.Eventually(t, func() bool {
		return getWorkflowStatus(t, ctx, qc, wfID) == "S"
	}, 6*time.Second, 200*time.Millisecond, "workflow should reach S")

	require.Eventually(t, func() bool {
		return getWorker(t, ctx, qc, workerID).RecyclableScope == "G"
	}, 6*time.Second, 200*time.Millisecond, "worker should be freed to global scope after workflow completion")
}

func getWorker(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, workerID int32) *pb.Worker {
	t.Helper()
	res, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	for _, w := range res.Workers {
		if w.WorkerId == workerID {
			return w
		}
	}
	t.Fatalf("worker %d not found", workerID)
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
