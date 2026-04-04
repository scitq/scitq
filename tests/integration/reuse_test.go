package integration_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// computeReuseKey mirrors the Python DSL's reuse key computation for testing.
func computeReuseKey(command, shell, container string, resources []string, inputIdentities []string) string {
	// Task fingerprint
	fpParts := []string{
		"command:" + command,
		"shell:" + shell,
		"container:" + container,
		"container_options:",
	}
	sortedRes := make([]string, len(resources))
	copy(sortedRes, resources)
	// resources should be pre-sorted by caller
	for _, r := range sortedRes {
		fpParts = append(fpParts, "resource:"+r)
	}
	fpHash := sha256.Sum256([]byte(strings.Join(fpParts, "\n")))
	fingerprint := fmt.Sprintf("%x", fpHash)

	// Reuse key = hash(fingerprint + sorted input identities)
	keyParts := []string{fingerprint}
	sortedIds := make([]string, len(inputIdentities))
	copy(sortedIds, inputIdentities)
	// input identities should be pre-sorted by caller
	keyParts = append(keyParts, sortedIds...)
	keyHash := sha256.Sum256([]byte(strings.Join(keyParts, "\n")))
	return fmt.Sprintf("%x", keyHash)
}

// TestReuseHitSkipsTask verifies that a task with a reuse_key matching an existing
// task_reuse entry is immediately promoted to S with the cached output path.
func TestReuseHitSkipsTask(t *testing.T) {
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

	// Start a worker so the first task can actually run
	cleanupClient, store := startClientForTest(t, serverAddr, "reuse-worker", workerToken, 1)
	defer cleanupClient()
	_ = store

	// Compute a reuse key for "echo hello" with external input "file:///data/sample1.fq"
	reuseKey := computeReuseKey("echo hello", "", "alpine", nil, []string{"file:///data/sample1.fq"})

	// --- Run 1: Submit task with reuse_key, no entry in task_reuse yet → cache miss, runs normally ---
	outputDir1 := t.TempDir() + "/run1_output/"
	task1, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo hello",
		Container: "alpine",
		Status:    "P",
		Output:    &outputDir1,
		ReuseKey:  strPtr(reuseKey),
	})
	require.NoError(t, err)
	t.Logf("Run 1: task %d submitted with reuse_key=%s", task1.TaskId, reuseKey[:12])

	// Wait for task to succeed (actually runs on the worker)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task1.TaskId).Status == "S"
	}, 30*time.Second, 500*time.Millisecond, "first task should succeed")
	t.Logf("Run 1: task %d completed successfully", task1.TaskId)

	// --- Run 2: Submit a second task with the same reuse_key → should be a reuse hit ---
	outputDir2 := t.TempDir() + "/run2_output/"
	task2, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo hello",
		Container: "alpine",
		Status:    "P",
		Output:    &outputDir2,
		ReuseKey:  strPtr(reuseKey),
	})
	require.NoError(t, err)
	t.Logf("Run 2: task %d submitted with same reuse_key=%s", task2.TaskId, reuseKey[:12])

	// Task should be skipped to S via reuse hit (no worker needed)
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task2.TaskId).Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "second task should be reuse-hit to S")

	// Verify the output was rewritten to the cached path (from task 1)
	task2Final := getTask(t, ctx, qc, task2.TaskId)
	require.NotNil(t, task2Final.Output)
	require.Equal(t, outputDir1, *task2Final.Output, "reuse hit should rewrite output to cached path")

	t.Logf("Run 2: task %d was reuse-hit, output rewritten to %s", task2.TaskId, *task2Final.Output)
}

// TestReusePromotesDependents verifies that a reuse hit on a prerequisite task
// correctly promotes dependent tasks from W to P.
func TestReusePromotesDependents(t *testing.T) {
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

	// Start a worker
	cleanupClient, _ := startClientForTest(t, serverAddr, "reuse-dep-worker", workerToken, 1)
	defer cleanupClient()

	reuseKey := computeReuseKey("echo prep", "", "alpine", nil, []string{"file:///ref/db.fa"})

	// Run 1: prep task runs normally, populates task_reuse
	outputDir := t.TempDir() + "/prep_output/"
	prepTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo prep",
		Container: "alpine",
		Status:    "P",
		Output:    &outputDir,
		ReuseKey:  strPtr(reuseKey),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, prepTask.TaskId).Status == "S"
	}, 30*time.Second, 500*time.Millisecond, "prep task should succeed")

	// Run 2: submit prep task again with same reuse_key, plus a dependent task
	outputDir2 := t.TempDir() + "/prep2_output/"
	prepTask2, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo prep",
		Container: "alpine",
		Status:    "P",
		Output:    &outputDir2,
		ReuseKey:  strPtr(reuseKey),
	})
	require.NoError(t, err)

	// Dependent task: starts as W, depends on prep task 2
	depTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:    "echo downstream",
		Container:  "alpine",
		Status:     "P",
		Dependency: []int32{prepTask2.TaskId},
	})
	require.NoError(t, err)
	t.Logf("Run 2: prep=%d (reuse_key=%s), dependent=%d", prepTask2.TaskId, reuseKey[:12], depTask.TaskId)

	// Prep task should be reuse-hit to S
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, prepTask2.TaskId).Status == "S"
	}, 15*time.Second, 200*time.Millisecond, "prep task should be reuse-hit to S")

	// Dependent task should be promoted from W to P
	require.Eventually(t, func() bool {
		st := getTask(t, ctx, qc, depTask.TaskId).Status
		return st == "P" || st == "A" || st == "C" || st == "D" || st == "O" || st == "R" || st == "S"
	}, 15*time.Second, 200*time.Millisecond, "dependent task should be promoted after reuse hit")

	t.Logf("Dependent task %d promoted after reuse hit on prep task %d", depTask.TaskId, prepTask2.TaskId)
}

// TestReuseMissRunsNormally verifies that a task with a reuse_key that has no match
// in task_reuse runs normally (cache miss).
func TestReuseMissRunsNormally(t *testing.T) {
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

	cleanupClient, _ := startClientForTest(t, serverAddr, "reuse-miss-worker", workerToken, 1)
	defer cleanupClient()

	// Unique reuse key that has no match
	reuseKey := computeReuseKey("echo unique_"+t.Name(), "", "alpine", nil, []string{"file:///data/unique.fq"})

	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo unique_" + t.Name(),
		Container: "alpine",
		Status:    "P",
		ReuseKey:  strPtr(reuseKey),
	})
	require.NoError(t, err)

	// Should run normally and succeed
	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task.TaskId).Status == "S"
	}, 30*time.Second, 500*time.Millisecond, "task with cache miss should run and succeed")

	t.Logf("Task %d ran normally (reuse miss) and succeeded", task.TaskId)
}

// TestNoReuseKeyRunsNormally verifies that tasks without a reuse_key are unaffected.
func TestNoReuseKeyRunsNormally(t *testing.T) {
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

	cleanupClient, _ := startClientForTest(t, serverAddr, "reuse-none-worker", workerToken, 1)
	defer cleanupClient()

	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo normal",
		Container: "alpine",
		Status:    "P",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return getTask(t, ctx, qc, task.TaskId).Status == "S"
	}, 30*time.Second, 500*time.Millisecond, "task without reuse_key should run normally")

	t.Logf("Task %d ran normally without reuse_key", task.TaskId)
}
