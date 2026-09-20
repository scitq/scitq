package integration_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
)

// TestWorkflowLifetimeSweep exercises the outputs.lifetime="workflow"
// contract end-to-end:
//
//  1. Create a workflow with two steps — one declared as ephemeral
//     (output_lifetime='W'), one as persistent (default NULL).
//  2. Point each step's task at a real local-filesystem output URI that
//     the test seeds with a file.
//  3. Mark the tasks succeeded so recomputeWorkflowStatus fires
//     workflow -> S.
//  4. Wait for the fire-and-forget sweep goroutine to run, then assert
//     the ephemeral prefix is gone and the persistent one is untouched.
//
// The sweep resolves URIs through fetch.ParseURI + rclone; a bare
// absolute path routes through the local backend, so the test doesn't
// need cloud credentials. If this ever starts failing, likely culprits
// are (a) recomputeWorkflowStatus stopped calling the sweep, (b) the
// enumeration query lost the AND NOT hidden clause and swept nothing,
// or (c) rclone's local-backend Purge stopped treating "already gone"
// as success.
func TestWorkflowLifetimeSweep(t *testing.T) {
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

	// Seed two workspace directories with a marker file. The sweep
	// works at the *prefix* level (task.output is a directory URI),
	// so we don't need the file to look like a real upload — anything
	// under the prefix is fair game.
	root := t.TempDir()
	ephemeralDir := filepath.Join(root, "ephemeral") + string(filepath.Separator)
	persistentDir := filepath.Join(root, "persistent") + string(filepath.Separator)
	for _, d := range []string{ephemeralDir, persistentDir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(d, "sentinel.txt"), []byte("seeded"), 0o644))
	}

	// Workflow + two steps. The ephemeral step goes in via
	// output_lifetime="workflow"; the persistent one uses the default.
	wfResp, err := qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: "lifetime-sweep-test"})
	require.NoError(t, err)
	wfID := wfResp.WorkflowId

	lifetime := "workflow"
	epStep, err := qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId:     &wfID,
		Name:           "ephemeral",
		OutputLifetime: &lifetime,
	})
	require.NoError(t, err)

	persistStep, err := qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId: &wfID,
		Name:       "persistent",
	})
	require.NoError(t, err)

	// Bad value must be refused loudly, not silently dropped — a typo
	// like "worflow" would otherwise look like "cleanup didn't run".
	bogus := "forever"
	_, err = qc.CreateStep(ctx, &pb.StepRequest{
		WorkflowId:     &wfID,
		Name:           "bogus",
		OutputLifetime: &bogus,
	})
	require.Error(t, err, "unknown output_lifetime must be rejected")

	// One task per step, each pointing its output at the seeded dir.
	epURI := ephemeralDir
	persistURI := persistentDir
	epTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo done",
		Container: "bare",
		StepId:    &epStep.StepId,
		Output:    &epURI,
		Status:    "P",
	})
	require.NoError(t, err)
	persistTask, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo done",
		Container: "bare",
		StepId:    &persistStep.StepId,
		Output:    &persistURI,
		Status:    "P",
	})
	require.NoError(t, err)

	// Complete both tasks. recomputeWorkflowStatus fires from each
	// UpdateTaskStatus; the second one flips the workflow to S and
	// launches the sweep goroutine.
	for _, id := range []int32{epTask.TaskId, persistTask.TaskId} {
		_, err = qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    id,
			NewStatus: "S",
		})
		require.NoError(t, err, "failed marking task %d succeeded", id)
	}

	// Wait for the workflow to actually be S — recompute cascades
	// through a couple of goroutines, so poll rather than assume
	// synchronous. Then wait for the sweep itself (also a goroutine).
	require.Eventually(t, func() bool {
		wfList, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
		if err != nil {
			return false
		}
		for _, wf := range wfList.Workflows {
			if wf.WorkflowId == wfID && wf.Status == "S" {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "workflow never transitioned to S")

	// Ephemeral dir must be gone; persistent dir must survive. We
	// give the sweep a generous window — rclone-on-local-fs is fast
	// but the sweep runs in a detached goroutine.
	require.Eventually(t, func() bool {
		_, err := os.Stat(ephemeralDir)
		return os.IsNotExist(err)
	}, 5*time.Second, 100*time.Millisecond,
		"ephemeral workspace %q should have been swept", ephemeralDir)

	_, err = os.Stat(filepath.Join(persistentDir, "sentinel.txt"))
	require.NoError(t, err, "persistent workspace must be untouched by the sweep")

	// Also verify the step's output_lifetime column was actually
	// written — a silent NULL would let the sweep no-op and the
	// test pass for the wrong reason if the dir happened not to
	// exist.
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		return
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return
	}
	defer db.Close()
	var got sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT output_lifetime FROM step WHERE step_id=$1`, epStep.StepId).Scan(&got)
	require.NoError(t, err)
	require.True(t, got.Valid && got.String == "W",
		"ephemeral step should have output_lifetime='W', got %+v", got)
}
