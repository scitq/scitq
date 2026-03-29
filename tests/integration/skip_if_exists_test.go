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
	"google.golang.org/protobuf/proto"
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

// TestSkipIfExistsWithGlob verifies that skip_if_exists checks a glob pattern
// in the output path: only skip if matching files exist.
func TestSkipIfExistsWithGlob(t *testing.T) {
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

	// Dir with a .txt file but no .tgz file
	outputDir := filepath.Join(t.TempDir(), "glob_output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "partial.txt"), []byte("partial"), 0644))

	// Task 1: glob matches *.tgz — no match, should NOT be skipped
	outputGlobNoMatch := outputDir + "/*.tgz"
	taskNoMatch, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:      "echo should run",
		Container:    "alpine",
		Status:       "P",
		Output:       &outputGlobNoMatch,
		SkipIfExists: true,
	})
	require.NoError(t, err)

	// Task 2: glob matches *.txt — has match, should be skipped
	outputGlobMatch := outputDir + "/*.txt"
	taskMatch, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:      "echo should be skipped",
		Container:    "alpine",
		Status:       "P",
		Output:       &outputGlobMatch,
		SkipIfExists: true,
	})
	require.NoError(t, err)

	// Task with glob match should be skipped to S
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, taskMatch.TaskId).Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "task with matching glob should be skipped")

	// Task with no glob match should stay P (not skipped)
	time.Sleep(3 * time.Second) // let a few assign cycles pass
	task := getTask(t, ctx, qc, taskNoMatch.TaskId)
	require.Equal(t, "P", task.Status, "task with non-matching glob should stay pending")

	t.Logf("Glob *.txt matched (task %d skipped), *.tgz did not (task %d stays P)",
		taskMatch.TaskId, taskNoMatch.TaskId)
}

// TestSkipIfExistsUsesPublishPath verifies that skip_if_exists checks the
// publish path (not the workspace output) when publish is set.
func TestSkipIfExistsUsesPublishPath(t *testing.T) {
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

	// Workspace output: empty (no files)
	workspaceDir := filepath.Join(t.TempDir(), "workspace_output")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))

	// Publish path: has files (previous successful run)
	publishDir := filepath.Join(t.TempDir(), "publish_output")
	require.NoError(t, os.MkdirAll(publishDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(publishDir, "result.tgz"), []byte("published"), 0644))

	// Task with both output (workspace) and publish — skip check should use publish
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:      "echo should be skipped",
		Container:    "alpine",
		Status:       "P",
		Output:       &workspaceDir,
		Publish:      proto.String(publishDir),
		SkipIfExists: true,
	})
	require.NoError(t, err)

	// Should be skipped because publish path has files
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task.TaskId).Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "task should be skipped based on publish path")

	t.Logf("Task %d skipped (publish path had files, workspace was empty)", task.TaskId)
}

// TestPublishOnlyOnSuccess verifies that when a task has a publish path,
// failed tasks upload to workspace (output), not to publish.
func TestPublishOnlyOnSuccess(t *testing.T) {
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

	cleanupClient, _ := startClientForTest(t, serverAddr, "publish-worker", workerToken, 1)
	defer cleanupClient()

	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	publishDir := filepath.Join(t.TempDir(), "publish")
	require.NoError(t, os.MkdirAll(workspaceDir, 0755))
	require.NoError(t, os.MkdirAll(publishDir, 0755))

	shell := "sh"

	// Task that succeeds — output should go to publish path
	successTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo success > /output/result.txt",
		Container: "alpine",
		Shell:     &shell,
		Status:    "P",
		Output:    &workspaceDir,
		Publish:   proto.String(publishDir + "/success/"),
	})
	require.NoError(t, err)

	// Task that fails — output should go to workspace, NOT publish
	failPublishDir := filepath.Join(publishDir, "fail")
	require.NoError(t, os.MkdirAll(failPublishDir, 0755))
	failTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo partial > /output/partial.txt && exit 1",
		Container: "alpine",
		Shell:     &shell,
		Status:    "P",
		Output:    &workspaceDir,
		Publish:   proto.String(failPublishDir + "/"),
	})
	require.NoError(t, err)

	// Wait for both tasks to finish
	require.Eventually(t, func() bool {
		s := getTask(t, ctx, qc, successTask.TaskId).Status
		return s == "S"
	}, 60*time.Second, 500*time.Millisecond, "success task should complete")

	require.Eventually(t, func() bool {
		s := getTask(t, ctx, qc, failTask.TaskId).Status
		return s == "F"
	}, 60*time.Second, 500*time.Millisecond, "fail task should fail")

	// Check: publish dir for success should have files
	successFiles, _ := filepath.Glob(filepath.Join(publishDir, "success", "*.txt"))
	require.NotEmpty(t, successFiles, "success task should publish output")

	// Check: publish dir for failure should be empty
	failFiles, _ := filepath.Glob(filepath.Join(failPublishDir, "*.txt"))
	require.Empty(t, failFiles, "failed task should NOT publish output")

	t.Logf("Success task published to %s, failed task did not publish to %s",
		publishDir+"/success/", failPublishDir)
}

// TestForceRunTask verifies that ForceRunTask transitions a W task to P,
// bypassing dependency checks.
func TestForceRunTask(t *testing.T) {
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

	// Create a prereq task but leave it pending (never completes)
	prereq, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo prereq",
		Container: "alpine",
		Status:    "P",
	})
	require.NoError(t, err)

	// Create a dependent task — starts as W because prereq isn't done
	dependent, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:    "echo dependent",
		Container:  "alpine",
		Status:     "P",
		Dependency: []int32{prereq.TaskId},
	})
	require.NoError(t, err)

	// Verify it's in W status
	task := getTask(t, ctx, qc, dependent.TaskId)
	require.Equal(t, "W", task.Status, "dependent task should start as W")

	// Force-run it
	_, err = qc.ForceRunTask(ctx, &pb.ForceRunTaskRequest{TaskId: dependent.TaskId})
	require.NoError(t, err)

	// Should now be P
	task = getTask(t, ctx, qc, dependent.TaskId)
	require.Equal(t, "P", task.Status, "task should be P after force-run")

	t.Logf("Task %d force-run from W to P (prereq %d still pending)", dependent.TaskId, prereq.TaskId)
}
