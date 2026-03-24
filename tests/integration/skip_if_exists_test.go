package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestSkipIfExistsSkipsTask verifies that a task with skip_if_exists=true
// is immediately marked as S when its output path already contains files.
func TestSkipIfExistsSkipsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Start a real worker so tasks can actually be assigned
	cleanupClient, _ := startClientForTest(t, serverAddr, "skip-worker", workerToken, 1)
	defer cleanupClient()

	// Create a temp directory to use as the output path
	outputDir := filepath.Join(t.TempDir(), "task_output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Pre-populate the output dir with a file (simulating previous run)
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "result.txt"), []byte("existing output"), 0644))

	// Submit a task with skip_if_exists=true and the pre-populated output path
	skipTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:       "echo this should be skipped",
		Container:     "alpine",
		Status:        "P",
		Output:       &outputDir,
		SkipIfExists:  true,
	})
	require.NoError(t, err)
	skipTaskID := skipTask.TaskId

	// The task should be skipped (S) without ever running, within a few assignment cycles
	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, skipTaskID)
		return task.Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "task with existing output should be skipped to S")

	t.Logf("Task %d was skipped (status=S) because output already existed", skipTaskID)
}

// TestSkipIfExistsRunsWhenMissing verifies that a task with skip_if_exists=true
// runs normally when its output path is empty.
func TestSkipIfExistsRunsWhenMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Start a real worker
	cleanupClient, _ := startClientForTest(t, serverAddr, "skip-worker2", workerToken, 1)
	defer cleanupClient()

	// Empty output dir (no pre-existing output)
	outputDir := filepath.Join(t.TempDir(), "empty_output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Submit task with skip_if_exists=true but empty output
	shell := "sh"
	runTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:       fmt.Sprintf("echo done > /output/result.txt"),
		Container:     "alpine",
		Shell:         &shell,
		Status:        "P",
		Output:       &outputDir,
		SkipIfExists:  true,
	})
	require.NoError(t, err)
	runTaskID := runTask.TaskId

	// Task should run normally and eventually succeed
	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, runTaskID)
		return task.Status == "S" || task.Status == "F"
	}, 60*time.Second, 500*time.Millisecond, "task with empty output should run normally")

	task := getTask(t, ctx, qc, runTaskID)
	require.Equal(t, "S", task.Status, "task should succeed")
	t.Logf("Task %d ran normally (output was missing)", runTaskID)
}

// TestSkipIfExistsPromotesDependents verifies that when a task is skipped,
// its dependent tasks (in W state) are promoted to P.
func TestSkipIfExistsPromotesDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Pre-populate output
	outputDir := filepath.Join(t.TempDir(), "prep_output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "db.tgz"), []byte("fake db"), 0644))

	// Task A: preparation, should be skipped
	taskA, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:       "echo building database",
		Container:     "alpine",
		Status:        "P",
		Output:       &outputDir,
		SkipIfExists:  true,
	})
	require.NoError(t, err)

	// Task B: depends on A, starts as W
	taskB, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:    "echo using database",
		Container:  "alpine",
		Status:     "P",
		Dependency: []int32{taskA.TaskId},
	})
	require.NoError(t, err)

	// Task A should be skipped to S
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskA.TaskId).Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "prep task should be skipped")

	// Task B should be promoted from W to P (dependencies resolved)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskB.TaskId).Status == "P"
	}, 15*time.Second, 200*time.Millisecond, "dependent task should be promoted to P after skip")

	t.Logf("Task A (%d) skipped, Task B (%d) promoted to P", taskA.TaskId, taskB.TaskId)
}
