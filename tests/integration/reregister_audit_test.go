package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// A worker that registers again (its client restarted) loses its active
// tasks: they fail as evictions, and the audit trail names the
// re-registration as the actor.
func TestReRegistrationEvictionIsAudited(t *testing.T) {
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

	one := int32(1)
	reg, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{Name: "rereg-w", Concurrency: &one})
	require.NoError(t, err)
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{Command: "sleep 600", Container: "alpine", Status: "P"})
	require.NoError(t, err)

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`UPDATE task SET status='R', worker_id=$1, run_started_at=now() WHERE task_id=$2`, reg.WorkerId, task.TaskId)
	require.NoError(t, err)

	_, err = qc.RegisterWorker(ctx, &pb.WorkerInfo{Name: "rereg-w", Concurrency: &one})
	require.NoError(t, err)

	var status, class string
	require.NoError(t, db.QueryRow(`SELECT status, COALESCE(failure_class, '') FROM task WHERE task_id=$1`, task.TaskId).Scan(&status, &class))
	require.Equal(t, "F", status)
	require.Equal(t, "eviction", class)

	kind := "task"
	res, err := qc.ListAuditEvents(ctx, &pb.AuditFilter{ObjectKind: &kind, ObjectId: &task.TaskId})
	require.NoError(t, err)
	actor := fmt.Sprintf("worker %d re-registration", reg.WorkerId)
	for _, e := range res.Events {
		if e.Action == "task.status" && e.ActorKind == "scheduler" && e.ActorName == actor {
			return
		}
	}
	require.Failf(t, "eviction not audited", "task %d: no task.status by scheduler %q; got %v", task.TaskId, actor, res.Events)
}

// On re-registration the server keeps the active tasks whose container the
// client reports still running, and fails the others.
func TestReRegistrationKeepsReportedTasks(t *testing.T) {
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

	one := int32(1)
	reg, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{Name: "keep-w", Concurrency: &one})
	require.NoError(t, err)
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	running := func() int32 {
		task, err := qc.SubmitTask(ctx, &pb.TaskRequest{Command: "sleep 600", Container: "alpine", Status: "P"})
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE task SET status='R', worker_id=$1, run_started_at=now() WHERE task_id=$2`, reg.WorkerId, task.TaskId)
		require.NoError(t, err)
		return task.TaskId
	}
	reported, lost := running(), running()

	res, err := qc.RegisterWorker(ctx, &pb.WorkerInfo{Name: "keep-w", Concurrency: &one, RunningTaskIds: []int32{reported, 999999}})
	require.NoError(t, err)
	require.Equal(t, []int32{reported}, res.KeptTaskIds, "only the reported task the server still assigns is kept")

	status := func(id int32) (s string) {
		require.NoError(t, db.QueryRow(`SELECT status FROM task WHERE task_id=$1`, id).Scan(&s))
		return s
	}
	require.Equal(t, "R", status(reported))
	require.Equal(t, "F", status(lost))
}
