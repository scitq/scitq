package integration_test

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/stretchr/testify/require"
)

func submitContainerTask(t *testing.T, qc pb.TaskQueueClient, command, options string) int32 {
	t.Helper()
	shell, retry := "sh", int32(0)
	req := &pb.TaskRequest{Command: command, Container: "alpine", Shell: &shell, Status: "P", Retry: &retry}
	if options != "" {
		req.ContainerOptions = &options
	}
	task, err := qc.SubmitTask(context.Background(), req)
	require.NoError(t, err)
	return task.TaskId
}

func requireFailureClass(t *testing.T, qc pb.TaskQueueClient, taskID int32, want string) {
	t.Helper()
	task := waitTerminal(t, qc, taskID)
	require.Equal(t, "F", task.Status, "task %d should fail", taskID)
	require.NotNil(t, task.FailureClass, "task %d has no failure_class", taskID)
	require.Equal(t, want, *task.FailureClass, "task %d failure_class", taskID)
}

// TestFailureClassOOM: a container killed by the kernel OOM killer is
// classified oom.
func TestFailureClassOOM(t *testing.T) {
	t.Parallel()
	qc, cleanup := manifestTestSetup(t, "fc-oom")
	defer cleanup()

	// tail keeps its whole (newline-free) input in memory: 300 MB in a
	// 64 MB container.
	id := submitContainerTask(t, qc, "head -c 300m /dev/zero | tail", "--memory=64m --memory-swap=64m")
	requireFailureClass(t, qc, id, "oom")
}

// TestFailureClassExternalKillIsNotOOM: a container killed from outside
// (what a Docker daemon shutdown or an operator does) also exits 137, but is
// not an OOM.
func TestFailureClassExternalKillIsNotOOM(t *testing.T) {
	t.Parallel()
	qc, cleanup := manifestTestSetup(t, "fc-kill")
	defer cleanup()

	id := submitContainerTask(t, qc, "sleep 300", "")
	container := fmt.Sprintf("scitq-fc-kill-task-%d", id)
	require.Eventually(t, func() bool {
		return exec.Command("docker", "inspect", "-f", "{{.State.Running}}", container).Run() == nil &&
			getTask(t, context.Background(), qc, id).Status == "R"
	}, 120*time.Second, 500*time.Millisecond, "task should be running")
	out, err := exec.Command("docker", "kill", container).CombinedOutput()
	require.NoError(t, err, "docker kill: %s", out)

	requireFailureClass(t, qc, id, "other")
}
