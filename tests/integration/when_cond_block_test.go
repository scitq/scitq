package integration_test

import (
	"context"
	"os/user"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
)

// whenCondBlockYAMLSrc exercises the regression `when:` cond-block path.
// During F' the `when:` handler narrowed to a str-only fast path and silently
// passed dicts through unchanged — making every dict-shaped `when:` always
// fire (a non-empty dict is truthy), regardless of which branch the cond was
// supposed to pick. The bug went unnoticed in unit tests because they
// exercised string `when:` only.
//
// The template below defines two steps gated by `when:` cond blocks
// dispatching on `params.mode`:
//   - `igc2_only`: `true` when mode is "igc2" or "both", `false` for "oral"
//   - `oral_only`: `true` only when mode is "both"
//
// We then run the template twice with different param values and check the
// resulting task topology.
const whenCondBlockYAMLSrc = `format: 2
name: when-cond-block
version: "1.0.0"
description: dict-shaped when must dispatch via _resolve_cond
language: bash
container: alpine

params:
  mode:
    type: enum
    choices: ["igc2", "oral", "both"]
    default: "igc2"

steps:
  - name: always_runs
    command: 'echo always'
  - name: igc2_only
    when:
      cond: "{params.mode}"
      igc2: "true"
      oral: "false"
      both: "true"
    command: 'echo igc2_only'
  - name: oral_only
    when:
      cond: "{params.mode}"
      igc2: "false"
      oral: "false"
      both: "true"
    command: 'echo oral_only'
`

// TestWhenCondBlockDispatches verifies that a dict-shaped `when:` block on a
// step actually picks the correct branch and skips the step when the chosen
// branch resolves to a falsy string.
//
// Before the fix, all three runs would have produced the same three steps
// (always_runs, igc2_only, oral_only) because the cond dict was returned as-is
// and any non-empty dict is truthy.
func TestWhenCondBlockDispatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cur, err := user.Current()
	require.NoError(t, err)
	override := &config.Config{}
	override.Scitq.ScriptRunnerUser = cur.Username

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, override)
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

	// Upload the template once the venv is ready. Log the failure
	// reason on every loss so a timeout actually carries diagnostics.
	var up *pb.UploadTemplateResponse
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		var uerr error
		up, uerr = qc.UploadTemplate(ctx, &pb.UploadTemplateRequest{
			Script:   []byte(whenCondBlockYAMLSrc),
			Filename: strPtr("when_cond.yaml"),
			Force:    true,
		})
		if uerr == nil && up.Success {
			break
		}
		if uerr != nil {
			t.Logf("UploadTemplate transport error: %v", uerr)
		} else {
			t.Logf("UploadTemplate reported failure: %s", up.Message)
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, up, "UploadTemplate never returned a response")
	require.True(t, up.Success, "template upload never succeeded: %s", up.Message)
	require.NotNil(t, up.WorkflowTemplateId)
	tmplID := *up.WorkflowTemplateId

	type modeCase struct {
		mode     string
		expected []string // step names that should produce tasks
		excluded []string // step names that must NOT produce tasks
	}
	cases := []modeCase{
		{
			mode:     "igc2",
			expected: []string{"always_runs", "igc2_only"},
			excluded: []string{"oral_only"},
		},
		{
			mode:     "oral",
			expected: []string{"always_runs"},
			excluded: []string{"igc2_only", "oral_only"},
		},
		{
			mode:     "both",
			expected: []string{"always_runs", "igc2_only", "oral_only"},
			excluded: []string{}, // every step fires
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.mode, func(t *testing.T) {
			paramsJSON := `{"mode":"` + tc.mode + `"}`
			tr, err := qc.RunTemplate(ctx, &pb.RunTemplateRequest{
				WorkflowTemplateId: tmplID,
				ParamValuesJson:    paramsJSON,
			})
			require.NoError(t, err)
			require.NotNil(t, tr.WorkflowId, "RunTemplate must return the workflow id")
			wfID := *tr.WorkflowId

			tasks := visibleTasks(t, ctx, qc, wfID)
			names := taskNames(tasks)

			for _, name := range tc.expected {
				require.Contains(t, tasks, name,
					"mode=%s: expected step %q to fire (got: %v)", tc.mode, name, names)
			}
			for _, name := range tc.excluded {
				require.NotContains(t, tasks, name,
					"mode=%s: step %q must NOT fire — when: cond branch resolved to 'false'; this is the F' regression where dict-shaped when: was silently always-truthy",
					tc.mode, name)
			}
		})
	}
}
