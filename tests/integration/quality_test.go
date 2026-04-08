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

func TestQualityScoring(t *testing.T) {
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

	// Create workflow + step with quality definition
	wfID := createWorkflowViaCLI(t, c, "wf-quality-test")
	qualityDef := `{"variables":{"accuracy":"accuracy: ([0-9.]+)","loss":"loss: ([0-9.]+)"},"formula":"accuracy"}`
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId:        &wfID,
		Name:              "train",
		QualityDefinition: &qualityDef,
	})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Start workflow
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	// Start worker client and assign to step after it registers
	cleanupClient, _ := startClientForTest(t, serverAddr, "quality-worker", token, 1)
	defer cleanupClient()

	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.Name == "quality-worker" {
				workerID = w.WorkerId
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "worker should register")
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	// Submit task that outputs quality metrics to stdout
	// The command prints multiple iterations — quality extraction takes the LAST match
	taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   `printf "epoch 1: accuracy: 0.65, loss: 0.8\nepoch 2: accuracy: 0.78, loss: 0.5\nepoch 3: accuracy: 0.847, loss: 0.3\n"`,
		Container: "alpine",
		Shell:     strPtr("sh"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)
	taskID := taskResp.TaskId

	// Wait for the task to succeed
	require.Eventually(t, func() bool {
		res, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
		if err != nil {
			return false
		}
		for _, tk := range res.Tasks {
			if tk.TaskId == taskID && tk.Status == "S" {
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond, "task should reach S")

	// Give the async quality extraction a moment to complete
	time.Sleep(1 * time.Second)

	// Verify quality score is populated
	res, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
	require.NoError(t, err)

	var task *pb.Task
	for _, tk := range res.Tasks {
		if tk.TaskId == taskID {
			task = tk
			break
		}
	}
	require.NotNil(t, task, "task should exist")
	require.NotNil(t, task.QualityScore, "quality_score should be set")

	// The formula is "accuracy", and the last match for accuracy is 0.847
	require.InDelta(t, 0.847, *task.QualityScore, 0.001, "quality score should be 0.847 (last accuracy)")

	// Verify quality_vars contains both variables
	require.NotNil(t, task.QualityVars, "quality_vars should be set")
	require.Contains(t, *task.QualityVars, "accuracy")
	require.Contains(t, *task.QualityVars, "loss")
}

func TestSignalStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)
	_ = c // not used further

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Submit a long-running task
	taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "sleep 300",
		Container: "alpine",
		Shell:     strPtr("sh"),
		Status:    "P",
	})
	require.NoError(t, err)
	taskID := taskResp.TaskId

	// Start a worker
	cleanupClient, _ := startClientForTest(t, serverAddr, "signal-worker", token, 1)
	defer cleanupClient()

	// Wait for the task to start running
	require.Eventually(t, func() bool {
		res, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
		if err != nil {
			return false
		}
		for _, tk := range res.Tasks {
			if tk.TaskId == taskID && tk.Status == "R" {
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond, "task should reach R")

	// Send SIGTERM (T) signal
	_, err = qc.SignalTask(ctx, &pb.TaskSignalRequest{TaskId: taskID, Signal: "T"})
	require.NoError(t, err)

	// Task should fail (SIGTERM causes non-zero exit)
	require.Eventually(t, func() bool {
		res, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
		if err != nil {
			return false
		}
		for _, tk := range res.Tasks {
			if tk.TaskId == taskID && tk.Status == "F" {
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond, "task should reach F after SIGTERM")
}
