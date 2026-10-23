package client

import (
	"strconv"
	"strings"
)

// finishedExitFormat is the `docker inspect -f` template whose output
// finishedExitCode parses: status|exit code|finished-at|oom-killed.
const finishedExitFormat = "{{.State.Status}}|{{.State.ExitCode}}|{{.State.FinishedAt}}|{{.State.OOMKilled}}"

// finishedExitCode reports the exit code of a container that genuinely
// finished, from `docker inspect -f finishedExitFormat` output. ok is
// true only for status "exited" with a real FinishedAt timestamp.
//
// Reading .State.ExitCode alone is a trap: Docker reports ExitCode 0 for
// a container that never ran to completion — a "created" container that
// was never started carries ExitCode 0 and FinishedAt
// 0001-01-01T00:00:00Z, indistinguishable from a clean exit by the code
// alone. An OOM-killed container is reported as 137.
func finishedExitCode(state string) (int32, bool) {
	parts := strings.Split(strings.TrimSpace(state), "|")
	if len(parts) != 4 || parts[0] != "exited" || strings.HasPrefix(parts[2], "0001-01-01") {
		return -1, false
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return -1, false
	}
	if parts[3] == "true" {
		return 137, true
	}
	return int32(code), true
}
