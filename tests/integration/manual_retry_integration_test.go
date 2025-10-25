package integration_test

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestRetryCLI verifies that `scitq task retry` successfully creates a clone of a failed task.
func TestRetryCLI(t *testing.T) {
	t.Parallel()

	// 1) Boot server
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	// 2) Login via CLI
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)
	require.NotEmpty(t, token)
	c.Attr.Token = token

	// 3) Create a failing task with retry=1
	out, err = runCLICommand(c, []string{
		"task", "create",
		"--container", "alpine",
		"--command", "false", // immediate failure
	})
	require.NoError(t, err)
	require.Contains(t, out, "Task created")
	parentID := extractTaskID(out)
	require.NotZero(t, parentID)

	// 4) Simulate worker reporting failure via gRPC
	ctx := context.Background()
	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    parentID,
		NewStatus: "F",
	})
	require.NoError(t, err)

	// 5) Call CLI retry
	out, err = runCLICommand(c, []string{
		"task", "retry",
		"--id", itoa(parentID),
	})
	require.NoError(t, err)
	require.Contains(t, out, "Retried task")

	newID := extractNewTaskID(out)
	require.NotEqual(t, parentID, newID)

	// 6) Wait a moment and list visible tasks
	require.Eventually(t, func() bool {
		out, _ := runCLICommand(c, []string{"task", "list", "--show-hidden"})
		t.Logf("Current task list output:\n%s", out)

		reNew := regexp.MustCompile(fmt.Sprintf(`ID:\s*%d.*Status:\s*P.*Hidden:\s*false`, newID))
		reOld := regexp.MustCompile(fmt.Sprintf(`ID:\s*%d.*Status:\s*F.*Hidden:\s*true`, parentID))

		return reNew.MatchString(out) && reOld.MatchString(out)
	}, 3*time.Second, 100*time.Millisecond, "new retried task not visible yet")

}

// helper: convert int → string
func itoa(i int32) string { return strconv.FormatInt(int64(i), 10) }

// helper: parse "Created task <id>" or "Retried task <id> → new task ID <id>"
func extractTaskID(out string) int32 {
	// Example: "Created task 12"
	re := regexp.MustCompile(`(\d+)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(out))
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.Atoi(m[1])
	return int32(v)
}

func extractNewTaskID(out string) int32 {
	// Example: "Retried task 12 → new task ID 13"
	re := regexp.MustCompile(`new task ID (\d+)`)
	m := re.FindStringSubmatch(strings.TrimSpace(out))
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.Atoi(m[1])
	return int32(v)
}
