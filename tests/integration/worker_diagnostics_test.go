package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestWorkerFailureDiagnostics covers two diagnostics features:
//   - a retried task's hidden parent RETAINS its worker_id (so "which worker
//     ran/failed this?" stays answerable after a retry), and
//   - ListWorkers reports recent_failures (failures since the worker's last
//     success), the signal the dashboard surfaces as a warning.
func TestWorkerFailureDiagnostics(t *testing.T) {
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

	wfName := fmt.Sprintf("worker-diag-%d", time.Now().UnixNano())
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "diag-step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	cleanupClient, _ := startClientForTest(t, serverAddr, "diag-worker", workerToken, 1)
	defer cleanupClient()
	var workerID int32
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil || len(workers.Workers) == 0 {
			return false
		}
		workerID = workers.Workers[0].WorkerId
		return true
	}, 15*time.Second, 500*time.Millisecond, "worker should register")
	_, err = qc.UserUpdateWorker(ctx, &pb.WorkerUpdateRequest{WorkerId: workerID, StepId: &stepID})
	require.NoError(t, err)

	// A task that always fails, with one retry so it auto-retries (producing a
	// hidden parent) before terminally failing.
	shell := "sh"
	_, err = qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: "exit 1", Shell: &shell, Container: "bare",
		Status: "P", StepId: &stepID, Retry: int32Ptr(1),
	})
	require.NoError(t, err)
	_, err = qc.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{WorkflowId: wfID, Status: "R"})
	require.NoError(t, err)

	// The worker should accumulate failures since it never succeeds. Wait for
	// the recent_failures signal to register.
	require.Eventually(t, func() bool {
		workers, err := qc.ListWorkers(ctx, &pb.ListWorkersRequest{})
		if err != nil {
			return false
		}
		for _, w := range workers.Workers {
			if w.WorkerId == workerID && w.RecentFailures != nil && *w.RecentFailures >= 1 {
				return true
			}
		}
		return false
	}, 45*time.Second, 1*time.Second, "ListWorkers should report recent_failures >= 1 for a worker that keeps failing")

	// The hidden retry-parent must retain its worker_id (the whole point: the
	// failed attempt stays attributable to the worker that ran it).
	all, err := qc.ListTasks(ctx, &pb.ListTasksRequest{WorkflowIdFilter: &wfID, ShowHidden: boolPtr(true)})
	require.NoError(t, err)
	var hiddenWithWorker int
	for _, tk := range all.Tasks {
		if tk.Hidden && tk.WorkerId != nil && *tk.WorkerId == workerID {
			hiddenWithWorker++
		}
	}
	require.GreaterOrEqual(t, hiddenWithWorker, 1,
		"a hidden retry-parent must retain its worker_id (worker info preserved across retry)")
}
