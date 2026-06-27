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

// TestAssignTask_GPURequiredRoutedToHasGPUWorker pins down the GPU
// dimension of the worker-fit predicate (the fourth alongside
// cpu/mem/disk). Setup: two workers on the same step with identical
// cpu/mem/disk; one's flavor has has_gpu=false, the other has
// has_gpu=true. A task with min_gpu=1 must route to the has_gpu
// worker exclusively; a task with min_gpu=0 (or unset) must not be
// blocked by the predicate.
//
// Pre-fix (no gpu dimension): both workers were treated as
// interchangeable, the assignment loop would happily route the
// GPU-required task to a CPU-only worker, the container's
// `--gpus all` would fail, and the task would die at runtime with
// "Failed to initialize NVML" — silent at scheduling time.
func TestAssignTask_GPURequiredRoutedToHasGPUWorker(t *testing.T) {
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

	wfName := fmt.Sprintf("gpu-fit-%d", time.Now().UnixNano())
	rStatus := "R"
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName, Status: &rStatus})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "step"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	// Two flavors with IDENTICAL cpu/mem/disk; only has_gpu differs.
	// That isolates the gpu dimension as the discriminator.
	var cpuFlavorID, gpuFlavorID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu)
		VALUES (NULL, $1, 8, 32, 100, FALSE) RETURNING flavor_id
	`, fmt.Sprintf("cpu-%s", wfName)).Scan(&cpuFlavorID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk, has_gpu)
		VALUES (NULL, $1, 8, 32, 100, TRUE)  RETURNING flavor_id
	`, fmt.Sprintf("gpu-%s", wfName)).Scan(&gpuFlavorID))

	// Two workers on the step. concurrency=2 so each can host both
	// tasks if the fit predicate doesn't filter — keeps the test
	// from accidentally passing because of capacity exhaustion.
	var cpuWorkerID, gpuWorkerID int32
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 2, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("cpu-%s", wfName), stepID, cpuFlavorID).Scan(&cpuWorkerID))
	require.NoError(t, db.QueryRow(`
		INSERT INTO worker (worker_name, step_id, concurrency, prefetch, status, flavor_id, is_permanent)
		VALUES ($1, $2, 2, 0, 'R', $3, TRUE) RETURNING worker_id
	`, fmt.Sprintf("gpu-%s", wfName), stepID, gpuFlavorID).Scan(&gpuWorkerID))

	// Submit one task that REQUIRES a GPU and one that doesn't.
	// The required-GPU task is submitted first so it has the older
	// created_at — without the fit predicate, the smallest-first
	// sort (which doesn't yet consider gpu) would happily hand it to
	// the cpu worker. With the predicate, the cpu worker's SELECT
	// excludes it entirely.
	one := int32(1)
	gpuTaskID, err := submitGPUFitTask(ctx, qc, stepID, "gpu-required", &one)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	cpuTaskID, err := submitGPUFitTask(ctx, qc, stepID, "no-gpu", nil)
	require.NoError(t, err)

	// Eventually: GPU task lands on the GPU worker, CPU task lands
	// somewhere (we don't pin which — either worker fits an unsetter).
	require.Eventually(t, func() bool {
		row := getTask(t, ctx, qc, gpuTaskID)
		return row.Status == "A" && row.WorkerId != nil && *row.WorkerId == gpuWorkerID
	}, 15*time.Second, 200*time.Millisecond,
		"min_gpu=1 task must route to the has_gpu=true worker, never the cpu-only one")

	require.Eventually(t, func() bool {
		row := getTask(t, ctx, qc, cpuTaskID)
		return row.Status == "A" && row.WorkerId != nil
	}, 5*time.Second, 200*time.Millisecond,
		"min_gpu=0 task must be assignable (no gpu predicate when min_gpu is unset)")

	// Hard negative: the cpu worker must NEVER have picked up the
	// GPU task. Re-check the worker_id after both tasks are settled.
	gpuRow := getTask(t, ctx, qc, gpuTaskID)
	require.NotNil(t, gpuRow.WorkerId)
	require.NotEqual(t, cpuWorkerID, *gpuRow.WorkerId,
		"GPU task assigned to cpu-only worker — fit predicate failed")
}

func submitGPUFitTask(ctx context.Context, qc pb.TaskQueueClient, stepID int32, name string, minGpu *int32) (int32, error) {
	req := &pb.TaskRequest{
		Command:   "echo not run",
		Container: "bare",
		Status:    "P",
		StepId:    &stepID,
		TaskName:  strPtr(name),
	}
	if minGpu != nil {
		req.MinGpu = minGpu
	}
	sub, err := qc.SubmitTask(ctx, req)
	if err != nil {
		return 0, err
	}
	return sub.TaskId, nil
}
