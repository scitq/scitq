package integration_test

import (
	"context"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestAdhocTemplateRun exercises the local-Python-DSL provenance path:
// RegisterAdhocRun creates a template_run row with NULL workflow_template_id,
// UpdateTemplateRun later attaches the workflow_id, and both ListTemplateRuns
// and ListWorkflows surface the script identity so the UI/CLI can answer
// "what launched this workflow?" without a follow-up query.
func TestAdhocTemplateRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	// --- 1. Register an ad-hoc run
	const scriptName = "my_local_pipeline.py"
	const scriptSha = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	tr, err := qc.RegisterAdhocRun(ctx, &pb.RegisterAdhocRunRequest{
		ScriptName:      scriptName,
		ScriptSha256:    scriptSha,
		ParamValuesJson: `{"sample":"A"}`,
		ModulePinsJson:  `{"scitq2":"0.7.4-test"}`,
	})
	require.NoError(t, err)
	require.NotZero(t, tr.TemplateRunId)
	require.Equal(t, int32(0), tr.WorkflowTemplateId, "ad-hoc runs have NULL workflow_template_id (serialised as 0)")
	trID := tr.TemplateRunId

	// Missing fields must be rejected — traceability has no value without identity.
	_, err = qc.RegisterAdhocRun(ctx, &pb.RegisterAdhocRunRequest{ScriptSha256: scriptSha})
	require.Error(t, err, "empty script_name should be rejected")
	_, err = qc.RegisterAdhocRun(ctx, &pb.RegisterAdhocRunRequest{ScriptName: scriptName})
	require.Error(t, err, "empty script_sha256 should be rejected")

	// --- 2. Create a workflow and link it
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: "adhoc-test-workflow"})
	require.NoError(t, err)
	_, err = qc.UpdateTemplateRun(ctx, &pb.UpdateTemplateRunRequest{
		TemplateRunId: trID,
		WorkflowId:    &wfResp.WorkflowId,
	})
	require.NoError(t, err)

	// --- 3. ListTemplateRuns surfaces the ad-hoc identity
	trList, err := qc.ListTemplateRuns(ctx, &pb.TemplateRunFilter{})
	require.NoError(t, err)
	var found *pb.TemplateRun
	for _, r := range trList.Runs {
		if r.TemplateRunId == trID {
			found = r
			break
		}
	}
	require.NotNil(t, found, "ad-hoc run must appear in ListTemplateRuns (LEFT JOIN against workflow_template)")
	require.Equal(t, int32(0), found.WorkflowTemplateId)
	require.NotNil(t, found.ScriptName)
	require.Equal(t, scriptName, *found.ScriptName)
	require.NotNil(t, found.ScriptSha256)
	require.Equal(t, scriptSha, *found.ScriptSha256)
	require.NotNil(t, found.WorkflowId)
	require.Equal(t, wfResp.WorkflowId, *found.WorkflowId)
	require.Nil(t, found.TemplateName, "ad-hoc runs have no template name")

	// --- 4. ListWorkflows carries the launch provenance
	nameLike := "adhoc-test-workflow"
	wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
	require.NoError(t, err)
	require.Len(t, wfList.Workflows, 1)
	wf := wfList.Workflows[0]
	require.NotNil(t, wf.TemplateRunId, "workflow should know its template_run")
	require.Equal(t, trID, *wf.TemplateRunId)
	require.NotNil(t, wf.ScriptName)
	require.Equal(t, scriptName, *wf.ScriptName)
	require.Nil(t, wf.TemplateName, "ad-hoc workflow has no template name")
}
