package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// helper to get workflow by ID from ListWorkflows
func getWorkflow(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, wfID int32) *pb.Workflow {
	t.Helper()
	res, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
	require.NoError(t, err)
	for _, wf := range res.Workflows {
		if wf.WorkflowId == wfID {
			return wf
		}
	}
	t.Fatalf("workflow %d not found", wfID)
	return nil
}

// TestWorkflowCountersBasic verifies that the workflow progress counters
// (totalTasks, succeededTasks, failedTasks, runningTasks) are correctly
// populated from the in-memory step stats aggregator.
func TestWorkflowCountersBasic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	// Create workflow + step first (before worker, so we can assign it)
	wfName := fmt.Sprintf("counter-test-%d", time.Now().UnixNano())
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId

	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "count-step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Start a worker and assign it to the step
	cleanupClient, _ := startClientForTest(t, serverAddr, "counter-worker", workerToken, 2)
	defer cleanupClient()
	time.Sleep(3 * time.Second) // wait for worker to register
	workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, workers.Workers, "worker should be registered")
	workerID := workers.Workers[0].WorkerId
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{WorkerId: workerID, StepId: &stepID})
	require.NoError(t, err)

	// Submit 3 tasks: 2 will succeed, 1 will fail
	shell := "sh"
	task1, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo ok", Shell: &shell, Container: "alpine",
		Status: "P", StepId: &stepID,
	})
	require.NoError(t, err)

	task2, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo ok", Shell: &shell, Container: "alpine",
		Status: "P", StepId: &stepID,
	})
	require.NoError(t, err)

	task3, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "exit 1", Shell: &shell, Container: "alpine",
		Status: "P", StepId: &stepID,
	})
	require.NoError(t, err)

	// Activate workflow
	_, err = qc.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{
		WorkflowId: wfID, Status: "R",
	})
	require.NoError(t, err)

	// Check initial counters: 3 total, 0 succeeded
	wf := getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(3), wf.TotalTasks, "should have 3 total tasks")
	require.Equal(t, int32(0), wf.SucceededTasks, "no tasks succeeded yet")

	// Wait for tasks to complete
	require.Eventually(t, func() bool {
		t1 := getTask(t, ctx, qc, task1.TaskId)
		t2 := getTask(t, ctx, qc, task2.TaskId)
		t3 := getTask(t, ctx, qc, task3.TaskId)
		return (t1.Status == "S" || t1.Status == "F") &&
			(t2.Status == "S" || t2.Status == "F") &&
			(t3.Status == "S" || t3.Status == "F")
	}, 60*time.Second, 500*time.Millisecond, "all tasks should finish")

	// Verify counters
	wf = getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(3), wf.TotalTasks, "total should be 3")
	require.Equal(t, int32(2), wf.SucceededTasks, "2 tasks should succeed")
	require.Equal(t, int32(1), wf.FailedTasks, "1 task should fail")
	require.Equal(t, int32(0), wf.RunningTasks, "no tasks running")

	t.Logf("Workflow %d counters: total=%d succeeded=%d failed=%d running=%d",
		wfID, wf.TotalTasks, wf.SucceededTasks, wf.FailedTasks, wf.RunningTasks)
}

