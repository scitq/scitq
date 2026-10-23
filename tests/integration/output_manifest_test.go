package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

func manifestTestSetup(t *testing.T, workerName string) (pb.TaskQueueClient, func()) {
	t.Helper()
	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	cleanupClient, _ := startClientForTest(t, serverAddr, workerName, workerToken, 1)

	return qclient.Client, func() {
		cleanupClient()
		qclient.Close()
		cleanup()
	}
}

func submitTwoFileTask(t *testing.T, qc pb.TaskQueueClient, output string) int32 {
	t.Helper()
	shell := "sh"
	retry := int32(0)
	task, err := qc.SubmitTask(context.Background(), &pb.TaskRequest{
		Command:   "mkdir -p /output/sub && echo hello > /output/a.txt && echo world > /output/sub/b.txt",
		Container: "alpine",
		Shell:     &shell,
		Status:    "P",
		Output:    &output,
		Retry:     &retry,
	})
	require.NoError(t, err)
	return task.TaskId
}

func waitTerminal(t *testing.T, qc pb.TaskQueueClient, taskID int32) *pb.Task {
	t.Helper()
	ctx := context.Background()
	require.Eventually(t, func() bool {
		st := getTask(t, ctx, qc, taskID).Status
		return st == "S" || st == "F"
	}, 120*time.Second, 500*time.Millisecond, "task %d should reach a terminal status", taskID)
	return getTask(t, ctx, qc, taskID)
}

// TestOutputManifestReportedOnSuccess: a task that writes two files ends in
// S with both files produced and uploaded, and the files are at the target.
func TestOutputManifestReportedOnSuccess(t *testing.T) {
	t.Parallel()
	qc, cleanup := manifestTestSetup(t, "manifest-ok")
	defer cleanup()

	outputDir := filepath.Join(t.TempDir(), "out") + "/"
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	task := waitTerminal(t, qc, submitTwoFileTask(t, qc, outputDir))
	require.Equal(t, "S", task.Status)
	require.NotNil(t, task.ProducedFiles)
	require.NotNil(t, task.UploadedFiles)
	require.NotNil(t, task.ProducedBytes)
	require.EqualValues(t, 2, *task.ProducedFiles)
	require.EqualValues(t, 2, *task.UploadedFiles)
	require.EqualValues(t, 12, *task.ProducedBytes) // "hello\n" + "world\n"
	require.FileExists(t, filepath.Join(outputDir, "sub", "b.txt"))
}

// TestOutputManifestUploadShortfallFails: when the files the program produced
// cannot reach the destination, the task ends in F (class network) and the
// manifest shows the shortfall.
func TestOutputManifestUploadShortfallFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only destination does not stop root")
	}
	t.Parallel()
	qc, cleanup := manifestTestSetup(t, "manifest-short")
	defer cleanup()

	readOnly := filepath.Join(t.TempDir(), "ro")
	require.NoError(t, os.MkdirAll(readOnly, 0o755))
	require.NoError(t, os.Chmod(readOnly, 0o555))
	defer os.Chmod(readOnly, 0o755)

	task := waitTerminal(t, qc, submitTwoFileTask(t, qc, filepath.Join(readOnly, "out")+"/"))
	require.Equal(t, "F", task.Status)
	require.NotNil(t, task.ProducedFiles)
	require.NotNil(t, task.UploadedFiles)
	require.EqualValues(t, 2, *task.ProducedFiles)
	require.Less(t, *task.UploadedFiles, *task.ProducedFiles)
}
