package integration_test

import (
	"context"
	"encoding/json"
	"os/user"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// A minimal YAML template (the same shape as the .py fixture in
// extend_workflow_test.go): qc -> align per sample, with cmd_version injected
// only into qc so it drifts independently of align. Samples come from a
// newline-separated `lines` iterator fed by the `samples` param. No
// worker_pool/workspace → no recruiter, no execution; tasks stay P/W, which is
// all the compile-time reconcile needs.
const extendYAMLTemplateSrc = `format: 2
name: extend-e2e-yaml
version: "1.0.0"
description: extend reconcile E2E fixture (YAML path)
language: bash
container: alpine

params:
  samples:
    type: string
    required: true
  cmd_version:
    type: string
    default: "v1"

iterate:
  name: sample
  source: lines
  content: "{params.samples}"

steps:
  - name: qc
    command: "echo qc {params.cmd_version} {SAMPLE}"
    outputs:
      txt: "*.txt"
  - name: align
    command: "echo align {SAMPLE}"
    inputs: qc.txt
`

func mustParamsJSON(t *testing.T, m map[string]string) string {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return string(b)
}

// TestExtendWorkflowYAMLE2E is the YAML-template counterpart of
// TestExtendWorkflowE2E. It guards the gap that originally shipped: extend was
// wired into runner.py (the .py path) but not yaml_runner.py, so YAML templates
// like biomscope rejected --extend-workflow.
func TestExtendWorkflowYAMLE2E(t *testing.T) {
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

	// Upload the YAML template (server keys YAML off the .yaml extension).
	var up *pb.UploadTemplateResponse
	require.Eventually(t, func() bool {
		var uerr error
		up, uerr = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(extendYAMLTemplateSrc),
			Filename: strPtr("extend_e2e.yaml"),
			Force:    true,
		})
		return uerr == nil && up.Success
	}, 120*time.Second, 2*time.Second, "YAML template upload should succeed once the venv is ready")
	require.NotNil(t, up.WorkflowTemplateId)
	tmplID := *up.WorkflowTemplateId

	runTemplate := func(params map[string]string, extendID *int32) {
		req := &pb.RunTemplateRequest{
			WorkflowTemplateId: tmplID,
			ParamValuesJson:    mustParamsJSON(t, params),
		}
		if extendID != nil {
			req.ExtendWorkflowId = extendID
		}
		_, err := qc.RunTemplate(ctx, req)
		require.NoError(t, err, "RunTemplate (YAML) failed")
	}

	// --- 1. Initial run: samples S1, S2 ---
	runTemplate(map[string]string{"samples": "S1\nS2"}, nil)
	nameLike := "extend-e2e-yaml"
	wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
	require.NoError(t, err)
	require.Len(t, wfList.Workflows, 1, "first run must produce exactly one workflow")
	wfID := wfList.Workflows[0].WorkflowId

	v := visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{"qc.S1", "qc.S2", "align.S1", "align.S2"}, taskNames(v),
		"initial YAML run creates qc+align per sample")
	require.Contains(t, v["qc.S1"].Command, "v1")
	origQCS1 := v["qc.S1"].TaskId
	origAlignS1 := v["align.S1"].TaskId

	// --- 2. Extend with a new sample S3: new tags added, existing referenced ---
	runTemplate(map[string]string{"samples": "S1\nS2\nS3"}, &wfID)
	v = visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{
		"qc.S1", "qc.S2", "qc.S3", "align.S1", "align.S2", "align.S3",
	}, taskNames(v), "new sample's tasks added to existing YAML steps")
	require.Equal(t, origQCS1, v["qc.S1"].TaskId, "existing qc.S1 referenced, not recreated")
	require.Equal(t, origAlignS1, v["align.S1"].TaskId, "existing align.S1 referenced, not recreated")

	// --- 3. Extend with drifted qc command: qc edited + align cascades ---
	runTemplate(map[string]string{"samples": "S1\nS2\nS3", "cmd_version": "v2"}, &wfID)
	v = visibleTasks(t, ctx, qc, wfID)
	require.Contains(t, v["qc.S1"].Command, "v2", "qc.S1 re-run with drifted command")
	require.NotEqual(t, origQCS1, v["qc.S1"].TaskId, "qc.S1 is a fresh clone after edit-and-retry")
	require.NotEqual(t, origAlignS1, v["align.S1"].TaskId,
		"align.S1 cascade-retried because its prerequisite qc.S1 changed")
}
