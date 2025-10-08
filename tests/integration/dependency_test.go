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

func TestTaskDependencyUnlocksNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 1) Boot infra
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	// 2) Login
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)
	c.Attr.Token = token

	// 3) gRPC client
	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client
	mdCtx := ctx

	// 4) Submit first task (A)
	a, err := qc.SubmitTask(mdCtx, &pb.TaskRequest{
		Command:   "echo A",
		Container: "alpine",
		TaskName:  strPtr("taskA"),
		Status:    "P",
	})
	require.NoError(t, err)
	aID := a.TaskId

	// 5) Submit dependent task (B depends on A)
	b, err := qc.SubmitTask(mdCtx, &pb.TaskRequest{
		Command:    "echo B",
		Container:  "alpine",
		TaskName:   strPtr("taskB"),
		Status:     "W", // waiting due to dependency
		Dependency: []int32{aID},
	})
	require.NoError(t, err)
	bID := b.TaskId

	// 6) Check B is not yet pending
	lt, err := qc.ListTasks(mdCtx, &pb.ListTasksRequest{})
	require.NoError(t, err)
	var ta, tb *pb.Task
	for _, tk := range lt.Tasks {
		if tk.TaskId == aID {
			ta = tk
		}
		if tk.TaskId == bID {
			tb = tk
		}
	}
	require.NotNil(t, ta)
	require.NotNil(t, tb)
	require.Equal(t, "P", ta.Status)
	require.Equal(t, "W", tb.Status)

	// 7) Simulate A success
	_, err = qc.UpdateTaskStatus(mdCtx, &pb.TaskStatusUpdate{
		TaskId:    aID,
		NewStatus: "S",
	})
	require.NoError(t, err)

	// 8) Eventually B should become pending
	require.Eventually(t, func() bool {
		lt2, err := qc.ListTasks(mdCtx, &pb.ListTasksRequest{})
		require.NoError(t, err)
		for _, tk := range lt2.Tasks {
			if tk.TaskId == bID {
				return tk.Status == "P"
			}
		}
		return false
	}, 3*time.Second, 100*time.Millisecond, "dependent task did not unlock")
}
