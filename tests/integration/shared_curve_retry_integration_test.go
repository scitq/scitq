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

// TestRetryShiftsSharedCurve verifies that mem_shared_curve is shifted
// alongside mem_curve on the retry-clone SQL (server.go retryTaskInternal).
// If mem_curve escalates but mem_shared_curve stays flat, the shared value
// on the retry clone must be the same scalar (or the flat curve's element),
// not a shifted mem_curve entry. Symmetrically, if the shared curve grows,
// the retry clone picks up the shifted shared value.
//
// This is the shared-side counterpart of TestRetryEscalatesAlongCurve.
// A regression that skipped copying mem_shared_curve or that shifted with
// the wrong index would surface here.
func TestRetryShiftsSharedCurve(t *testing.T) {
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

	// mem: [5,10,20], mem_shared: [15,15,30]. Attempts:
	//   0 → mem=5,  mem_shared=15
	//   1 → mem=10, mem_shared=15
	//   2 → mem=20, mem_shared=30
	// The retry SQL uses LEAST(retry_count+2, array_length(curve,1)) so
	// index 1 on retry_count=0 hits array position 2 → mem=10, and the
	// shared curve at that same position stays 15 (position 2 is still
	// the second element).
	memCurve := []float32{5, 10, 20}
	memSharedCurve := []float32{15, 15, 30}
	minMem := float32(5)
	minMemShared := float32(15)

	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:        "false",
		Container:      "bare",
		Retry:          int32Ptr(2),
		TaskName:       strPtr("shared-curve-demo"),
		Status:         "P",
		MinMem:         &minMem,
		MemCurve:       memCurve,
		MinMemShared:   &minMemShared,
		MemSharedCurve: memSharedCurve,
	})
	require.NoError(t, err)
	parentID := sub.TaskId

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

	// First retry: mem shifts to curve[1]=10, mem_shared stays at
	// curve[1]=15 (flat at this index).
	firstRetry := failAndWaitForClone(parentID, 1)
	require.NotNil(t, firstRetry.MinMem)
	require.InDelta(t, 10.0, *firstRetry.MinMem, 0.001,
		"first retry: expected min_mem=10 (curve[1]), got %v", firstRetry.MinMem)
	require.NotNil(t, firstRetry.MinMemShared)
	require.InDelta(t, 15.0, *firstRetry.MinMemShared, 0.001,
		"first retry: expected min_mem_shared=15 (shared curve stays flat at index 1), got %v", firstRetry.MinMemShared)
	require.Equal(t, memSharedCurve, firstRetry.MemSharedCurve,
		"shared curve must travel verbatim to the retry clone")

	// Second retry: both bump — mem to curve[2]=20, mem_shared to
	// curve[2]=30. This is the interesting case: the shared value
	// really does escalate, proving the shift index is honoured on
	// the shared column and not just piggy-backed onto min_mem.
	secondRetry := failAndWaitForClone(firstRetry.TaskId, 2)
	require.NotNil(t, secondRetry.MinMem)
	require.InDelta(t, 20.0, *secondRetry.MinMem, 0.001,
		"second retry: expected min_mem=20 (curve[2]), got %v", secondRetry.MinMem)
	require.NotNil(t, secondRetry.MinMemShared)
	require.InDelta(t, 30.0, *secondRetry.MinMemShared, 0.001,
		"second retry: expected min_mem_shared=30 (curve[2]), got %v", secondRetry.MinMemShared)
}
