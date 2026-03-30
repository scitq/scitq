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

// TestEditAndRetryTask verifies that editing a task's command creates a new
// retry clone with the modified command while hiding the original.
func TestEditAndRetryTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Start a worker so the edited task can actually run
	cleanupClient, _ := startClientForTest(t, serverAddr, "edit-worker", workerToken, 1)
	defer cleanupClient()

	// Submit a task that will fail (bad command)
	shell := "sh"
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "this_command_does_not_exist",
		Shell:     &shell,
		Container: "alpine",
		Status:    "P",
		TaskName:  strPtr("edit-test"),
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	// Wait for it to fail
	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, taskID)
		return task.Status == "F"
	}, 60*time.Second, 500*time.Millisecond, "task should fail")

	// Edit and retry with a valid command
	newCmd := "echo edited-success"
	editResp, err := qc.EditAndRetryTask(ctx, &pb.EditAndRetryTaskRequest{
		TaskId:  taskID,
		Command: newCmd,
	})
	require.NoError(t, err)
	newTaskID := editResp.TaskId
	require.NotEqual(t, taskID, newTaskID, "should create a new task")

	t.Logf("Task %d edited → new task %d", taskID, newTaskID)

	// New task should have the edited command and eventually succeed
	require.Eventually(t, func() bool {
		task := getTask(t, ctx, qc, newTaskID)
		return task.Status == "S"
	}, 60*time.Second, 500*time.Millisecond, "edited task should succeed")

	newTask := getTask(t, ctx, qc, newTaskID)
	require.Contains(t, newTask.Command, "echo edited-success", "new task should have the edited command")

	// Original task should be hidden
	showHidden := true
	allTasks, err := qc.ListTasks(ctx, &pb.ListTasksRequest{ShowHidden: &showHidden})
	require.NoError(t, err)
	for _, task := range allTasks.Tasks {
		if task.TaskId == taskID {
			require.True(t, task.Hidden, "original task should be hidden")
		}
	}

	t.Logf("Original task %d hidden, new task %d succeeded with edited command", taskID, newTaskID)
}

// TestEditStepCommand verifies that find/replace on all failed tasks in a step
// creates new retry clones with modified commands.
func TestEditStepCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Create a step
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{Name: "edit-step-test"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Submit 3 failed tasks in the step with a common bad path
	shell := "sh"
	var taskIDs []int32
	for i := 0; i < 3; i++ {
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:   "cat /wrong/path/data.txt",
			Shell:     &shell,
			Container: "alpine",
			Status:    "P",
			StepId:    &stepID,
			TaskName:  strPtr("step-edit-task"),
		})
		require.NoError(t, err)
		taskIDs = append(taskIDs, sub.TaskId)
	}

	// Manually mark them as failed (simulate worker reporting failure)
	for _, id := range taskIDs {
		_, err := qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    id,
			NewStatus: "F",
		})
		require.NoError(t, err)
	}

	// Verify they're all failed
	for _, id := range taskIDs {
		task := getTask(t, ctx, qc, id)
		require.Equal(t, "F", task.Status, "task %d should be failed", id)
	}

	// Edit step: find/replace the bad path
	editResp, err := qc.EditStepCommand(ctx, &pb.EditStepCommandRequest{
		StepId:  stepID,
		Find:    "/wrong/path",
		Replace: "/correct/path",
	})
	require.NoError(t, err)
	require.Equal(t, int32(3), editResp.EditedCount, "should edit all 3 tasks")
	require.Len(t, editResp.NewTaskIds, 3, "should create 3 new tasks")

	t.Logf("Step %d: 3 tasks edited → new IDs %v", stepID, editResp.NewTaskIds)

	// Verify new tasks have the corrected command
	for _, newID := range editResp.NewTaskIds {
		newTask := getTask(t, ctx, qc, newID)
		require.Contains(t, newTask.Command, "/correct/path", "new task should have corrected path")
		require.NotContains(t, newTask.Command, "/wrong/path", "new task should not have old path")
	}

	// Verify original tasks are hidden
	showHidden := true
	allTasks, err := qc.ListTasks(ctx, &pb.ListTasksRequest{ShowHidden: &showHidden, StepIdFilter: &stepID})
	require.NoError(t, err)
	for _, task := range allTasks.Tasks {
		for _, origID := range taskIDs {
			if task.TaskId == origID {
				require.True(t, task.Hidden, "original task %d should be hidden", origID)
			}
		}
	}
}

// TestEditStepCommandRegexp verifies regexp find/replace on step commands.
func TestEditStepCommandRegexp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := strings.TrimSpace(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Create step and a failed task with a versioned command
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{Name: "regexp-edit-test"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	shell := "sh"
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "bowtie2 -k 100 -x /ref/index_v1.2 -U input.fq",
		Shell:     &shell,
		Container: "alpine",
		Status:    "P",
		StepId:    &stepID,
	})
	require.NoError(t, err)

	// Mark as failed
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    sub.TaskId,
		NewStatus: "F",
	})
	require.NoError(t, err)

	// Regexp replace: change -k <number> to -k 200
	editResp, err := qc.EditStepCommand(ctx, &pb.EditStepCommandRequest{
		StepId:   stepID,
		Find:     `-k \d+`,
		Replace:  `-k 200`,
		IsRegexp: true,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), editResp.EditedCount)

	newTask := getTask(t, ctx, qc, editResp.NewTaskIds[0])
	require.Contains(t, newTask.Command, "-k 200", "should have regexp-replaced value")
	require.NotContains(t, newTask.Command, "-k 100", "should not have old value")

	t.Logf("Regexp edit: %q → command now contains '-k 200'", `-k \d+`)
}
