package integration_test

import (
	"context"
	"database/sql"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	srv "github.com/scitq/scitq/server"
	"github.com/stretchr/testify/require"
)

// A step none of whose tasks ran (a skipped step) has no start or end
// time. Reported as 0, its start (1970) made the workflow's elapsed time
// decades long in `scitq step stats --totals`.
func TestStepStatsTimesOfStepThatDidNotRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	qclient, err := lib.CreateClient(serverAddr, extractToken(out))
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	wfName := "step-times"
	wf, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	taskIn := func(stepName string) (int32, int32) {
		step, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: stepName})
		require.NoError(t, err)
		task, err := qc.SubmitTask(ctx, &pb.TaskRequest{Command: "true", Container: "bare", Status: "P", StepId: &step.StepId})
		require.NoError(t, err)
		return step.StepId, task.TaskId
	}
	ranStep, ranTask := taskIn("ran")
	skippedStep, skippedTask := taskIn("skipped")

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`UPDATE task SET status='S', run_started_at=to_timestamp(1000000),
		download_duration=10, run_duration=100, upload_duration=5 WHERE task_id=$1`, ranTask)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE task SET status='S' WHERE task_id=$1`, skippedTask)
	require.NoError(t, err)

	agg, err := srv.NewStepStatsAgg(db)
	require.NoError(t, err)
	start, end := agg.StepTimes(wf.WorkflowId, ranStep)
	require.NotNil(t, start)
	require.NotNil(t, end)
	require.Equal(t, int64(1000000-10), *start)
	require.Equal(t, int64(1000000+100+5), *end)
	start, end = agg.StepTimes(wf.WorkflowId, skippedStep)
	require.Nil(t, start, "start of a step that did not run")
	require.Nil(t, end, "end of a step that did not run")
}
