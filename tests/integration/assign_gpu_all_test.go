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

// TestAssignTask_GpuAllResolvesAtAssignment exercises the
// `task_spec.gpu="all"` sentinel from spec/gpu_all_sentinel.md.
//
// Property: a task submitted with gpu_all=TRUE and min_gpu unset
// must (a) fit any worker whose flavor.gpu_count >= 1, and (b)
// have its min_gpu materialized to worker.flavor.gpu_count at the
// moment the assignment UPDATE flips status P→A. The sentinel
// stays on the row so a retry onto a differently-sized flavor
// re-resolves cleanly.
//
// Setup: one 4-GPU flavor + one matching worker. Submit a sentinel
// task. Assert it lands on the worker with min_gpu=4 and
// gpu_all=TRUE persisted.
func TestAssignTask_GpuAllResolvesAtAssignment(t *testing.T) {
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

	wfName := fmt.Sprintf("gpu-all-%d", time.Now().UnixNano())
	rStatus := "R"
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName, Status: &rStatus})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// 4-GPU flavor + matching worker. concurrency=1 because gpu="all"
	// is one task per worker — the recruiter would pin this on the
	// Python side; here we set it on the worker row directly since
	// the test bypasses recruitment.
	var flavorID, workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu, gpu_count)
		VALUES (NULL, $1, 32, 128, 200, TRUE, 4) RETURNING flavor_id
	`, fmt.Sprintf("4gpu-%s", wfName)).Scan(&flavorID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 1, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("w-%s", wfName), stepID, flavorID).Scan(&workerID))

	// Sentinel submission: gpu_all=TRUE, min_gpu left nil.
	gpuAll := true
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo not run",
		Container: "bare",
		Status:    "P",
		StepId:    &stepID,
		TaskName:  strPtr("task-gpu-all"),
		GpuAll:    &gpuAll,
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	// Submission row: gpu_all=TRUE, min_gpu=NULL. This is the state
	// before assignment — the integer count hasn't been chosen yet.
	var preMinGpu sql.NullInt32
	var preGpuAll bool
	require.NoError(t, db.QueryRow(`
		SELECT min_gpu, gpu_all FROM task WHERE task_id = $1
	`, taskID).Scan(&preMinGpu, &preGpuAll))
	require.False(t, preMinGpu.Valid,
		"freshly-submitted gpu_all task must have min_gpu=NULL (resolution is deferred)")
	require.True(t, preGpuAll,
		"freshly-submitted gpu_all task must have gpu_all=TRUE")

	// Wait for assignment, then verify the row got materialized.
	require.Eventually(t, func() bool {
		r := getTask(t, ctx, qc, taskID)
		return r.Status == "A" && r.WorkerId != nil && *r.WorkerId == workerID
	}, 15*time.Second, 200*time.Millisecond,
		"sentinel task must assign to the 4-GPU worker")

	// Post-assignment row: min_gpu materialized to flavor.gpu_count (4),
	// gpu_all stays TRUE so retries re-resolve.
	var postMinGpu sql.NullInt32
	var postGpuAll bool
	require.NoError(t, db.QueryRow(`
		SELECT min_gpu, gpu_all FROM task WHERE task_id = $1
	`, taskID).Scan(&postMinGpu, &postGpuAll))
	require.True(t, postMinGpu.Valid,
		"assignment must materialize min_gpu from flavor.gpu_count")
	require.Equal(t, int32(4), postMinGpu.Int32,
		"materialized min_gpu must equal worker.flavor.gpu_count")
	require.True(t, postGpuAll,
		"gpu_all stays TRUE post-assignment so retries onto another flavor re-resolve")
}

// TestAssignTask_GpuAllRejectsNonGPUWorker: sentinel tasks must NOT
// land on CPU-only workers. The fit predicate translates gpu_all=TRUE
// to "worker.gpu_count >= 1" via the COALESCE in assigntask.go.
func TestAssignTask_GpuAllRejectsNonGPUWorker(t *testing.T) {
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

	wfName := fmt.Sprintf("gpu-all-cpu-only-%d", time.Now().UnixNano())
	rStatus := "R"
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName, Status: &rStatus})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// CPU-only worker (gpu_count=0).
	var flavorID, workerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu, gpu_count)
		VALUES (NULL, $1, 32, 128, 200, FALSE, 0) RETURNING flavor_id
	`, fmt.Sprintf("cpu-%s", wfName)).Scan(&flavorID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 4, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("w-%s", wfName), stepID, flavorID).Scan(&workerID))

	gpuAll := true
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo not run",
		Container: "bare",
		Status:    "P",
		StepId:    &stepID,
		TaskName:  strPtr("task-gpu-all-rejected"),
		GpuAll:    &gpuAll,
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	// Wait a generous window — the task must stay P (the fit
	// predicate filters it out, no implicit downgrade onto a
	// CPU-only worker).
	time.Sleep(3 * time.Second)
	r := getTask(t, ctx, qc, taskID)
	require.Equal(t, "P", r.Status,
		"gpu_all task must not assign to a CPU-only worker")
	require.Nil(t, r.WorkerId,
		"unassigned gpu_all task must have nil worker_id")

	// min_gpu must still be NULL — no assignment ran, so no
	// resolution happened.
	var postMinGpu sql.NullInt32
	var postGpuAll bool
	require.NoError(t, db.QueryRow(`
		SELECT min_gpu, gpu_all FROM task WHERE task_id = $1
	`, taskID).Scan(&postMinGpu, &postGpuAll))
	require.False(t, postMinGpu.Valid,
		"unassigned gpu_all task must keep min_gpu=NULL")
	require.True(t, postGpuAll,
		"gpu_all stays TRUE on unassigned tasks")
}
