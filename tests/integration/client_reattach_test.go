package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// startClientProcess runs the client binary in its own process group, so
// the test can kill it with its `docker run` children as systemd does.
func startClientProcess(t *testing.T, binary, serverAddr, workerToken, name, store string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binary, "-server", serverAddr, "-token", workerToken, "-name", name,
		"-store", store, "-concurrency", "1", "-permanent")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	return cmd
}

// A client killed while its task runs leaves the container running. The
// next client re-attaches to it: the task succeeds under the same id, with
// the output printed before and after the restart, and is not retried.
func TestClientRestartReattachesRunningTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	binary := filepath.Join(t.TempDir(), "scitq-client")
	build := exec.Command("go", "build", "-o", binary, "./cmd/client")
	build.Dir = "../.."
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out2, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out2)
	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	name := fmt.Sprintf("reattach-%d", time.Now().UnixNano())
	store := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.MkdirAll(store, 0o755))

	shell := "sh"
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo before-restart; sleep 20; echo after-restart",
		Container: "alpine",
		Shell:     &shell,
		Status:    "P",
	})
	require.NoError(t, err)
	container := fmt.Sprintf("scitq-%s-task-%d", name, task.TaskId)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", container).Run() })

	first := startClientProcess(t, binary, serverAddr, workerToken, name, store)
	require.Eventually(t, func() bool {
		running, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", container).Output()
		return strings.TrimSpace(string(running)) == "true"
	}, 90*time.Second, time.Second, "the task's container never started")
	time.Sleep(3 * time.Second) // let "before-restart" reach the server

	// Kill the client and its `docker run`; Docker keeps the container.
	require.NoError(t, syscall.Kill(-first.Process.Pid, syscall.SIGKILL))
	_ = first.Wait()
	running, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", container).Output()
	require.Equal(t, "true", strings.TrimSpace(string(running)), "the container should outlive the client")

	startClientProcess(t, binary, serverAddr, workerToken, name, store)

	require.Eventually(t, func() bool {
		got, err := qc.GetTask(ctx, &pb.TaskId{TaskId: task.TaskId})
		return err == nil && got.Status == "S"
	}, 90*time.Second, time.Second, "the re-attached task did not succeed")

	got, err := qc.GetTask(ctx, &pb.TaskId{TaskId: task.TaskId})
	require.NoError(t, err)
	require.False(t, got.Hidden, "the task should not have been retried")
	stdout, err := runCLICommand(c, []string{"task", "stdout", "--id", fmt.Sprint(task.TaskId)})
	require.NoError(t, err)
	require.Contains(t, stdout, "before-restart")
	require.Contains(t, stdout, "after-restart")

	require.Eventually(t, func() bool {
		return exec.Command("docker", "inspect", container).Run() != nil
	}, 30*time.Second, time.Second, "the container should be removed once the task ends")
}
