package client

import "testing"

// Inspect strings below are verbatim shapes observed against dockerd
// 27.5.1 (created / clean exit / exited-after-daemon-crash) and Docker
// Desktop (OOM kill under a memory limit above the container; SIGKILL from
// outside, which also exits 137 but is not an OOM).
func TestParseContainerState(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  containerEnd
	}{
		{"created never started", "created|0|0001-01-01T00:00:00Z|false", containerEnd{-1, false, false}},
		{"still running", "running|0|0001-01-01T00:00:00Z|false", containerEnd{-1, false, false}},
		{"clean exit", "exited|0|2026-10-23T06:23:00.783731299Z|false", containerEnd{0, true, false}},
		{"failed exit", "exited|2|2026-10-23T06:23:00.783731299Z|false", containerEnd{2, true, false}},
		{"process lost under daemon crash", "exited|255|2026-10-23T06:26:41.174108762Z|false", containerEnd{255, true, false}},
		{"oom killed", "exited|137|2026-10-25T18:40:12.1Z|true", containerEnd{137, true, true}},
		{"sigkill from outside is not oom", "exited|137|2026-10-25T18:40:14.2Z|false", containerEnd{137, true, false}},
		{"exited without finish time", "exited|0|0001-01-01T00:00:00Z|false", containerEnd{-1, false, false}},
		{"dead", "dead|0|2026-10-23T06:23:00.783731299Z|false", containerEnd{-1, false, false}},
		{"garbage", "Error: No such object", containerEnd{-1, false, false}},
		{"empty", "", containerEnd{-1, false, false}},
	}
	for _, tc := range cases {
		if got := parseContainerState(tc.state); got != tc.want {
			t.Errorf("%s: parseContainerState(%q) = %+v, want %+v", tc.name, tc.state, got, tc.want)
		}
	}
}
