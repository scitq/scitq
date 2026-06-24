package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestDeleteTask_Inactive: pending task is removed immediately, no
// worker involved. Verifies the happy path: row gone, no leftover
// dependency edges (there are none in this case, but the DELETE
// shouldn't barf either).
func TestDeleteTask_Inactive(t *testing.T) {
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

	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo doomed",
		Container: "bare",
		Status:    "P",
		TaskName:  strPtr("doomed-task"),
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	_, err = qc.DeleteTask(ctx, &pb.TaskId{TaskId: taskID})
	require.NoError(t, err)

	lt, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
	require.NoError(t, err)
	for _, tk := range lt.Tasks {
		require.NotEqual(t, taskID, tk.TaskId, "deleted task should not appear in ListTasks")
	}
}

// TestDeleteTask_NotFound: deleting a non-existent task returns a gRPC
// NotFound error rather than silently succeeding. Catches a future
// refactor that drops the "rows affected == 0" check.
func TestDeleteTask_NotFound(t *testing.T) {
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

	_, err = qc.DeleteTask(ctx, &pb.TaskId{TaskId: 999_999})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err), "want NotFound, got %v", err)
}

// TestDeleteTask_CascadesDependencyEdges: when a task with dependents
// is deleted, the task_dependencies rows that reference it on EITHER
// side go away too — the schema has ON DELETE CASCADE on both
// dependent_task_id and prerequisite_task_id (see
// 000001_init_schema.up.sql:142-143). This is the user-asserted
// scope: "remove the task, its dependencies, but that's all".
func TestDeleteTask_CascadesDependencyEdges(t *testing.T) {
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

	// A is a prerequisite of B and C — three dependency edges total
	// (A→B, A→C) get exercised in the cascade. C also depends on B so
	// the second edge (B→C) covers the dependent_task_id side too.
	a, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo A", Container: "bare", Status: "P", TaskName: strPtr("A"),
	})
	require.NoError(t, err)
	b, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo B", Container: "bare", Status: "W",
		TaskName: strPtr("B"), Dependency: []int32{a.TaskId},
	})
	require.NoError(t, err)
	cTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo C", Container: "bare", Status: "W",
		TaskName: strPtr("C"), Dependency: []int32{a.TaskId, b.TaskId},
	})
	require.NoError(t, err)

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	var depCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task_dependencies
		   WHERE dependent_task_id = $1
		      OR prerequisite_task_id = $1`,
		a.TaskId,
	).Scan(&depCount))
	require.Equal(t, 2, depCount, "pre-delete: A should appear in 2 edges (A→B, A→C)")

	_, err = qc.DeleteTask(ctx, &pb.TaskId{TaskId: a.TaskId})
	require.NoError(t, err)

	// A row gone.
	var aCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task WHERE task_id = $1`, a.TaskId).Scan(&aCount))
	require.Equal(t, 0, aCount, "task A row should be gone")

	// All A-touching dep edges gone.
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task_dependencies
		   WHERE dependent_task_id = $1
		      OR prerequisite_task_id = $1`,
		a.TaskId,
	).Scan(&depCount))
	require.Equal(t, 0, depCount, "post-delete: no edges should reference A")

	// B and C rows survive — scope is "the task and its dep edges,
	// nothing else". The dangling B→C edge stays (it's not connected
	// to A) and B, C are still in the DB.
	var bCount, cCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task WHERE task_id IN ($1, $2)`,
		b.TaskId, cTask.TaskId,
	).Scan(&bCount))
	require.Equal(t, 2, bCount, "B and C should still exist")
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task_dependencies
		   WHERE dependent_task_id = $1 AND prerequisite_task_id = $2`,
		cTask.TaskId, b.TaskId,
	).Scan(&cCount))
	require.Equal(t, 1, cCount, "B→C edge (unrelated to A) should survive")
}

// TestDeleteTask_UnblocksWaitingDependent: when the operator deletes a
// failed prerequisite (the common emergency-cleanup move), any
// downstream task that was sitting in W *purely* because of that
// prereq should be promoted to P automatically.
//
// Pre-fix: DeleteTask did the DELETE (cascade dropped the edge in
// task_dependencies) but never called triggerAssign, so
// promoteWaitingTasks didn't re-evaluate W tasks. The compile task in
// workflow 2846 sat in W forever after the operator deleted 3 failed
// hermes prereqs whose remaining (succeeded) siblings should have
// been enough to release it.
func TestDeleteTask_UnblocksWaitingDependent(t *testing.T) {
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

	// Two prereqs A1, A2 (mimicking the multi-prereq fan-in shape).
	a1, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo A1", Container: "bare", Status: "P", TaskName: strPtr("A1"),
	})
	require.NoError(t, err)
	a2, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo A2", Container: "bare", Status: "P", TaskName: strPtr("A2"),
	})
	require.NoError(t, err)
	// B depends on both. Stays in W.
	b, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "echo B", Container: "bare", Status: "W",
		TaskName: strPtr("B"), Dependency: []int32{a1.TaskId, a2.TaskId},
	})
	require.NoError(t, err)

	// Mark A1 as succeeded directly. B should still be in W (waiting on A2).
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: a1.TaskId, NewStatus: "S"})
	require.NoError(t, err)
	// Force A2 into F to mimic a real failure that the operator
	// then cleans up by deleting it.
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: a2.TaskId, NewStatus: "F"})
	require.NoError(t, err)
	require.Equal(t, "W", getTask(t, ctx, qc, b.TaskId).Status,
		"B should still be in W after A1 succeeds and A2 fails")

	// THE OPERATOR ACTION: delete the failed prereq A2.
	// Cascade removes the A2→B edge. B's remaining edge (A1) is satisfied (S).
	// Promotion to P should fire automatically — this is the fix being locked in.
	_, err = qc.DeleteTask(ctx, &pb.TaskId{TaskId: a2.TaskId})
	require.NoError(t, err)

	// B must promote within a couple of assignment ticks. 5s is comfortable
	// (the loop fires immediately via triggerAssign — see DeleteTask's
	// final call — and a single cycle should be well under a second).
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, b.TaskId).Status == "P"
	}, 5*time.Second, 100*time.Millisecond,
		"B must promote W→P after its last failed prereq is deleted")

	// Sanity: dependency edge is gone too (defence-in-depth — the CASCADE
	// is the schema's job, but if a future schema change drops it this test
	// catches the regression alongside).
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var depCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM task_dependencies
		   WHERE dependent_task_id = $1 AND prerequisite_task_id = $2`,
		b.TaskId, a2.TaskId,
	).Scan(&depCount))
	require.Equal(t, 0, depCount, "deleted-prereq edge must be cascaded away")
}

