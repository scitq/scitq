package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/user"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// Minimal child template — one trivial step. The child workflow's tasks
// don't need to complete for the chain test; we only verify the workflow
// was created with the right template and resolved params.
const chainChildYAMLSrc = `format: 2
name: chain-child
version: "1.0.0"
description: minimal child for chain tests
language: bash
container: alpine

params:
  msg:
    type: string
    default: "default-msg"

iterate:
  name: i
  source: list
  values: ["one"]

steps:
  - name: log
    command: 'echo "child got: {params.msg}"'
`

// Parent template arming four chain entries covering most of the
// lifecycle surface: on:succeeded (fires), on:failed (skipped when parent
// succeeds), when:"false" (skipped), always_new (fresh child each time).
const chainParentYAMLSrc = `format: 2
name: chain-parent
version: "1.0.0"
description: parent that arms multiple chain entries
language: bash
container: alpine

params:
  run_qc:
    type: boolean
    default: true

iterate:
  name: i
  source: list
  values: ["one"]

steps:
  - name: noop
    command: "echo parent-noop"

chain:
  - template: chain-child
    when: "{params.run_qc}"
    params:
      msg: "from-parent-wf{parent.workflow_id}"
  - template: chain-child
    on: failed
    params:
      msg: "triage"
  - template: chain-child
    when: "false"
    params:
      msg: "should-never-fire"
  - template: chain-child
    always_new: true
    params:
      msg: "always-new"
`

// chainTestEnv bundles one bootstrapped server + uploaded templates so
// the four scenario sub-tests can share infra (the venv install is the
// expensive part — keeping it under one server saves ~30s × 3 across
// scenarios).
type chainTestEnv struct {
	ctx       context.Context
	qc        pb.TaskQueueClient
	parentTID int32
	childTID  int32
}

func setupChainEnv(t *testing.T) *chainTestEnv {
	t.Helper()
	ctx := context.Background()

	cur, err := user.Current()
	require.NoError(t, err)
	override := &config.Config{}
	override.Scitq.ScriptRunnerUser = cur.Username

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
	t.Cleanup(cleanup)

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	token := extractToken(out)

	qclient, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	t.Cleanup(func() { qclient.Close() })
	qc := qclient.Client

	uploadYAML := func(filename string, content string) int32 {
		var up *pb.UploadTemplateResponse
		require.Eventually(t, func() bool {
			var uerr error
			up, uerr = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
				Script:   []byte(content),
				Filename: strPtr(filename),
				Force:    true,
			})
			return uerr == nil && up.Success
		}, 120*time.Second, 2*time.Second, "upload "+filename+" should succeed once the venv is ready")
		require.NotNil(t, up.WorkflowTemplateId)
		return *up.WorkflowTemplateId
	}
	childTID := uploadYAML("chain_child.yaml", chainChildYAMLSrc)
	parentTID := uploadYAML("chain_parent.yaml", chainParentYAMLSrc)

	return &chainTestEnv{ctx: ctx, qc: qc, parentTID: parentTID, childTID: childTID}
}

// driveWorkflowToTerminal walks every visible task of a workflow and
// pushes it to the requested terminal status via UpdateTaskStatus. The
// server's recomputeWorkflowStatus auto-transitions the workflow row,
// which triggers the chain-completion hook.
func driveWorkflowToTerminal(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, wfID int32, newStatus string) {
	t.Helper()
	tasks := visibleTasks(t, ctx, qc, wfID)
	require.NotEmpty(t, tasks, "workflow %d has no visible tasks to drive", wfID)
	for _, tk := range tasks {
		_, err := qc.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    tk.TaskId,
			NewStatus: newStatus,
		})
		require.NoError(t, err, "UpdateTaskStatus(task=%d, %s) failed", tk.TaskId, newStatus)
	}
	require.Eventually(t, func() bool {
		wfs, e := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
		if e != nil {
			return false
		}
		for _, w := range wfs.Workflows {
			if w.WorkflowId == wfID && w.Status == newStatus {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "workflow %d did not transition to %s", wfID, newStatus)
}

func listChainFor(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, parentID int32) []*pb.ChainEntry {
	t.Helper()
	res, err := qc.ListChainEntries(ctx, &pb.ListChainEntriesRequest{ParentWorkflowId: &parentID})
	require.NoError(t, err)
	return res.Entries
}

// runChainParent submits one run of the parent template and returns the
// new parent workflow id. Uses a name-filtered before/after diff so it
// works regardless of pre-existing parent workflows.
func runChainParent(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, parentTID int32, paramsJSON string) int32 {
	t.Helper()
	nameLike := "chain-parent"
	before := map[int32]bool{}
	if wl, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike}); err == nil {
		for _, w := range wl.Workflows {
			before[w.WorkflowId] = true
		}
	}
	_, err := qc.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: parentTID,
		ParamValuesJson:    paramsJSON,
	})
	require.NoError(t, err)
	var newID int32
	require.Eventually(t, func() bool {
		wl, e := qc.ListWorkflows(ctx, &pb.WorkflowFilter{NameLike: &nameLike})
		if e != nil {
			return false
		}
		for _, w := range wl.Workflows {
			if !before[w.WorkflowId] {
				newID = w.WorkflowId
				return true
			}
		}
		return false
	}, 30*time.Second, 200*time.Millisecond, "new chain-parent workflow not visible after RunTemplate")
	return newID
}