// TestWorkflowCountersRetrying verifies that the retrying counter correctly
// tracks tasks that failed and have a retry clone in progress.
func TestWorkflowCountersRetrying(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	// Create workflow + step first
	wfName := fmt.Sprintf("retry-counter-test-%d", time.Now().UnixNano())
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId

	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "retry-step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Start a worker and assign it to the step
	cleanupClient, _ := startClientForTest(t, serverAddr, "retry-counter-worker", workerToken, 1)
	defer cleanupClient()
	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil || len(workers.Workers) == 0 {
			return false
		}
		workerID = workers.Workers[0].WorkerId
		return true
	}, 15*time.Second, 500*time.Millisecond, "worker should register")
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{WorkerId: workerID, StepId: &stepID})
	require.NoError(t, err)

	// Submit a task with retry=1 that will fail on first attempt
	// The command fails, gets retried automatically, fails again (terminal)
	shell := "sh"
	retry := int32(1)
	_, err = qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "exit 1", Shell: &shell, Container: "alpine",
		Status: "P", StepId: &stepID, Retry: &retry,
	})
	require.NoError(t, err)

	// Activate workflow
	_, err = qc.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{
		WorkflowId: wfID, Status: "R",
	})
	require.NoError(t, err)

	// Wait for all retries to exhaust — check workflow counters directly
	// (original task gets hidden after retry, so we can't track by task ID)
	require.Eventually(t, func() bool {
		wf := getWorkflow(t, ctx, qc, wfID)
		return wf.FailedTasks >= 1
	}, 60*time.Second, 500*time.Millisecond, "task should eventually fail terminally after retries")

	// Final state: retrying should be 0 (all retries exhausted)
	wf := getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(0), wf.RetryingTasks, "retrying should be 0 after all retries exhausted")
	require.Equal(t, int32(1), wf.FailedTasks, "1 terminal failure")

	t.Logf("Workflow %d final: total=%d succeeded=%d failed=%d retrying=%d running=%d",
		wfID, wf.TotalTasks, wf.SucceededTasks, wf.FailedTasks, wf.RetryingTasks, wf.RunningTasks)
}

// TestWorkflowCountersRetrySuccess verifies that when a retry clone succeeds,
// the retrying counter decrements back to 0.
func TestWorkflowCountersRetrySuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	// Create workflow + step first
	wfName := fmt.Sprintf("retry-success-test-%d", time.Now().UnixNano())
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId

	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "retry-success-step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Start a worker and assign it to the step
	cleanupClient, _ := startClientForTest(t, serverAddr, "retry-success-worker", workerToken, 1)
	defer cleanupClient()
	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil || len(workers.Workers) == 0 {
			return false
		}
		workerID = workers.Workers[0].WorkerId
		return true
	}, 15*time.Second, 500*time.Millisecond, "worker should register")
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{WorkerId: workerID, StepId: &stepID})
	require.NoError(t, err)

	// Submit a task that fails, then manually retry with a command that succeeds
	shell := "sh"
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "exit 1", Shell: &shell, Container: "alpine",
		Status: "P", StepId: &stepID,
	})
	require.NoError(t, err)

	// Activate workflow
	_, err = qc.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{
		WorkflowId: wfID, Status: "R",
	})
	require.NoError(t, err)

	// Wait for task to fail
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task.TaskId).Status == "F"
	}, 60*time.Second, 500*time.Millisecond, "task should fail")

	// Check counters: 1 total, 1 failed, 0 retrying
	wf := getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(1), wf.FailedTasks, "1 failed task")
	require.Equal(t, int32(0), wf.RetryingTasks, "0 retrying (no retry clone yet)")

	// Edit and retry with a succeeding command
	editResp, err := qc.EditAndRetryTask(ctx, &pb.EditAndRetryTaskRequest{
		TaskId:  task.TaskId,
		Command: "echo fixed",
	})
	require.NoError(t, err)
	newTaskID := editResp.TaskId

	// Immediately after retry: retrying should be 1
	wf = getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(1), wf.RetryingTasks, "1 retrying (clone is pending)")

	// Wait for retry clone to succeed
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, newTaskID).Status == "S"
	}, 60*time.Second, 500*time.Millisecond, "retry clone should succeed")

	// Final state: retrying back to 0, succeeded 1
	wf = getWorkflow(t, ctx, qc, wfID)
	require.Equal(t, int32(0), wf.RetryingTasks, "retrying should be 0 after retry succeeded")
	require.Equal(t, int32(1), wf.SucceededTasks, "1 succeeded (the retry clone)")
	require.Equal(t, int32(0), wf.FailedTasks, "0 terminal failures (original was hidden)")

	t.Logf("Workflow %d after retry success: total=%d succeeded=%d failed=%d retrying=%d",
		wfID, wf.TotalTasks, wf.SucceededTasks, wf.FailedTasks, wf.RetryingTasks)
}
