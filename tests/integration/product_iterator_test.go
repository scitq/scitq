package integration_test

import (
	"context"
	"os/user"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// matrixYAMLSrc combines the two new YAML features:
//   - iterate.product: outer-product the sample dimension with a chromosome
//     dimension → 4 (sample × chrom) iterations.
//   - grouped_by: sample on the merge step → one merge task per sample,
//     each collecting that sample's per-chrom outputs from the call step.
// No worker_pool / workspace: tasks stay P/W, which is all the compile-time
// reconcile + dependency wiring needs to be inspectable.
const matrixYAMLSrc = `format: 2
name: matrix-pipeline
version: "1.0.0"
description: product iterator + grouped_by smoke test
language: bash
container: alpine

iterate:
  name: sample
  source: list
  values: [S1, S2]
  product:
    name: chrom
    source: list
    values: [chr1, chr2]

steps:
  - name: call
    command: 'echo "call {SAMPLE} {CHROM}"'
    outputs:
      vcf: "*.vcf"
  - name: merge
    grouped_by: sample
    command: 'echo "merge {SAMPLE}"'
    inputs: call.vcf
    outputs:
      merged: "*.merged"
`

// TestProductIteratorAndGroupedBy drives the YAML runner end-to-end through
// the real server and verifies the new dimension features land the expected
// task topology:
//   - 4 call tasks (samples × chroms), each with composite tag like S1.chr1
//   - 2 merge tasks (one per sample), each with a sample-only tag
//   - merge.S1's dependencies are call.S1.chr1 and call.S1.chr2 (and not the S2 calls)
func TestProductIteratorAndGroupedBy(t *testing.T) {
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

	// Upload + run the matrix template once the venv is ready.
	var up *pb.UploadTemplateResponse
	require.Eventually(t, func() bool {
		var uerr error
		up, uerr = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(matrixYAMLSrc),
			Filename: strPtr("matrix.yaml"),
			Force:    true,
		})
		return uerr == nil && up.Success
	}, 120*time.Second, 2*time.Second, "template upload should succeed once the venv is ready")
	require.NotNil(t, up.WorkflowTemplateId)
	tmplID := *up.WorkflowTemplateId

	tr, err := qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: tmplID,
		ParamValuesJson:    `{}`,
	})
	require.NoError(t, err)
	require.NotNil(t, tr.WorkflowId, "RunTemplate must return the workflow id")
	wfID := *tr.WorkflowId

	tasks := visibleTasks(t, ctx, qc, wfID)

	// Product iterator: expect 4 call tasks with composite tags.
	expectedCall := []string{"call.S1.chr1", "call.S1.chr2", "call.S2.chr1", "call.S2.chr2"}
	for _, name := range expectedCall {
		require.Contains(t, tasks, name, "missing per-iter task %q (got: %v)", name, taskNames(tasks))
	}

	// grouped_by: sample — expect ONE merge task per sample (S1, S2), with a
	// sample-only tag (no `.chr*` component).
	expectedMerge := []string{"merge.S1", "merge.S2"}
	for _, name := range expectedMerge {
		require.Contains(t, tasks, name, "missing grouped_by task %q (got: %v)", name, taskNames(tasks))
	}
	// Negative check: merge.S1.chr1 must NOT exist (would mean we accidentally
	// built per-iter merge tasks).
	require.NotContains(t, tasks, "merge.S1.chr1", "grouped_by:sample must collapse the chrom dimension")
	require.NotContains(t, tasks, "merge.S2.chr1", "grouped_by:sample must collapse the chrom dimension")

	// Dependency partitioning: merge.S1 must depend on the S1 calls only.
	// We verify this by status transition (the dependency-list isn't exposed
	// via gRPC on the Task response): merge tasks start in W (waiting on
	// their prereqs). Mark only the S1 calls succeeded → merge.S1 should
	// transition to P (all its deps satisfied) while merge.S2 must stay in W
	// (its deps haven't moved). If grouped_by had cross-wired the deps,
	// merge.S2 would transition too, or merge.S1 wouldn't.
	require.Equal(t, "W", tasks["merge.S1"].Status, "merge.S1 should start waiting on its deps")
	require.Equal(t, "W", tasks["merge.S2"].Status, "merge.S2 should start waiting on its deps")

	for _, name := range []string{"call.S1.chr1", "call.S1.chr2"} {
		_, err := qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId: tasks[name].TaskId, NewStatus: "S",
		})
		require.NoError(t, err, "UpdateTaskStatus %s -> S", name)
	}
	require.Eventually(t, func() bool {
		after := visibleTasks(t, ctx, qc, wfID)
		return after["merge.S1"].Status != "W" && after["merge.S2"].Status == "W"
	}, 10*time.Second, 100*time.Millisecond,
		"merge.S1 should promote out of W once its two S1 prereqs succeed; merge.S2 must stay W")

	// Sentinel use of strings to keep the import in place after the rewrite.
	_ = strings.HasPrefix
}
