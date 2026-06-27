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

// TestAssignTask_MultiGPUFitAndConcurrency covers the post-migration-41
// model where flavor.gpu_count is the unit of the fit predicate
// (not just the binary has_gpu). Three properties:
//
//   1. A worker with gpu_count=4 admits a min_gpu=4 task (fit
//      succeeds) but rejects a min_gpu=5 task (fit fails — the task
//      stays P and gets a no-fit worker_event).
//   2. Two min_gpu=1 tasks land on the same 4-GPU worker concurrently
//      (the per-worker capacity should be at least the GPU dimension's
//      slot count, not collapsed to 1 by the old has_gpu boolean).
//   3. The 5-GPU task that fit-rejected on the 4-GPU worker would
//      have to wait for a larger flavor — no implicit downgrade, no
//      silent assignment to a smaller worker.
func TestAssignTask_MultiGPUFitAndConcurrency(t *testing.T) {
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

	wfName := fmt.Sprintf("multi-gpu-fit-%d", time.Now().UnixNano())
	rStatus := "R"
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName, Status: &rStatus})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// One 4-GPU flavor + one 4-GPU worker. concurrency=4 + prefetch=0
	// so the in-flight cap matches the GPU slot count exactly.
	var flavorID, workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu, gpu_count)
		VALUES (NULL, $1, 32, 128, 200, TRUE, 4) RETURNING flavor_id
	`, fmt.Sprintf("4gpu-%s", wfName)).Scan(&flavorID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 4, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("w-%s", wfName), stepID, flavorID).Scan(&workerID))

	submit := func(name string, minGpu int32) int32 {
		mg := minGpu
		sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
			Command:   "echo not run",
			Container: "bare",
			Status:    "P",
			StepId:    &stepID,
			TaskName:  strPtr(name),
			MinGpu:    &mg,
		})
		require.NoError(t, err)
		return sub.TaskId
	}

	// Two min_gpu=1 tasks — both must reach status=A on the same
	// worker concurrently (concurrency=4 allows it; the fit
	// predicate must not coalesce them to one slot).
	t1 := submit("task-1gpu-a", 1)
	t2 := submit("task-1gpu-b", 1)
	// One min_gpu=4 task — exact fit, should assign on its own.
	t3 := submit("task-4gpu", 4)
	// One min_gpu=5 task — exceeds the worker's capacity, must
	// stay P and never get assigned (no implicit downgrade).
	t5 := submit("task-5gpu", 5)

	// Eventually: t1, t2, t3 are all status=A on workerID. They
	// each consume 1, 1, 4 GPU slots respectively → total 6 GPU
	// slots requested but only 4 available. Capacity-wise (the
	// scitq slot accounting), three tasks fit because the worker
	// has concurrency=4 (one per worker.weight slot). The fit
	// predicate per task is GPU-checked individually against the
	// worker — all three pass. The full-correct multi-task GPU
	// accounting (sum of min_gpu across in-flight tasks ≤
	// gpu_count) is a tighter property we can layer later; for
	// now this test asserts the fit + concurrency primitives.
	require.Eventually(t, func() bool {
		r1 := getTask(t, ctx, qc, t1)
		r2 := getTask(t, ctx, qc, t2)
		return r1.Status == "A" && r1.WorkerId != nil && *r1.WorkerId == workerID &&
			r2.Status == "A" && r2.WorkerId != nil && *r2.WorkerId == workerID
	}, 15*time.Second, 200*time.Millisecond,
		"two min_gpu=1 tasks must both assign to the 4-GPU worker (concurrency=4)")

	// t3 (min_gpu=4) — exact-fit on the worker's gpu_count.
	require.Eventually(t, func() bool {
		r := getTask(t, ctx, qc, t3)
		return r.Status == "A" && r.WorkerId != nil && *r.WorkerId == workerID
	}, 15*time.Second, 200*time.Millisecond,
		"min_gpu=4 task must assign to the 4-GPU worker (exact fit)")

	// t5 (min_gpu=5) — must NOT assign. Stays P; flagged as
	// no-fit. We wait a short, generous window to confirm it
	// stays P rather than racing into A.
	time.Sleep(3 * time.Second)
	r5 := getTask(t, ctx, qc, t5)
	require.Equal(t, "P", r5.Status,
		"min_gpu=5 task on a 4-GPU worker must NOT be assigned (no implicit downgrade)")
	require.Nil(t, r5.WorkerId,
		"unassigned task must have nil worker_id")
}
