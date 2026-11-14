package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// A task whose prerequisite fails for good, through a dependency that does
// not accept failure, fails too ("upstream"). A fan-in that accepts failure
// then runs with what succeeded, and the workflow ends on its result.
// Retrying the failed prerequisite reopens the cascaded task.
func TestUpstreamFailureCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)
	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfID := createWorkflowViaCLI(t, c, "wf-upstream")
	sampleStep := createStepViaCLI(t, c, wfID, "sample")
	profileStep := createStepViaCLI(t, c, wfID, "profile")
	compileStep := createStepViaCLI(t, c, wfID, "compile")
	workerID := registerWorkerForTest(t, ctx, qc, "wf-upstream-worker", 2)
	moveWorker := func(step int32) {
		_, err := runCLICommand(c, []string{"worker", "update", "--worker-id", fmt.Sprint(workerID), "--step-id", fmt.Sprint(step)})
		require.NoError(t, err)
	}
	status := func(id int32) string { return getTask(t, ctx, qc, id).Status }

	// Without a retry budget, the sample's failure is final.
	sample, err := qc.SubmitTask(ctx, &pb.TaskRequest{StepId: &sampleStep, Command: "false", Container: "alpine", Status: "P"})
	require.NoError(t, err)
	profile, err := qc.SubmitTask(ctx, &pb.TaskRequest{StepId: &profileStep, Command: "true", Container: "alpine",
		Status: "W", Dependency: []int32{sample.TaskId}})
	require.NoError(t, err)
	compile, err := qc.SubmitTask(ctx, &pb.TaskRequest{StepId: &compileStep, Command: "true", Container: "alpine",
		Status: "W", Dependency: []int32{sample.TaskId, profile.TaskId}, AcceptFailure: true})
	require.NoError(t, err)

	moveWorker(sampleStep)
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprint(wfID), "--status", "R"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return status(sample.TaskId) == "A" }, 6*time.Second, 200*time.Millisecond)
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: sample.TaskId, NewStatus: "F"})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return status(profile.TaskId) == "F" }, 6*time.Second, 200*time.Millisecond,
		"the profile cannot run without its sample")
	require.Equal(t, "upstream", getTask(t, ctx, qc, profile.TaskId).GetFailureClass())
	require.Equal(t, "R", getWorkflowStatus(t, ctx, qc, wfID), "the compile can still run")

	moveWorker(compileStep)
	require.Eventually(t, func() bool { return status(compile.TaskId) == "A" }, 6*time.Second, 200*time.Millisecond,
		"the compile accepts the failures")
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: compile.TaskId, NewStatus: "S"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return getWorkflowStatus(t, ctx, qc, wfID) == "S" }, 6*time.Second, 200*time.Millisecond)

	// Retrying the sample reopens the profile and the workflow.
	retried, err := qc.RetryTask(ctx, &pb.RetryTaskRequest{TaskId: sample.TaskId})
	require.NoError(t, err)
	require.Equal(t, "W", status(profile.TaskId), "the profile waits for the retried sample")
	require.Empty(t, getTask(t, ctx, qc, profile.TaskId).GetFailureClass())
	require.Equal(t, "R", getWorkflowStatus(t, ctx, qc, wfID))

	moveWorker(sampleStep)
	require.Eventually(t, func() bool { return status(retried.TaskId) == "A" }, 6*time.Second, 200*time.Millisecond)
	_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: retried.TaskId, NewStatus: "S"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return status(profile.TaskId) == "P" }, 6*time.Second, 200*time.Millisecond)
}

// A task forced to run in a finished workflow sets it back to running, so
// that it is assigned.
func TestForceRunReopensWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	c.Attr.Token = extractToken(out)
	qclient, err := lib.CreateClient(serverAddr, c.Attr.Token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfID := createWorkflowViaCLI(t, c, "wf-force-run")
	stepID := createStepViaCLI(t, c, wfID, "s")
	waiting, err := qc.SubmitTask(ctx, &pb.TaskRequest{StepId: &stepID, Command: "true", Container: "alpine", Status: "W"})
	require.NoError(t, err)
	_, err = runCLICommand(c, []string{"workflow", "update", "--id", fmt.Sprint(wfID), "--status", "F"})
	require.NoError(t, err)

	_, err = qc.ForceRunTask(ctx, &pb.ForceRunTaskRequest{TaskId: waiting.TaskId})
	require.NoError(t, err)
	require.Equal(t, "R", getWorkflowStatus(t, ctx, qc, wfID))
}
