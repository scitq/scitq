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

// TestOptunaLoop simulates an Optuna-style optimization loop:
// 1. Creates a step with quality scoring (regex extraction + formula)
// 2. Submits multiple "trials" with different score outputs
// 3. Verifies quality scores are extracted correctly (last regex match wins)
// 4. Verifies the best trial has the highest score
// 5. Tests SIGTERM (stop) signal for pruning
//
// All tasks are submitted upfront to keep the workflow in 'R' status
// (workflow auto-completes when all tasks are terminal — live mode will
// need to prevent this, tracked in the spec).
func TestOptunaLoop(t *testing.T) {
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

	// Create workflow with live=true (prevents auto-completion between trials)
	liveFlag := true
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{
		Name: "wf-optuna-loop",
		Live: &liveFlag,
	})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId
	qualityDef := `{"variables":{"score":"score: ([0-9.]+)","loss":"loss: ([0-9.]+)"},"formula":"score"}`
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

	// Start worker client and assign to step
	cleanupClient, _ := startClientForTest(t, serverAddr, "optuna-loop-worker", token, 2)
	defer cleanupClient()

	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.Name == "optuna-loop-worker" {
				workerID = w.WorkerId
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "worker should register")
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	// Submit all trial tasks upfront (keeps workflow in 'R' while some are still pending)
	// Trial 1: low score, multiple epochs (last regex match wins → 0.60)
	t1Resp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   `printf "epoch 1: score: 0.40, loss: 0.8\nepoch 2: score: 0.55, loss: 0.5\nepoch 3: score: 0.60, loss: 0.3\n"`,
		Container: "bare",
		Shell:     strPtr("sh"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)

	// Trial 2: high score (optimal)
	t2Resp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   `printf "score: 0.95, loss: 0.05\n"`,
		Container: "bare",
		Shell:     strPtr("sh"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)

	// Trial 3: long-running task for SIGTERM test (keeps workflow alive)
	t3Resp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "sleep 600",
		Container: "bare",
		Shell:     strPtr("sh"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)

	// Wait for trials 1 and 2 to succeed with quality scores
	for _, tid := range []int32{t1Resp.TaskId, t2Resp.TaskId} {
		tid := tid
		polls := 0
		require.Eventually(t, func() bool {
			polls++
			res, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
			if err != nil {
				t.Logf("[poll %d] ListTasks error: %v", polls, err)
				return false
			}
			for _, tk := range res.Tasks {
				if tk.TaskId == tid {
					if polls%20 == 0 { // log every 10 seconds
						t.Logf("[poll %d] task %d: status=%s quality=%v", polls, tid, tk.Status, tk.QualityScore)
					}
					return tk.Status == "S" && tk.QualityScore != nil
				}
			}
			if polls%20 == 0 {
				t.Logf("[poll %d] task %d not found in %d tasks", polls, tid, len(res.Tasks))
			}
			return false
		}, 60*time.Second, 500*time.Millisecond, fmt.Sprintf("task %d should succeed with quality score", tid))
	}

	// Verify trial 1 quality (last match: score: 0.60)
	tk1 := getTask(t, ctx, qc, t1Resp.TaskId)
	require.InDelta(t, 0.60, *tk1.QualityScore, 0.001)
	require.NotNil(t, tk1.QualityVars)
	require.Contains(t, *tk1.QualityVars, `"loss"`)

	// Verify trial 2 quality (score: 0.95)
	tk2 := getTask(t, ctx, qc, t2Resp.TaskId)
	require.InDelta(t, 0.95, *tk2.QualityScore, 0.001)

	// Verify "optimization": trial 2 score > trial 1 score
	require.Greater(t, *tk2.QualityScore, *tk1.QualityScore)

	// Wait for trial 3 to start running
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, t3Resp.TaskId).Status == "R"
	}, 60*time.Second, 500*time.Millisecond, "trial 3 should start running")

	// Send SIGTERM — simulates Optuna pruning a bad trial
	_, err = qc.SignalTask(ctx, &pb.TaskSignalRequest{TaskId: t3Resp.TaskId, Signal: "T"})
	require.NoError(t, err)

	// Task should fail (docker stop → SIGTERM → non-zero exit)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, t3Resp.TaskId).Status == "F"
	}, 60*time.Second, 500*time.Millisecond, "trial 3 should be stopped (F)")

	t.Log("Optuna loop: quality scoring OK, multi-trial comparison OK, SIGTERM pruning OK")
}
