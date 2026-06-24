package integration_test

import (
	"context"
	"database/sql"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestEditTaskMinFields_PersistsAndIsObservable verifies the EditTask
// path can mutate the per-task fit floors (min_cpu / min_mem /
// min_disk). The motivating scenario is the bigbrother / skani
// allgenomes case where the operator needs to nudge a stuck task
// (worker has flavor_mem=15.6, task min_mem=40 → fitsWorker returns
// false, no assignment) down to a value the available worker can
// pass. Without these fields on EditTask the only path was psql.
//
// We verify three properties:
//   1. EditTask accepts the new fields and the row reflects them
//      both in the gRPC GetTask view AND directly in the DB column
//      (the assignment SQL reads the DB, not the API view).
//   2. Other fields are untouched (the absent-= don't change semantic
//      that the rest of EditTask already honours).
//   3. Round-tripping zero is fine (the proto's `optional` distinguishes
//      "field unset" from "set to 0" — we lean on that for the
//      reset-to-zero use case).
func TestEditTaskMinFields_PersistsAndIsObservable(t *testing.T) {
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

	// Submit with the YAML-style heavy floor (mirrors what
	// task_spec.mem produces for skani-search allgenomes).
	initialMin := float32(40)
	zero := float32(0)
	sub, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo not actually run",
		Container: "bare",
		Status:    "P",
		TaskName:  strPtr("min-floors-edit"),
		MinCpu:    &initialMin,
		MinMem:    &initialMin,
		MinDisk:   &initialMin,
	})
	require.NoError(t, err)
	taskID := sub.TaskId

	// Sanity: the floors landed on submit.
	row := getTask(t, ctx, qc, taskID)
	require.NotNil(t, row.MinCpu)
	require.NotNil(t, row.MinMem)
	require.NotNil(t, row.MinDisk)
	require.InDelta(t, 40.0, *row.MinCpu, 0.001)
	require.InDelta(t, 40.0, *row.MinMem, 0.001)
	require.InDelta(t, 40.0, *row.MinDisk, 0.001)

	// Edit ONLY min_mem — leave min_cpu / min_disk alone. Mirrors the
	// real "lower mem for bigbrother" intervention.
	loweredMem := float32(15)
	_, err = qc.EditTask(ctx, &pb.EditTaskRequest{
		TaskId: taskID,
		MinMem: &loweredMem,
	})
	require.NoError(t, err)

	// Check via the API surface that operators see in `scitq task list -l`.
	row = getTask(t, ctx, qc, taskID)
	require.NotNil(t, row.MinMem)
	require.InDelta(t, 15.0, *row.MinMem, 0.001, "min_mem should be lowered")
	require.NotNil(t, row.MinCpu)
	require.InDelta(t, 40.0, *row.MinCpu, 0.001, "min_cpu must NOT change (field absent on the EditTask)")
	require.NotNil(t, row.MinDisk)
	require.InDelta(t, 40.0, *row.MinDisk, 0.001, "min_disk must NOT change")

	// Verify the *DB column* — the assignment SQL reads here, not the
	// API view, so a divergence between the two would be a latent bug
	// where the API claims the edit succeeded but fit checks still
	// see the old value.
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()
	var dbCpu, dbMem, dbDisk sql.NullFloat64
	require.NoError(t, db.QueryRow(
		`SELECT min_cpu, min_mem, min_disk FROM task WHERE task_id = $1`,
		taskID,
	).Scan(&dbCpu, &dbMem, &dbDisk))
	require.True(t, dbCpu.Valid && dbMem.Valid && dbDisk.Valid)
	require.InDelta(t, 40.0, dbCpu.Float64, 0.001)
	require.InDelta(t, 15.0, dbMem.Float64, 0.001)
	require.InDelta(t, 40.0, dbDisk.Float64, 0.001)

	// Round-trip zero: a deliberate 0 isn't the same as "field absent".
	// Operators may legitimately want to drop the floor entirely.
	_, err = qc.EditTask(ctx, &pb.EditTaskRequest{
		TaskId:  taskID,
		MinDisk: &zero,
	})
	require.NoError(t, err)
	row = getTask(t, ctx, qc, taskID)
	// 0.0 may round-trip as "field present, value 0" rather than "field
	// absent" depending on how the proto runtime handles default values
	// for optional float32. Both are acceptable as "no disk floor"; we
	// only check we didn't accidentally bump it back to 40.
	if row.MinDisk != nil {
		require.InDelta(t, 0.0, *row.MinDisk, 0.001)
	}
}
