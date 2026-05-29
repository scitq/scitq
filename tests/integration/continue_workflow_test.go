package integration_test

import (
	"context"
	"os/user"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// TestContinueResolvesLastWorkflow exercises --continue: a run that resolves the
// caller's most recent run of the same template (matching params) and extends
// that workflow instead of creating a new one — without the operator passing a
// workflow id. Reuses extendTemplateSrc / visibleTasks from extend_workflow_test.go.
func TestContinueResolvesLastWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cur, err := user.Current()
	require.NoError(t, err)
	override := &config.Config{}
	override.Scitq.ScriptRunnerUser = cur.Username

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
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

	var up *pb.UploadTemplateResponse
	require.Eventually(t, func() bool {
		var uerr error
		up, uerr = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(extendTemplateSrc),
			Filename: strPtr("continue_e2e.py"),
			Force:    true,
		})
		return uerr == nil && up.Success
	}, 120*time.Second, 2*time.Second, "template upload should succeed once the venv is ready")
	tmplID := *up.WorkflowTemplateId

	// --- 1. Initial run (creates the workflow) ---
	_, err = qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{"samples":"S1,S2"}`,
	})
	require.NoError(t, err)

	// ListWorkflows returns one row per template_run (provenance), so a
	// workflow with N runs shows N rows — count DISTINCT workflow ids.
	nameLike := "extend-e2e"
	distinctWfIDs := func() map[int32]bool {
		wl, e := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
		require.NoError(t, e)
		ids := map[int32]bool{}
		for _, w := range wl.Workflows {
			ids[w.WorkflowId] = true
		}
		return ids
	}
	ids := distinctWfIDs()
	require.Len(t, ids, 1)
	var wfID int32
	for id := range ids {
		wfID = id
	}
	origQCS1 := visibleTasks(t, ctx, qc, wfID)["qc.S1"].TaskId

	// --- 2. --continue with the SAME params resolves and extends that workflow ---
	// (Same template version + same params → nothing drifted → all tasks are
	// referenced; the proof that --continue worked is that NO new workflow was
	// created and the existing task ids are unchanged.)
	_, err = qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{"samples":"S1,S2"}`,
		ContinueLast:       true,
	})
	require.NoError(t, err, "--continue should resolve the prior workflow")

	require.Equal(t, map[int32]bool{wfID: true}, distinctWfIDs(),
		"--continue must extend the existing workflow, not create a new one")
	require.Equal(t, origQCS1, visibleTasks(t, ctx, qc, wfID)["qc.S1"].TaskId,
		"existing task referenced on --continue, not recreated")

	// Param order/whitespace shouldn't matter: an equivalent (re-formatted)
	// param set still resolves the same workflow.
	_, err = qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{ "samples" : "S1,S2" }`,
		ContinueLast:       true,
	})
	require.NoError(t, err, "--continue should match on canonical params (whitespace-insensitive)")
	require.Equal(t, map[int32]bool{wfID: true}, distinctWfIDs(),
		"canonical param match must resolve the same workflow")

	// --- 3. --continue with params that never ran → hard error (no silent create) ---
	_, err = qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{"samples":"ZZZ"}`,
		ContinueLast:       true,
	})
	require.Error(t, err, "--continue with unseen params must fail, not create a new workflow")

	// --- 4. --continue + --extend-workflow is rejected (mutually exclusive) ---
	_, err = qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{"samples":"S1,S2"}`,
		ContinueLast:       true,
		ExtendWorkflowId:   &wfID,
	})
	require.Error(t, err, "continue_last and extend_workflow_id are mutually exclusive")
}