func entryByIndex(t *testing.T, entries []*pb.ChainEntry, idx int32) *pb.ChainEntry {
	t.Helper()
	for _, e := range entries {
		if e.Idx == idx {
			return e
		}
	}
	t.Fatalf("no chain entry with idx=%d among %d entries", idx, len(entries))
	return nil
}

func waitForEntryStatus(t *testing.T, ctx context.Context, qc pb.TaskQueueClient, entryID int32, wanted string, timeout time.Duration) *pb.ChainEntry {
	t.Helper()
	var last *pb.ChainEntry
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e, err := qc.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: entryID})
		if err == nil {
			last = e
			if e.Status == wanted {
				return last
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	errMsg := ""
	statusGot := "?"
	if last != nil {
		statusGot = last.Status
		if last.ErrorMessage != nil {
			errMsg = *last.ErrorMessage
		}
	}
	t.Fatalf("chain entry %d did not reach %q within %s — last status=%q error=%q",
		entryID, wanted, timeout, statusGot, errMsg)
	return nil
}

// TestChainLifecycleE2E exercises the full chain lifecycle end-to-end
// against the real server + Python YAML runner. One server, one set of
// uploaded templates, four scenarios run sequentially:
//
//   1. Success path — on:succeeded fires, on:failed/when:false skip,
//      always_new fires a fresh child, fired children carry the
//      resolved {parent.workflow_id} substitution and the parent_workflow_id
//      backlink.
//   2. Failure path — on:failed fires, the on:succeeded entries skip.
//   3. Operator lifecycle — suspend before completion holds the entry
//      through the hook; edit-while-suspended updates the params; cancel
//      from suspended is terminal; resume after parent terminated fires
//      immediately with the edited params; cancel and edit reject a fired
//      entry with the expected error shape.
//   4. Bad template — a chain entry pointing at a missing template
//      transitions to `failed` with error_message, other entries
//      unaffected.
func TestChainLifecycleE2E(t *testing.T) {
	t.Parallel()
	env := setupChainEnv(t)
	ctx, qc := env.ctx, env.qc

	// --------------------------------------------------------------
	// Scenario 1 — success path
	// --------------------------------------------------------------
	t.Run("success_path", func(t *testing.T) {
		parentID := runChainParent(t, ctx, qc, env.parentTID, `{}`)

		entries := listChainFor(t, ctx, qc, parentID)
		require.Len(t, entries, 4, "all four chain entries materialized at parent submit")
		for _, e := range entries {
			require.Equal(t, "pending", e.Status, "entry idx=%d should start pending", e.Idx)
			require.Equal(t, "chain-child", e.TemplateName)
			require.Nil(t, e.ChildWorkflowId)
		}

		driveWorkflowToTerminal(t, ctx, qc, parentID, "S")

		// idx 0: on:succeeded + when:true → fired.
		e0 := entryByIndex(t, entries, 0)
		waitForEntryStatus(t, ctx, qc, e0.ChainEntryId, "fired", 30*time.Second)
		e0 = entryByIndex(t, listChainFor(t, ctx, qc, parentID), 0)
		require.NotNil(t, e0.ChildWorkflowId)
		require.NotNil(t, e0.FiredAt)
		// idx 1: on:failed + parent succeeded → skipped.
		e1 := entryByIndex(t, entries, 1)
		waitForEntryStatus(t, ctx, qc, e1.ChainEntryId, "skipped", 10*time.Second)
		// idx 2: when:"false" → skipped.
		e2 := entryByIndex(t, entries, 2)
		waitForEntryStatus(t, ctx, qc, e2.ChainEntryId, "skipped", 10*time.Second)
		// idx 3: always_new → fired with a separate child.
		e3 := entryByIndex(t, entries, 3)
		waitForEntryStatus(t, ctx, qc, e3.ChainEntryId, "fired", 30*time.Second)
		e3 = entryByIndex(t, listChainFor(t, ctx, qc, parentID), 3)
		require.NotNil(t, e3.ChildWorkflowId)
		require.NotEqual(t, *e0.ChildWorkflowId, *e3.ChildWorkflowId,
			"always_new and the default entry should land on distinct children")

		// Verify fired children look right.
		verifyChild := func(childID int32, wantMsgSubstr string) {
			wfs, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
			require.NoError(t, err)
			var found *pb.Workflow
			for _, w := range wfs.Workflows {
				if w.WorkflowId == childID {
					found = w
					break
				}
			}
			require.NotNil(t, found, "child workflow %d should exist", childID)
			require.NotNil(t, found.ParentWorkflowId, "child should record parent_workflow_id")
			require.Equal(t, parentID, *found.ParentWorkflowId)
			require.NotNil(t, found.TemplateRunId)
			trs, err := qc.ListTemplateRuns(ctx, &pb.TemplateRunFilter{TemplateRunId: found.TemplateRunId})
			require.NoError(t, err)
			require.Len(t, trs.Runs, 1)
			require.Contains(t, trs.Runs[0].ParamValuesJson, wantMsgSubstr,
				"child params should contain resolved %q", wantMsgSubstr)
		}
		verifyChild(*e0.ChildWorkflowId, fmt.Sprintf("from-parent-wf%d", parentID))
		verifyChild(*e3.ChildWorkflowId, "always-new")
	})

	// --------------------------------------------------------------
	// Scenario 2 — failure path
	// --------------------------------------------------------------
	t.Run("failure_path", func(t *testing.T) {
		parentID := runChainParent(t, ctx, qc, env.parentTID, `{}`)

		driveWorkflowToTerminal(t, ctx, qc, parentID, "F")

		entries := listChainFor(t, ctx, qc, parentID)
		// idx 1 (on:failed) fires.
		e1 := entryByIndex(t, entries, 1)
		waitForEntryStatus(t, ctx, qc, e1.ChainEntryId, "fired", 30*time.Second)
		e1 = entryByIndex(t, listChainFor(t, ctx, qc, parentID), 1)
		require.NotNil(t, e1.ChildWorkflowId)
		// idx 0 / idx 3 (on:succeeded) skip.
		waitForEntryStatus(t, ctx, qc, entryByIndex(t, entries, 0).ChainEntryId, "skipped", 10*time.Second)
		waitForEntryStatus(t, ctx, qc, entryByIndex(t, entries, 3).ChainEntryId, "skipped", 10*time.Second)
	})

	// --------------------------------------------------------------
	// Scenario 3 — operator lifecycle (suspend/edit/cancel/resume)
	// --------------------------------------------------------------
	t.Run("operator_lifecycle", func(t *testing.T) {
		parentID := runChainParent(t, ctx, qc, env.parentTID, `{}`)
		entries := listChainFor(t, ctx, qc, parentID)
		require.Len(t, entries, 4)
		e0 := entryByIndex(t, entries, 0)
		e3 := entryByIndex(t, entries, 3)

		// Suspend e0 before completion.
		suspended, err := qc.SuspendChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e0.ChainEntryId})
		require.NoError(t, err)
		require.Equal(t, "suspended", suspended.Status)

		// Edit suspended e0 — change the params the eventual child will see.
		// Compare on parsed JSON: Postgres jsonb round-trips reformat whitespace.
		newParams := `{"msg":"edited-before-fire"}`
		edited, err := qc.EditChainEntry(ctx, &pb.EditChainEntryRequest{
			ChainEntryId:       e0.ChainEntryId,
			ParamsTemplateJson: &newParams,
		})
		require.NoError(t, err)
		var wantParams, gotParams map[string]any
		require.NoError(t, json.Unmarshal([]byte(newParams), &wantParams))
		require.NoError(t, json.Unmarshal([]byte(edited.ParamsTemplateJson), &gotParams))
		require.Equal(t, wantParams, gotParams, "edited params should round-trip cleanly")

		// Cancel e3 — must not fire.
		cancelled, err := qc.CancelChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e3.ChainEntryId})
		require.NoError(t, err)
		require.Equal(t, "cancelled", cancelled.Status)

		driveWorkflowToTerminal(t, ctx, qc, parentID, "S")

		// e0 stays suspended, e3 stays cancelled.
		check0, err := qc.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e0.ChainEntryId})
		require.NoError(t, err)
		require.Equal(t, "suspended", check0.Status)
		require.Nil(t, check0.ChildWorkflowId)
		check3, err := qc.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e3.ChainEntryId})
		require.NoError(t, err)
		require.Equal(t, "cancelled", check3.Status)
		require.Nil(t, check3.ChildWorkflowId)

		// Resume e0 after parent terminated → fires immediately with edited params.
		_, err = qc.ResumeChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e0.ChainEntryId})
		require.NoError(t, err)
		fired := waitForEntryStatus(t, ctx, qc, e0.ChainEntryId, "fired", 30*time.Second)
		require.NotNil(t, fired.ChildWorkflowId)

		// Fired child carries the edited params.
		wfs, err := qc.ListWorkflows(ctx, &pb.WorkflowFilter{})
		require.NoError(t, err)
		var childWF *pb.Workflow
		for _, w := range wfs.Workflows {
			if w.WorkflowId == *fired.ChildWorkflowId {
				childWF = w
				break
			}
		}
		require.NotNil(t, childWF)
		require.NotNil(t, childWF.TemplateRunId)
		trs, err := qc.ListTemplateRuns(ctx, &pb.TemplateRunFilter{TemplateRunId: childWF.TemplateRunId})
		require.NoError(t, err)
		require.Len(t, trs.Runs, 1)
		require.Contains(t, trs.Runs[0].ParamValuesJson, "edited-before-fire",
			"edit-while-suspended must reach the fired child")

		// Cancel a fired entry → error.
		_, err = qc.CancelChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: e0.ChainEntryId})
		require.Error(t, err, "cancel must reject a fired entry")

		// Edit a fired entry → error mentioning the suspended-only rule.
		tn := "should-not-stick"
		_, err = qc.EditChainEntry(ctx, &pb.EditChainEntryRequest{
			ChainEntryId: e0.ChainEntryId,
			TemplateName: &tn,
		})
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "suspended"),
			"edit error should reference the suspended-only rule, got: %v", err)
	})

	// --------------------------------------------------------------
	// Scenario 4 — bad template reference
	// --------------------------------------------------------------
	t.Run("bad_template_ref", func(t *testing.T) {
		parentID := runChainParent(t, ctx, qc, env.parentTID, `{}`)

		// Append a bad-template entry via CreateChainEntries — avoids
		// shipping a second parent template just for this case.
		draftJSON, err := json.Marshal(map[string]string{"msg": "noop"})
		require.NoError(t, err)
		_, err = qc.CreateChainEntries(ctx, &pb.CreateChainEntriesRequest{
			WorkflowId: parentID,
			Entries: []*pb.ChainEntryDraft{{
				TemplateName:       "this-template-does-not-exist",
				ParamsTemplateJson: string(draftJSON),
				WhenExpr:           "true",
				OnStatus:           "succeeded",
			}},
		})
		require.NoError(t, err)

		driveWorkflowToTerminal(t, ctx, qc, parentID, "S")

		entries := listChainFor(t, ctx, qc, parentID)
		require.Len(t, entries, 5, "1 added entry on top of the 4 from the YAML")

		bad := entryByIndex(t, entries, 4)
		final := waitForEntryStatus(t, ctx, qc, bad.ChainEntryId, "failed", 30*time.Second)
		require.NotNil(t, final.ErrorMessage)
		require.True(t,
			strings.Contains(*final.ErrorMessage, "this-template-does-not-exist") ||
				strings.Contains(*final.ErrorMessage, "not found"),
			"error_message should reference the missing template, got: %s", *final.ErrorMessage)
		// Other entries unaffected — the on:succeeded ones still fired.
		waitForEntryStatus(t, ctx, qc, entryByIndex(t, entries, 0).ChainEntryId, "fired", 30*time.Second)
	})
}
