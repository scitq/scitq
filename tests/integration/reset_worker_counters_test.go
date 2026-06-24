package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestResetWorkerCounters verifies the operator-acknowledge path
// added for the (3)+(4) feature: a worker accumulates both task
// failures (recent_failures) and W-level worker_events
// (pending_warnings), the operator hits Reset and both drop to 0
// without losing the underlying audit rows.
//
// Also covers the workflow.eligible_worker_count signal added for
// (2): a workflow with a worker attached to one of its steps must
// report eligible_worker_count > 0, otherwise the UI can't
// distinguish "idle" from "stuck because no worker".
func TestResetWorkerCounters(t *testing.T) {
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

	wfName := fmt.Sprintf("reset-test-%d", time.Now().UnixNano())
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// Inject a worker on the step; no real client needed — the RPC
	// reads the DB directly. status='R' so eligible_worker_count
	// picks it up.
	var workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, is_permanent)
		VALUES ($1, $2, 1, 0, 'R', TRUE) RETURNING worker_id
	`, fmt.Sprintf("w-%s", wfName), stepID).Scan(&workerID))

	// --- (2) eligible_worker_count ----------------------------------
	wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &wfName})
	require.NoError(t, err)
	require.Len(t, wfList.Workflows, 1)
	require.EqualValues(t, 1, wfList.Workflows[0].EligibleWorkerCount,
		"workflow with one R worker on its step should report eligible_worker_count=1")

	// --- (3) pending_warnings counts unread W-level events ---------
	_, err = db.Exec(`
		INSERT INTO worker_event (worker_id, level, event_class, message)
		VALUES ($1, 'W', 'fit', 'simulated no-fit'),
		       ($1, 'W', 'fit', 'second warning'),
		       ($1, 'I', 'lifecycle', 'info — should NOT count')
	`, workerID)
	require.NoError(t, err)

	listResp, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{WorkflowId: &wfList.Workflows[0].WorkflowId})
	require.NoError(t, err)
	require.Len(t, listResp.Workers, 1)
	require.NotNil(t, listResp.Workers[0].PendingWarnings)
	require.EqualValues(t, 2, *listResp.Workers[0].PendingWarnings,
		"two unread W-level events should count; the I-level one must not")

	// --- (4) recent_failures fed by F-status tasks since last S ----
	// Submit + force-fail two tasks for this worker.
	for i := 0; i < 2; i++ {
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:   "false",
			Container: "bare",
			Status:    "F",
			StepId:    &stepID,
			TaskName:  strPtr(fmt.Sprintf("fail-%d", i)),
		})
		require.NoError(t, err)
		// Pin the task to our worker so the recent_failures
		// subquery (which filters by worker_id) picks it up.
		_, err = db.Exec(`UPDATE task SET worker_id = $1 WHERE task_id = $2`, workerID, sub.TaskId)
		require.NoError(t, err)
	}
	listResp, err = qc.ListWorkers(ctx, &pb.ListWorkersRequest{WorkflowId: &wfList.Workflows[0].WorkflowId})
	require.NoError(t, err)
	require.NotNil(t, listResp.Workers[0].RecentFailures)
	require.EqualValues(t, 2, *listResp.Workers[0].RecentFailures,
		"two F-tasks pinned to the worker since no S-task should yield recent_failures=2")

	// --- ResetWorkerCounters(both) ---------------------------------
	_, err = qc.ResetWorkerCounters(ctx, &pb.ResetWorkerCountersRequest{
		WorkerId:       workerID,
		ClearFailures:  true,
		ClearWarnings:  true,
	})
	require.NoError(t, err)

	listResp, err = qc.ListWorkers(ctx, &pb.ListWorkersRequest{WorkflowId: &wfList.Workflows[0].WorkflowId})
	require.NoError(t, err)
	require.NotNil(t, listResp.Workers[0].PendingWarnings)
	require.EqualValues(t, 0, *listResp.Workers[0].PendingWarnings, "after clear_warnings, pending_warnings must be 0")
	require.NotNil(t, listResp.Workers[0].RecentFailures)
	require.EqualValues(t, 0, *listResp.Workers[0].RecentFailures, "after clear_failures, recent_failures must be 0")

	// Audit rows must persist (the design: ack, don't delete).
	var weCount, fCount int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM worker_event WHERE worker_id = $1 AND level = 'W'`, workerID).Scan(&weCount))
	require.Equal(t, 2, weCount, "ack must NOT delete the worker_event rows")
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM task WHERE worker_id = $1 AND status = 'F'`, workerID).Scan(&fCount))
	require.Equal(t, 2, fCount, "reset must NOT delete the failed tasks")
}
