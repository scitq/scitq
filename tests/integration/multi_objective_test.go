package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestMultiObjectiveQuality verifies that multi-objective quality definitions
// extract all objectives and store them correctly:
// 1. A step with objectives (not single formula) extracts multiple scores
// 2. quality_score stores the first objective
// 3. quality_vars contains both extracted variables and the scores array
func TestMultiObjectiveQuality(t *testing.T) {
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

	// Create workflow + step with multi-objective quality definition
	wfID := createWorkflowViaCLI(t, c, "wf-multi-obj")
	qualityDef := `{
		"variables": {
			"train_auc": "train_auc: ([0-9.]+)",
			"test_auc": "test_auc: ([0-9.]+)"
		},
		"objectives": [
			{"formula": "test_auc", "direction": "maximize"},
			{"formula": "test_auc - train_auc", "direction": "maximize"}
		]
	}`
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId:        &wfID,
		Name:              "train",
		QualityDefinition: &qualityDef,
	})
	require.NoError(t, err)
	stepID := stepResp.StepId

	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprintf("%d", wfID), "--status", "R"})
	require.NoError(t, err)

	cleanupClient, _ := startClientForTest(t, serverAddr, "multi-obj-worker", token, 1)
	defer cleanupClient()

	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.Name == "multi-obj-worker" {
				workerID = w.WorkerId
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond, "worker should register")
	_, err = runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprintf("%d", workerID), "--step-id", fmt.Sprintf("%d", stepID)})
	require.NoError(t, err)

	// Task outputs both train and test AUC
	// train_auc=0.95, test_auc=0.82 → objectives: [0.82, 0.82-0.95=-0.13]
	taskResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   `printf "train_auc: 0.95\ntest_auc: 0.82\n"`,
		Container: "bare",
		Shell:     strPtr("sh"),
		StepId:    &stepID,
		Status:    "P",
	})
	require.NoError(t, err)
	taskID := taskResp.TaskId

	// Wait for task to succeed with quality score
	require.Eventually(t, func() bool {
		tk := getTask(t, ctx, qc, taskID)
		return tk.Status == "S" && tk.QualityScore != nil
	}, 60*time.Second, 500*time.Millisecond, "task should succeed with quality score")

	tk := getTask(t, ctx, qc, taskID)

	// quality_score should be the first objective (test_auc = 0.82)
	require.NotNil(t, tk.QualityScore)
	require.InDelta(t, 0.82, *tk.QualityScore, 0.001, "quality_score should be first objective (test_auc)")

	// quality_vars should contain the scores array
	require.NotNil(t, tk.QualityVars)
	var qvData map[string]any
	err = json.Unmarshal([]byte(*tk.QualityVars), &qvData)
	require.NoError(t, err, "quality_vars should be valid JSON")

	// Check vars
	vars, ok := qvData["vars"].(map[string]any)
	require.True(t, ok, "quality_vars should have 'vars' key")
	require.InDelta(t, 0.95, vars["train_auc"], 0.001)
	require.InDelta(t, 0.82, vars["test_auc"], 0.001)

	// Check scores array
	scoresRaw, ok := qvData["scores"].([]any)
	require.True(t, ok, "quality_vars should have 'scores' key")
	require.Len(t, scoresRaw, 2, "should have 2 objective scores")
	require.InDelta(t, 0.82, scoresRaw[0], 0.001, "first objective: test_auc")
	require.InDelta(t, -0.13, scoresRaw[1], 0.001, "second objective: test_auc - train_auc")

	t.Logf("Multi-objective: test_auc=%.2f, gap=%.2f", scoresRaw[0], scoresRaw[1])
}
