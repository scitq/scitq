package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

func TestRetryClonesHiddenParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1) Boot infra (reuse your helpers from TestIntegration)
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t)
	defer cleanup()

	// 2) Login via CLI to obtain a token (same approach as main integration test)
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)

	// 3) Create an authenticated gRPC client via lib helper
	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client
	mdCtx := ctx

	// 4) Submit a task with retry=1
	sub, err := qc.SubmitTask(mdCtx, &pb.TaskRequest{
		Command:   "false", // will fail immediately
		Container: "alpine",
		Retry:     int32Ptr(1),
		TaskName:  strPtr("retry-demo"),
		Status:    "P",
	})
	require.NoError(t, err)
	parentID := sub.TaskId
	require.NotZero(t, parentID)

	// 5) Simulate failure (what the worker would report)
	_, err = qc.UpdateTaskStatus(mdCtx, &pb.TaskStatusUpdate{
		TaskId:    parentID,
		NewStatus: "F",
	})
	require.NoError(t, err)

	// 6) Poll until the child appears (server clones in a TX; allow a tiny delay)
	var child *pb.Task
	require.Eventually(t, func() bool {
		lt, err := qc.ListTasks(mdCtx, &pb.ListTasksRequest{
			ShowHidden: boolPtr(true),
		})
		require.NoError(t, err)
		var parent *pb.Task
		for _, tk := range lt.Tasks {
			switch tk.TaskId {
			case parentID:
				parent = tk
			default:
				// find a child pointing to parent
				if tk.PreviousTaskId != nil && *tk.PreviousTaskId == parentID {
					child = tk
				}
			}
		}
		if parent == nil {
			return false
		}
		// Parent must be F + hidden
		if !(parent.Status == "F" && parent.Hidden) {
			return false
		}
		// Child must exist
		return child != nil
	}, 3*time.Second, 100*time.Millisecond, "child retry not created / parent not hidden")

	// 7) Validate child fields
	require.Equal(t, "P", child.Status)          // pending retry
	require.Equal(t, int32(1), child.RetryCount) // incremented
	require.NotNil(t, child.PreviousTaskId)
	require.Equal(t, parentID, *child.PreviousTaskId)
	require.Equal(t, "retry-demo", child.GetTaskName())

	// 8) Hidden filter behavior
	ltVis, err := qc.ListTasks(mdCtx, &pb.ListTasksRequest{}) // default: hidden=false
	require.NoError(t, err)
	require.True(t, containsTask(ltVis.Tasks, child.TaskId))
	require.False(t, containsTask(ltVis.Tasks, parentID)) // parent hidden

	ltAll, err := qc.ListTasks(mdCtx, &pb.ListTasksRequest{ShowHidden: boolPtr(true)})
	require.NoError(t, err)
	require.True(t, containsTask(ltAll.Tasks, child.TaskId))
	require.True(t, containsTask(ltAll.Tasks, parentID))
}

func containsTask(ts []*pb.Task, id int32) bool {
	for _, t := range ts {
		if t.TaskId == id {
			return true
		}
	}
	return false
}
func boolPtr(b bool) *bool { return &b }

func int32Ptr(i int32) *int32 { return &i }

func strPtr(s string) *string { return &s }