// TestDeleteTask_ActiveSignalsWorkerThenDeletes: when the target task
// is running on a worker, DeleteTask queues SIGKILL on the worker
// before removing the row. We can't directly observe the signal being
// delivered from a gRPC test (signal column is cleared on worker
// ping), but we can verify the observable contract:
//   - the call eventually succeeds (the wait-for-ping path returns)
//   - the task row is gone afterwards
//   - the worker is still alive (signal didn't kill the worker, just
//     the task's container) — proved by the worker still serving the
//     next submitted task.
func TestDeleteTask_ActiveSignalsWorkerThenDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
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

	cleanupClient, _ := startClientForTest(t, serverAddr, "delete-task-worker", workerToken, 1)
	defer cleanupClient()

	// Long-running task so the kill path actually has to fire. 60s
	// gives enough headroom for slow CI without the test itself
	// having to wait that long if delete works correctly.
	shell := "sh"
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "sleep 60",
		Shell:     &shell,
		Container: "bare",
		Status:    "P",
		TaskName:  strPtr("long-runner"),
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	// Wait for the worker to pick it up and start running.
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskID).Status == "R"
	}, 30*time.Second, 250*time.Millisecond, "task should reach R")

	// Delete while running — server queues SIGKILL, waits for worker
	// ping-ack (up to 15s), then deletes the row. The whole RPC
	// completes within that window.
	deleteStart := time.Now()
	_, err = qc.DeleteTask(ctx, &pb.TaskId{TaskId: taskID})
	require.NoError(t, err, "delete of an active task should succeed within the 15s ack window")
	elapsed := time.Since(deleteStart)
	require.Less(t, elapsed, 20*time.Second,
		"delete should not have to spin past its own 15s deadline")

	// Row gone via ListTasks.
	lt, err := qc.ListTasks(ctx, &pb.ListTasksRequest{})
	require.NoError(t, err)
	for _, tk := range lt.Tasks {
		require.NotEqual(t, taskID, tk.TaskId, "deleted task should not be in ListTasks")
	}

	// Sanity: the worker is still functional — submit a new task and
	// confirm it runs. If we'd killed the worker process instead of
	// just the container, this would hang.
	sub2, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo still-here",
		Shell:     &shell,
		Container: "bare",
		Status:    "P",
		TaskName:  strPtr("post-delete-canary"),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, sub2.TaskId).Status == "S"
	}, 30*time.Second, 250*time.Millisecond,
		"worker should still be alive and pick up a new task after the previous one's delete")
}
