package integration_test

import (
	"context"
	"database/sql"
	"os/user"
	"sort"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/lib/pq"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// extendFanInTemplateSrc declares a fan-in / grouped-output step at the
// bottom of a DAG. The compile step's inputs are
// qc.output(grouped=True), so the DSL must enumerate ALL qc tasks
// (including existing ones from a prior run that aren't in this run's
// sample list) when resolving them. Without that widening, an extend
// produces a compile clone that aggregates only the new samples and
// silently drops the previously-processed ones — exactly the
// failure mode the no-silent-filtering principle is in place to
// prevent. With it, the override path on EditAndRetryTask receives
// the full input/depends list and the clone reflects the union.
const extendFanInTemplateSrc = `from scitq2 import *

class Params(metaclass=ParamSpec):
    samples = Param.string(required=True, help="comma-separated sample tags")

def pipeline(params: Params):
    workflow = Workflow(
        name="extend-fanin-e2e",
        description="extend reconcile fan-in fixture",
        version="1.0.0",
        language=Shell("sh"),
        container="alpine",
        # publish_root + Outputs(publish=True) on the qc step give every
        # qc task a concrete publish URI. Without it the workflow has no
        # workspace_root (no provider configured for this test fixture)
        # and qc.output(grouped=True) resolves to a list of Nones, which
        # the input-list builder filters out — leaving compile with zero
        # inputs and defeating the test's count assertions.
        publish_root="s3://test/extend-fanin/",
    )
    qc = None
    for s in params.samples.split(","):
        s = s.strip()
        qc = workflow.Step(
            name="qc",
            tag=s,
            command="echo qc " + s,
            outputs=Outputs(publish=True),
        )
    workflow.Step(
        name="compile",
        command="echo compile",
        inputs=qc.output(grouped=True),
    )

run(pipeline)
`

// readTaskInputs returns the task's input list from the DB. The proto
// surfaces this via Task.Input on ListTasks, so the DB read is mostly
// to lock the assertion down to canonical Postgres order (the array
// column preserves insertion order).
func readTaskInputs(t *testing.T, serverAddr string, taskID int32) []string {
	t.Helper()
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var arr pq.StringArray
	err = db.QueryRow(`SELECT COALESCE(input, '{}') FROM task WHERE task_id = $1`, taskID).Scan(&arr)
	require.NoError(t, err)
	return []string(arr)
}

// TestExtendFanInE2E drives the real server + Python DSL (RunTemplate
// → scriptRunner → venv) to exercise the workflow-extend behavior for
// a fan-in / grouped-output step.
//
// Phase 1 runs the template with samples = S1,S2. The compile task
// aggregates qc.S1 and qc.S2 — two inputs, two dependencies.
//
// Phase 2 extends the SAME workflow with samples = S3,S4 (NOT the
// full set; S1 and S2 are intentionally omitted from this run's
// param list). What the DSL must do:
//   - submit fresh qc.S3 and qc.S4
//   - synthesise reference Task objects for qc.S1 and qc.S2 (already
//     in the workflow, but not in this run's iteration) so that
//     qc.output(grouped=True) enumerates all four
//   - re-run the existing compile task with the four-element
//     inputs/depends overrides
//
// The assertions on the compile clone (four inputs, four dependencies)
// are the actual integration coverage: they fail with either a stale
// DSL (only 2 inputs — the new ones), a stale server (parent inputs
// copied — the old 2 inputs only), or a wiring bug between the two
// (3 inputs because one ref was lost).
func TestExtendFanInE2E(t *testing.T) {
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

	// Wait for the venv → upload template.
	var up *pb.UploadTemplateResponse
	require.Eventually(t, func() bool {
		var err error
		up, err = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(extendFanInTemplateSrc),
			Filename: strPtr("extend_fanin_e2e.py"),
			Force:    true,
		})
		return err == nil && up.Success
	}, 120*time.Second, 2*time.Second, "template upload should succeed once the venv is ready")
	require.NotNil(t, up.WorkflowTemplateId)
	tmplID := *up.WorkflowTemplateId

	runTemplate := func(paramsJSON string, extendID *int32) {
		req := &pb.RunTemplateRequest{
			WorkflowTemplateId: tmplID,
			ParamValuesJson:    paramsJSON,
		}
		if extendID != nil {
			req.ExtendWorkflowId = extendID
		}
		_, err := qc.RunTemplate(ctx, req)
		require.NoError(t, err, "RunTemplate failed")
	}

	readDeps := func(taskID int32) []int32 {
		deps := readTaskDependencies(t, serverAddr, taskID)
		sort.Slice(deps, func(i, j int) bool { return deps[i] < deps[j] })
		return deps
	}

	// --- Phase 1: initial run with S1, S2 ---
	runTemplate(`{"samples":"S1,S2"}`, nil)
	nameLike := "extend-fanin-e2e"
	wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
	require.NoError(t, err)
	require.Len(t, wfList.Workflows, 1, "first run produces exactly one workflow")
	wfID := wfList.Workflows[0].WorkflowId

	v := visibleTasks(t, ctx, qc, wfID)
	require.ElementsMatch(t, []string{"qc.S1", "qc.S2", "compile"}, taskNames(v),
		"phase 1: two qc tasks + one compile task")

	qcS1 := v["qc.S1"].TaskId
	qcS2 := v["qc.S2"].TaskId
	initialCompile := v["compile"].TaskId

	initialDeps := readDeps(initialCompile)
	wantInitialDeps := []int32{qcS1, qcS2}
	sort.Slice(wantInitialDeps, func(i, j int) bool { return wantInitialDeps[i] < wantInitialDeps[j] })
	require.Equal(t, wantInitialDeps, initialDeps,
		"phase 1: compile depends on the two qc tasks")
	require.Len(t, readTaskInputs(t, serverAddr, initialCompile), 2,
		"phase 1: compile has two inputs (S1, S2 paths)")

	// --- Phase 2: extend with S3, S4 only ---
	//
	// The new param list deliberately drops S1 and S2 — the DSL must
	// pull them back in as references so the compile clone still sees
	// all four samples. If the DSL is stale, compile aggregates only
	// S3/S4. If the server is stale, compile clones with the parent's
	// (S1/S2-only) inputs and never picks up S3/S4. Either way the
	// inputs/depends-count assertion below fires.
	runTemplate(`{"samples":"S3,S4"}`, &wfID)

	v = visibleTasks(t, ctx, qc, wfID)
	// Visible (non-hidden) tasks now: qc.S1, qc.S2, qc.S3, qc.S4, and
	// the NEW compile clone. The original compile task is hidden.
	require.ElementsMatch(t, []string{
		"qc.S1", "qc.S2", "qc.S3", "qc.S4", "compile",
	}, taskNames(v), "phase 2: existing qc tasks preserved, new ones added")

	require.Equal(t, qcS1, v["qc.S1"].TaskId,
		"phase 2: qc.S1 referenced (not recreated) — reference-task path didn't drop it")
	require.Equal(t, qcS2, v["qc.S2"].TaskId,
		"phase 2: qc.S2 referenced (not recreated)")
	qcS3 := v["qc.S3"].TaskId
	qcS4 := v["qc.S4"].TaskId

	newCompile := v["compile"].TaskId
	require.NotEqual(t, initialCompile, newCompile,
		"phase 2: compile was edit-and-retried — its dependencies changed (new prereqs)")

	// The decisive assertion: the compile clone's inputs and dependencies
	// must include ALL FOUR qc tasks. Pre-PR2 this would have shown the
	// original two inputs (server copied from parent) or only the two
	// new ones (DSL didn't widen step.tasks). With both fixes it's all
	// four.
	require.Len(t, readTaskInputs(t, serverAddr, newCompile), 4,
		"phase 2: compile clone aggregates all four qc outputs (S1+S2 from references, S3+S4 from this run)")

	gotDeps := readDeps(newCompile)
	wantDeps := []int32{qcS1, qcS2, qcS3, qcS4}
	sort.Slice(wantDeps, func(i, j int) bool { return wantDeps[i] < wantDeps[j] })
	require.Equal(t, wantDeps, gotDeps,
		"phase 2: compile clone's task_dependencies span all four samples")

	// Sanity: every sample's qc publish path shows up in the compile
	// clone's input list. Catches the rare failure mode where the count
	// matches but the values are wrong (e.g. a task path got substituted
	// by another's). The publish_root layout puts the tag at the end:
	// "<root>/qc/<tag>/" — Outputs(publish=True) joins step + tag with
	// "/", not "." (the task_name separator).
	inputs := readTaskInputs(t, serverAddr, newCompile)
	for _, tag := range []string{"S1", "S2", "S3", "S4"} {
		found := false
		for _, in := range inputs {
			if strings.Contains(in, "/qc/"+tag+"/") {
				found = true
				break
			}
		}
		require.True(t, found, "phase 2: compile inputs include qc/%s path; got %v", tag, inputs)
	}
}
