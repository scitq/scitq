package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestAssignTask_SmallTasksGoToSmallWorkers reproduces the alpha2
// incident from 2026-06-24 step 73888: workers of mixed flavor share
// a step, big-mem tasks dominate the head of the FIFO queue, and a
// small-fitting task sits further down. Before the per-worker fit
// fix the scheduler fetched `LIMIT slots` (summed across workers) by
// created_at, then assigned in arbitrary worker order — a big worker
// could sweep up small-fitting tasks the small worker would also
// have accepted, AND the small worker would only see head-of-queue
// tasks that didn't fit it, emitting no-fit warnings forever.
//
// Post-fix behaviour:
//   - Workers are processed smallest-flavor-first.
//   - Each worker SELECTs only tasks that fit it (predicate baked
//     into SQL); LIMIT is the worker's own slot count.
//   - The small worker claims the small task even when older,
//     non-fitting tasks sit at the queue head.
func TestAssignTask_SmallTasksGoToSmallWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	defer qclient.Close()
	qc := qclient.Client

	// Workflow created in R so the assignment SQL's `w.status='R'`
	// gate doesn't exclude our tasks.
	wfName := fmt.Sprintf("small-first-%d", time.Now().UnixNano())
	rStatus := "R"
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName, Status: &rStatus})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	// Inject flavors + workers directly. We don't need a real worker
	// process — the assignment loop reads worker / flavor rows from
	// the DB. status='R' + flavor caps are all that matter for fit.
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	var smallFlavorID, bigFlavorID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk)
		VALUES (NULL, $1, 4, 15, 50) RETURNING flavor_id
	`, fmt.Sprintf("small-%s", wfName)).Scan(&smallFlavorID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk)
		VALUES (NULL, $1, 16, 200, 200) RETURNING flavor_id
	`, fmt.Sprintf("big-%s", wfName)).Scan(&bigFlavorID))

	var smallWorkerID, bigWorkerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 1, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("small-%s", wfName), stepID, smallFlavorID).Scan(&smallWorkerID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 1, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("big-%s", wfName), stepID, bigFlavorID).Scan(&bigWorkerID))

	// Submit 3 tasks: 2 oldest with min_mem=40 (only big worker
	// fits), 1 newest with min_mem=10 (both fit). The fixed scheduler
	// must route the small task to the small worker.
	big := float32(40)
	small := float32(10)
	submit := func(name string, minMem *float32) int32 {
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:   "echo not run",
			Container: "bare",
			Status:    "P",
			StepId:    &stepID,
			TaskName:  strPtr(name),
			MinMem:    minMem,
		})
		require.NoError(t, err)
		return sub.TaskId
	}
	bigT1 := submit("big-1", &big)
	time.Sleep(50 * time.Millisecond) // distinct created_at
	bigT2 := submit("big-2", &big)
	time.Sleep(50 * time.Millisecond)
	smallT := submit("small", &small)

	// Wait for the assignment loop to route the small task to the
	// small worker. The loop ticks every ~5s by default but
	// triggerAssign() fires on each task submit, so this is usually
	// instant.
	require.Eventually(t, func() bool {
		row := getTask(t, ctx, qc, smallT)
		return row.Status == "A" && row.WorkerId != nil && *row.WorkerId == smallWorkerID
	}, 15*time.Second, 200*time.Millisecond,
		"small task must be assigned to the small worker — pre-fix it would be eaten by the big worker or never seen")

	// Sanity: exactly one big task assigned (big worker has 1 slot),
	// to the big worker. The other stays P.
	bigT1Row := getTask(t, ctx, qc, bigT1)
	bigT2Row := getTask(t, ctx, qc, bigT2)
	assignedBig := 0
	for _, r := range []*pb.Task{bigT1Row, bigT2Row} {
		if r.Status == "A" {
			assignedBig++
			require.NotNil(t, r.WorkerId)
			require.Equal(t, bigWorkerID, *r.WorkerId, "big task must be assigned to the big worker")
		}
	}
	require.Equal(t, 1, assignedBig, "exactly one of the two big tasks should be assigned (big worker has 1 slot)")
}
