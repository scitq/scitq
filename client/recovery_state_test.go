package client

import "testing"

// Inspect strings below are verbatim shapes observed against dockerd
// 27.5.1 (created / clean exit / exited-after-daemon-crash) plus the
// running and OOM cases.
func TestFinishedExitCode(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		wantCode int32
		wantOK   bool
	}{
		{"created never started", "created|0|0001-01-01T00:00:00Z|false", -1, false},
		{"still running", "running|0|0001-01-01T00:00:00Z|false", -1, false},
		{"clean exit", "exited|0|2026-10-23T06:23:00.783731299Z|false", 0, true},
		{"failed exit", "exited|2|2026-10-23T06:23:00.783731299Z|false", 2, true},
		{"process lost under daemon crash", "exited|255|2026-10-23T06:26:41.174108762Z|false", 255, true},
		{"oom killed", "exited|0|2026-10-23T06:23:00.783731299Z|true", 137, true},
		{"exited without finish time", "exited|0|0001-01-01T00:00:00Z|false", -1, false},
		{"dead", "dead|0|2026-10-23T06:23:00.783731299Z|false", -1, false},
		{"garbage", "Error: No such object", -1, false},
		{"empty", "", -1, false},
	}
	for _, tc := range cases {
		code, ok := finishedExitCode(tc.state)
		if code != tc.wantCode || ok != tc.wantOK {
			t.Errorf("%s: finishedExitCode(%q) = (%d, %v), want (%d, %v)", tc.name, tc.state, code, ok, tc.wantCode, tc.wantOK)
		}
	}
}
