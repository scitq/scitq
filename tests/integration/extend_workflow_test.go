package integration_test

import (
	"context"
	"os/user"
	"sort"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// A parameterized Python DSL template used as the extend fixture. Two steps,
// qc -> align (data-flow dependency per sample). `cmd_version` is injected only
// into the qc command, so changing it drifts qc but not align — exactly what
// the cascade needs to be exercised. No worker_pool, so no recruiter and no
// execution: tasks stay P/W, which is all the reconcile (a compile-time
// operation) needs.
const extendTemplateSrc = `from scitq2 import *

class Params(metaclass=ParamSpec):
    samples = Param.string(required=True, help="comma-separated sample tags")
    cmd_version = Param.string(default="v1", help="token injected into the qc command (drift testing)")

def pipeline(params: Params):
    workflow = Workflow(
        name="extend-e2e",
        description="extend reconcile E2E fixture",
        version="1.0.0",
        language=Shell("sh"),
        container="alpine",
    )
    for s in params.samples.split(","):
        s = s.strip()
        qc = workflow.Step(
            name="qc",
            tag=s,
            command="echo qc " + params.cmd_version + " " + s,
        )
        workflow.Step(
            name="align",
            tag=s,
            command="echo align " + s,
            inputs=qc.output(),
        )

run(pipeline)
`

// visibleTasks returns name->Task for non-hidden tasks of a workflow.
func visibleTasks(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, wfID int32) map[string]*pb.Task {
	t.Helper()
	resp, err := qc.ListTasks(ctx, &pb.ListTasksRequest{WorkflowIdFilter: &wfID})
	require.NoError(t, err)
	out := map[string]*pb.Task{}
	for _, tk := range resp.Tasks {
		if tk.TaskName != nil {
			out[*tk.TaskName] = tk
		}
	}
	return out
}

func taskNames(m map[string]*pb.Task) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// TestExtendWorkflowE2E drives the real server + the real Python DSL (via
// RunTemplate -> scriptRunner -> venv) to exercise the "extend an existing
// workflow" reconcile end-to-end: new tags into existing steps, command-drift
// edit-and-retry with cascade, and the conservative retry-failed-only branch.
func TestExtendWorkflowE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Run template scripts as the current user — the test process isn't root,
	// so it can't drop to the default "nobody". (defaults.Set only fills the
	// zero value, so a non-empty username here survives.)
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

	// --- Upload the template ---
	// The server bootstraps its Python venv (installing scitq2 from source)
	// asynchronously; startServerForTest only waits for the gRPC server. Poll
	// the upload until the venv can import scitq2 (metadata extraction runs in
	// that venv).
	var up *pb.UploadTemplateResponse
	require.Eventually(t, func() bool {
		var err error
		up, err = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(extendTemplateSrc),
			Filename: strPtr("extend_e2e.py"),
			Force:    true,
		})
		return err == nil && up.Success
	}, 120*time.Second, 2*time.Second, "template upload should succeed once the venv is ready")
	require.NotNil(t, up.WorkflowTemplateId)
	tmplID := *up.WorkflowTemplateId

	runTemplate := func(paramsJSON string, extendID *int32, retryFailedOnly bool) *pb.TemplateRun {
		req := &pb.RunTemplateRequest{
			WorkflowTemplateId: tmplID,
			ParamValuesJson:    paramsJSON,
			RetryFailedOnly:    retryFailedOnly,
		}
		if extendID != nil {
			req.ExtendWorkflowId = extendID
		}
		tr, err := qc.RunTemplate(ctx, req)
		require.NoError(t, err, "RunTemplate failed")
		return tr
	}

	// --- 1. Initial run: create the workflow with samples S1, S2 ---
	runTemplate(`{"samples":"S1,S2"}`, nil, false)
	nameLike := "extend-e2e"
	wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
	require.NoError(t, err)
	require.Len(t, wfList.Workflows, 1, "first run must produce exactly one workflow")
	wfID := wfList.Workflows[0].WorkflowId

	v := visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{"qc.S1", "qc.S2", "align.S1", "align.S2"}, taskNames(v),
		"initial run creates qc+align for each sample")
	require.Contains(t, v["qc.S1"].Command, "v1", "qc command carries the cmd_version token")

	// Record the original ids so we can prove (A) references the existing tasks.
	origQCS1 := v["qc.S1"].TaskId
	origAlignS1 := v["align.S1"].TaskId

	// --- 2. (A/B) Extend with a new sample S3: new tags added, existing referenced ---
	runTemplate(`{"samples":"S1,S2,S3"}`, &wfID, false)
	v = visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{
		"qc.S1", "qc.S2", "qc.S3", "align.S1", "align.S2", "align.S3",
	}, taskNames(v), "new sample's tasks added to the existing steps")
	require.Equal(t, origQCS1, v["qc.S1"].TaskId, "existing qc.S1 referenced, not recreated")
	require.Equal(t, origAlignS1, v["align.S1"].TaskId, "existing align.S1 referenced, not recreated")

	// Steps were reused (not duplicated): exactly one qc and one align step.
	steps, err := qc.ListSteps(ctx, &pb.StepFilter{WorkflowId: wfID})
	require.NoError(t, err)
	stepNames := map[string]int{}
	for _, s := range steps.Steps {
		stepNames[s.Name]++
	}
	require.Equal(t, 1, stepNames["qc"], "qc step reused, not duplicated")
	require.Equal(t, 1, stepNames["align"], "align step reused, not duplicated")

	// --- 3. (C default) Extend with drifted qc command: qc edited + align cascades ---
	runTemplate(`{"samples":"S1,S2,S3","cmd_version":"v2"}`, &wfID, false)
	v = visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{
		"qc.S1", "qc.S2", "qc.S3", "align.S1", "align.S2", "align.S3",
	}, taskNames(v), "same set of tags after a drift reconcile")
	require.Contains(t, v["qc.S1"].Command, "v2", "qc.S1 re-run with the drifted command")
	require.NotEqual(t, origQCS1, v["qc.S1"].TaskId, "qc.S1 is a fresh clone after edit-and-retry")
	// Cascade: align.S1's own command didn't change, but its prerequisite (qc.S1)
	// was re-run, so it must have been re-run too — i.e. it's a new clone.
	require.NotEqual(t, origAlignS1, v["align.S1"].TaskId,
		"align.S1 cascade-retried because its prerequisite qc.S1 changed")
	require.NotNil(t, v["align.S1"].PreviousTaskId, "cascade clone carries lineage")

	driftQCS1 := v["qc.S1"].TaskId
	driftAlignS1 := v["align.S1"].TaskId

	// --- 4. (C retry_failed_only) Further drift, but no task is failed: nothing changes ---
	runTemplate(`{"samples":"S1,S2,S3","cmd_version":"v3"}`, &wfID, true)
	v = visibleTasks(t, ctx, qc, wfID)
	require.Equal(t, driftQCS1, v["qc.S1"].TaskId,
		"retry_failed_only must NOT touch a non-failed task even with command drift")
	require.Equal(t, driftAlignS1, v["align.S1"].TaskId,
		"retry_failed_only must NOT cascade")
	for name, tk := range v {
		if strings.HasPrefix(name, "qc.") {
			require.NotContains(t, tk.Command, "v3",
				"retry_failed_only left the drifted command unapplied on healthy task %s", name)
		}
	}
}
