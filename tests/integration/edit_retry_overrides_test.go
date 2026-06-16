package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// TestEditAndRetryTask_Overrides locks in the contract that
// EditAndRetryTask's optional Inputs / Resources / Depends fields
// REPLACE the clone's values instead of inheriting them from the
// parent. Workflow-extend's fan-in case (re-running a compile/grouped
// task when new samples are added) depends on this; without the
// overrides the clone aggregates only the original sample set,
// silently producing stale output.
//
// The test exercises both branches in the same setup:
//   - Override branch: pass new inputs / resources / depends and assert
//     the clone reflects them, NOT the parent.
//   - Legacy branch: pass nothing and assert the clone inherits from
//     the parent (the pre-extend operator-retry contract).
func TestEditAndRetryTask_Overrides(t *testing.T) {
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

	// Workflow + step for the test.
	wfName := fmt.Sprintf("edit-retry-overrides-%d", time.Now().UnixNano())
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "fanin"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	shell := "sh"

	// Two prerequisite tasks A, B. Submitted with status P, then marked
	// S so dependency resolution accepts them as completed predecessors.
	mkPred := func(name string) int32 {
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:   "true",
			Shell:     &shell,
			Container: "bare",
			Status:    "P",
			StepId:    &stepID,
			TaskName:  strPtr(name),
		})
		require.NoError(t, err)
		_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    sub.TaskId,
			NewStatus: "S",
		})
		require.NoError(t, err)
		return sub.TaskId
	}
	idA := mkPred("pred-a")
	idB := mkPred("pred-b")

	// Parent task that aggregates A + B. We submit it depending on A & B
	// with concrete inputs/resources, then mark it failed so we can
	// edit-and-retry it. We submit TWO of these so we can test both the
	// override branch and the legacy (no-override) branch in one run.
	mkParent := func(name, cmd string) int32 {
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:    cmd,
			Shell:      &shell,
			Container:  "bare",
			Status:     "P",
			StepId:     &stepID,
			TaskName:   strPtr(name),
			Input:      []string{"s3://bucket/A.tsv", "s3://bucket/B.tsv"},
			Resource:   []string{"s3://ref/index.v1"},
			Dependency: []int32{idA, idB},
		})
		require.NoError(t, err)
		_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    sub.TaskId,
			NewStatus: "F",
		})
		require.NoError(t, err)
		return sub.TaskId
	}
	parentOverride := mkParent("parent-override", "aggregate A B")
	parentLegacy := mkParent("parent-legacy", "aggregate A B")

	// A third prerequisite (representing a "new sample" in the extend
	// flow). Mark it S so the new clone's dependency rewiring resolves.
	idD := mkPred("pred-d")

	// ----- Override branch -----
	//
	// EditAndRetry with new inputs / resources / depends. The clone must
	// reflect THESE values, not the parent's.
	newCmd := "aggregate A B D"
	newInputs := []string{"s3://bucket/A.tsv", "s3://bucket/B.tsv", "s3://bucket/D.tsv"}
	newResources := []string{"s3://ref/index.v1", "s3://ref/index.v2"}
	newDepends := []int32{idA, idB, idD}

	editResp, err := qc.EditAndRetryTask(ctx, &pb.EditAndRetryTaskRequest{
		TaskId:    parentOverride,
		Command:   newCmd,
		Inputs:    &pb.StringList{Values: newInputs},
		Resources: &pb.StringList{Values: newResources},
		Depends:   &pb.Int32List{Values: newDepends},
	})
	require.NoError(t, err)
	cloneOverride := editResp.TaskId

	cloneOverrideTask := findTaskIncludingHidden(t, ctx, qc, cloneOverride)
	require.Equal(t, newCmd, cloneOverrideTask.Command, "override clone command")
	require.Equal(t, newInputs, cloneOverrideTask.Input, "override clone inputs replaced")
	require.Equal(t, newResources, cloneOverrideTask.Resource, "override clone resources replaced")

	gotDeps := readTaskDependencies(t, serverAddr, cloneOverride)
	sort.Slice(gotDeps, func(i, j int) bool { return gotDeps[i] < gotDeps[j] })
	wantDeps := append([]int32(nil), newDepends...)
	sort.Slice(wantDeps, func(i, j int) bool { return wantDeps[i] < wantDeps[j] })
	require.Equal(t, wantDeps, gotDeps, "override clone depends replaced")

	// ----- Legacy branch (no overrides) -----
	//
	// Same EditAndRetryTask call but without the override fields. Clone
	// must inherit parent's inputs/resources/depends — pre-extend
	// behavior preserved for the operator-driven retry path.
	editResp2, err := qc.EditAndRetryTask(ctx, &pb.EditAndRetryTaskRequest{
		TaskId:  parentLegacy,
		Command: "aggregate A B (no override)",
	})
	require.NoError(t, err)
	cloneLegacy := editResp2.TaskId

	cloneLegacyTask := findTaskIncludingHidden(t, ctx, qc, cloneLegacy)
	require.Equal(t, []string{"s3://bucket/A.tsv", "s3://bucket/B.tsv"}, cloneLegacyTask.Input,
		"legacy clone inherits parent inputs")
	require.Equal(t, []string{"s3://ref/index.v1"}, cloneLegacyTask.Resource,
		"legacy clone inherits parent resources")

	gotDepsLegacy := readTaskDependencies(t, serverAddr, cloneLegacy)
	sort.Slice(gotDepsLegacy, func(i, j int) bool { return gotDepsLegacy[i] < gotDepsLegacy[j] })
	wantDepsLegacy := []int32{idA, idB}
	sort.Slice(wantDepsLegacy, func(i, j int) bool { return wantDepsLegacy[i] < wantDepsLegacy[j] })
	require.Equal(t, wantDepsLegacy, gotDepsLegacy, "legacy clone inherits parent depends")
}

// findTaskIncludingHidden locates a task by id even when it's been
// hidden (the parents in this test get hidden as part of retry). The
// default getTask helper uses ShowHidden=false so it misses them.
func findTaskIncludingHidden(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, taskID int32) *pb.Task {
	t.Helper()
	show := true
	lt, err := qc.ListTasks(ctx, &pb.ListTasksRequest{ShowHidden: &show})
	require.NoError(t, err)
	for _, tk := range lt.Tasks {
		if tk.TaskId == taskID {
			return tk
		}
	}
	t.Fatalf("task %d not found (with ShowHidden=true)", taskID)
	return nil
}

// readTaskDependencies reads the prerequisite_task_id list for a given
// dependent task directly from Postgres. Used to assert that retry
// produced the right rows in task_dependencies — there's no public
// RPC that returns this list verbatim.
func readTaskDependencies(t *testing.T, serverAddr string, dependentTaskID int32) []int32 {
	t.Helper()
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var ids pq.Int32Array
	err = db.QueryRow(`
		SELECT COALESCE(array_agg(prerequisite_task_id), '{}')
		FROM task_dependencies
		WHERE dependent_task_id = $1
	`, dependentTaskID).Scan(&ids)
	require.NoError(t, err)
	out := make([]int32, len(ids))
	copy(out, ids)
	return out
}
