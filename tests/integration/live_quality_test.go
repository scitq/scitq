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

// TestLiveQualityMonitoring tests that quality scores are observable while a task
// is still running — the foundation for Optuna pruning. The task outputs quality
// metrics with delays between epochs, simulating a training loop with decaying quality.
//
// The test verifies:
// 1. Quality score is updated mid-execution (not just on S)
// 2. The score reflects the latest epoch's output (last match wins)
// 3. A pruning decision can be made and the task SIGTERMed based on observed quality
func TestLiveQualityMonitoring(t *testing.T) {
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

	liveFlag := true
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{
		Name: "wf-live-quality",
		Live: &liveFlag,
	})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId
	qualityDef := `{"variables":{"score":"score: ([0-9.]+)"},"formula":"score"}`
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId:        &wfID,
		Name:              "train",
		QualityDefinition: &qualityDef,
	})
	require.NoError(t, err)
	stepID := stepResp.StepId

	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	cleanupClient, _ := startClientForTest(t, serverAddr, "live-quality-worker", token, 1)
	defer cleanupClient()

	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.Name == "live-quality-worker" {
				workerID = w.WorkerId
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "worker should register")
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	// Submit a task that outputs quality metrics with delays:
	// epoch 1: score 0.90 (good start)
	// epoch 2: score 0.80 (decaying)
	// epoch 3: score 0.70 (still decaying)
	// then sleeps 300s (simulates continued training — will be pruned)
	// Uses a script with explicit echo (not printf) for unbuffered line output.
	taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo 'epoch 1: score: 0.90'\nsleep 3\necho 'epoch 2: score: 0.80'\nsleep 3\necho 'epoch 3: score: 0.70'\nsleep 300",
		Container: "bare",
		Shell:     strPtr("bash"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)
	taskID := taskResp.TaskId

	// Wait for the task to start running
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskID).Status == "R"
	}, 30*time.Second, 500*time.Millisecond, "task should start running")

	// Wait for the first quality score to appear (epoch 1 should be captured after ~2s)
	// This tests that quality extraction happens DURING execution, not just on S.
	var firstScore *float64
	require.Eventually(t, func() bool {
		tk := getTask(t, ctx, qc, taskID)
		if tk.QualityScore != nil {
			firstScore = tk.QualityScore
			return true
		}
		return false
	}, 20*time.Second, 1*time.Second, "quality score should be observable while task is running")

	t.Logf("First observed quality score: %.2f (task still running)", *firstScore)
	require.True(t, getTask(t, ctx, qc, taskID).Status == "R", "task should still be running when quality is observed")

	// Wait a bit for more epochs and check that quality updates (decays)
	time.Sleep(5 * time.Second)
	tk := getTask(t, ctx, qc, taskID)
	require.NotNil(t, tk.QualityScore)
	t.Logf("Later quality score: %.2f", *tk.QualityScore)

	// Score should have decayed (later epochs have lower scores)
	require.Less(t, *tk.QualityScore, *firstScore,
		"quality should decay as later epochs output lower scores")

	// Pruning decision: quality is declining, stop the task
	_, err = qc.SignalTask(ctx, &pb.TaskSignalRequest{TaskId: taskID, Signal: "T"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskID).Status == "F"
	}, 30*time.Second, 500*time.Millisecond, "task should be stopped after pruning")

	t.Log("Live quality monitoring: mid-execution observation OK, decay detection OK, pruning OK")
}
