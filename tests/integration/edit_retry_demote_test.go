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

// TestEditAndRetryTask_DemotesCloneWhenPrereqsUnmet: when
// EditAndRetryTask's Depends override points at a prereq that is
// NOT yet succeeded, the retry clone MUST end up in status 'W'
// (waiting), never 'P' (ready-to-run). Historically the retry SQL
// hardcoded 'P' at INSERT time and never re-checked prereqs after
// dep INSERT — so an extend re-run that repointed a dependent at
// freshly-created P prereqs would produce a P clone the assignment
// loop happily grabbed. Symptom: "bin ran while train never
// succeeded" on alpha2 workflow 3174, 2026-07-01.
//
// Setup: create prereq task in P (unmet), create dependent, then
// EditAndRetryTask(dependent, Depends=[prereq]). Assert the clone
// is in status 'W' and its task_dependencies row references the
// unmet prereq.
func TestEditAndRetryTask_DemotesCloneWhenPrereqsUnmet(t *testing.T) {
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

	wfName := fmt.Sprintf("edit-retry-demote-%d", time.Now().UnixNano())
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "s"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	shell := "sh"

	// Prereq: submit and leave in P (unmet — never marked S).
	prereqResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo prereq",
		Shell:     &shell,
		Container: "bare",
		Status:    "P",
		StepId:    &stepID,
	})
	require.NoError(t, err)
	prereqID := prereqResp.TaskId

	// Dependent: initially submit as 'F' (so EditAndRetryTask can
	// clone it). Give it a bogus dependency so it doesn't run before
	// we retry — that's actually the shape we're modelling: a task
	// that already failed and the operator/extend is retrying it
	// against new prereqs.
	depResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo dependent",
		Shell:     &shell,
		Container: "bare",
		Status:    "F",
		StepId:    &stepID,
	})
	require.NoError(t, err)
	dependentID := depResp.TaskId

	// EditAndRetryTask with Depends override pointing at the still-P
	// prereq. Pre-fix: the clone comes back in status 'P' and the
	// scheduler picks it up immediately. Post-fix: the check-and-
	// demote SQL at the end of retryTaskInternal moves it to 'W'.
	editReq := &pb.EditAndRetryTaskRequest{
		TaskId:  dependentID,
		Command: "echo dependent retry",
		Depends: &pb.Int32List{Values: []int32{prereqID}},
	}
	cloneResp, err := qc.EditAndRetryTask(ctx, editReq)
	require.NoError(t, err)
	cloneID := cloneResp.TaskId
	require.NotZero(t, cloneID)

	// Read the clone's status directly from the DB — the RPC
	// response only tells us the id.
	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	var cloneStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM task WHERE task_id = $1`, cloneID).Scan(&cloneStatus))
	require.Equal(t, "W", cloneStatus,
		"retry clone MUST be in status 'W' when its overridden prereq is not 'S' — pre-fix behaviour was 'P', letting the scheduler assign it immediately")

	// Sanity: the dep row was actually created and points at the
	// unmet prereq. If this weren't the case, the W status could be
	// hiding a totally-broken dep graph rather than a correctly-
	// gated clone.
	var depCount int
	require.NoError(t, db.QueryRow(`
		SELECT count(*) FROM task_dependencies
		WHERE dependent_task_id = $1 AND prerequisite_task_id = $2
	`, cloneID, prereqID).Scan(&depCount))
	require.Equal(t, 1, depCount,
		"the override Depends must have inserted a task_dependencies row from clone → prereq")
}

// TestEditAndRetryTask_KeepsCloneP_WhenPrereqsMet: the mirror case.
// If all overridden prereqs are already 'S', the clone should be
// 'P' (ready to run) — we don't want to over-demote and stall
// legitimate retries that are actually unblocked.
func TestEditAndRetryTask_KeepsCloneP_WhenPrereqsMet(t *testing.T) {
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

	wfName := fmt.Sprintf("edit-retry-keep-p-%d", time.Now().UnixNano())
	_, err = qc.CreateWorkflow(ctx, &pb.WorkflowRequest{Name: wfName})
	require.NoError(t, err)
	stepResp, err := qc.CreateStep(ctx, &pb.StepRequest{WorkflowName: &wfName, Name: "s"})
	require.NoError(t, err)
	stepID := stepResp.StepId

	shell := "sh"

	// Prereq: submit and mark S (met).
	prereqResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo prereq",
		Shell:     &shell,
		Container: "bare",
		Status:    "S",
		StepId:    &stepID,
	})
	require.NoError(t, err)
	prereqID := prereqResp.TaskId

	depResp, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command:   "echo dependent",
		Shell:     &shell,
		Container: "bare",
		Status:    "F",
		StepId:    &stepID,
	})
	require.NoError(t, err)
	dependentID := depResp.TaskId

	editReq := &pb.EditAndRetryTaskRequest{
		TaskId:  dependentID,
		Command: "echo dependent retry",
		Depends: &pb.Int32List{Values: []int32{prereqID}},
	}
	cloneResp, err := qc.EditAndRetryTask(ctx, editReq)
	require.NoError(t, err)
	cloneID := cloneResp.TaskId

	db, err := sql.Open("postgres", dbURLForAddr(t, serverAddr))
	require.NoError(t, err)
	defer db.Close()

	var cloneStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM task WHERE task_id = $1`, cloneID).Scan(&cloneStatus))
	require.Equal(t, "P", cloneStatus,
		"clone should stay 'P' when all overridden prereqs are 'S' — the check-and-demote must not fire spuriously")
}
