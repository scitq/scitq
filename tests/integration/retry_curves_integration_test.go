package integration_test

import (
	"context"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestRetryEscalatesAlongCurve verifies the end-to-end retry-with-extra-resource
// pipeline: a task with cpu_curve=[1,2,4] submitted at attempt 0 gets 1 CPU on
// the first attempt, 2 CPUs on the first retry, and 4 CPUs on the second retry.
// This exercises the CASE ... COALESCE(cpu_curve[LEAST(retry_count+2, len)], ...)
// SQL in retryTaskInternal (server.go), which is the only place the curve
// actually shifts.
//
// If this test ever starts failing, likely culprits: the retry SQL forgot to
// copy the curve to the child row, the server-side clamp lost track of
// retry_count, or someone bypassed the curve for a non-eviction failure.
func TestRetryEscalatesAlongCurve(t *testing.T) {
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

	curve := []float32{1, 2, 4}
	minCpu := float32(1)

	// Submit the parent task with retry=2 (so we get 3 attempts total) and a
	// three-point CPU escalation curve.
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "false",
		Container: "bare",
		Retry:     int32Ptr(2),
		TaskName:  strPtr("curve-demo"),
		Status:    "P",
		MinCpu:    &minCpu,
		CpuCurve:  curve,
	})
	require.NoError(t, err)
	parentID := sub.TaskId

	// Helper: fail the given task and wait for its retry clone. Returns the
	// clone. Marks the previous task hidden along the way.
	failAndWaitForClone := func(taskID int32, expectedRetryCount int32) *pb.Task {
		_, err := qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    taskID,
			NewStatus: "F",
		})
		require.NoError(t, err)

		var clone *pb.Task
		require.Eventually(t, func() bool {
			lt, err := qc.ListTasks(ctx, &pb.ListTasksRequest{ShowHidden: boolPtr(true)})
			require.NoError(t, err)
			for _, tk := range lt.Tasks {
				if tk.PreviousTaskId != nil && *tk.PreviousTaskId == taskID {
					clone = tk
					return true
				}
			}
			return false
		}, 3*time.Second, 100*time.Millisecond,
			"retry clone for task %d (attempt %d) never appeared", taskID, expectedRetryCount)

		require.Equal(t, expectedRetryCount, clone.RetryCount)
		return clone
	}

	// First retry: attempt 1 → curve index 1 → 2 CPUs.
	firstRetry := failAndWaitForClone(parentID, 1)
	require.NotNil(t, firstRetry.MinCpu, "first retry should carry a min_cpu shifted along the curve")
	require.InDelta(t, 2.0, *firstRetry.MinCpu, 0.001,
		"first retry: expected min_cpu=2 (curve[1]), got %v", firstRetry.MinCpu)
	require.Equal(t, curve, firstRetry.CpuCurve,
		"curve must travel verbatim to the retry clone (retry logic re-reads it there)")

	// Second retry: attempt 2 → curve index 2 → 4 CPUs.
	secondRetry := failAndWaitForClone(firstRetry.TaskId, 2)
	require.NotNil(t, secondRetry.MinCpu)
	require.InDelta(t, 4.0, *secondRetry.MinCpu, 0.001,
		"second retry: expected min_cpu=4 (curve[2]), got %v", secondRetry.MinCpu)

	// Beyond curve length: with retry=2 the second retry has retry_count=2 and
	// no further retries. We don't fail it — nothing to assert past the curve
	// tail without changing the retry budget, and the DSL-level test
	// (test_task_spec_curves.py) already covers the clamp behaviour.
}
