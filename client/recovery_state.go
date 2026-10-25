package client

import (
	"strconv"
	"strings"
)

// containerStateFormat is the `docker inspect -f` template whose output
// parseContainerState reads: status|exit code|finished-at|oom-killed.
const containerStateFormat = "{{.State.Status}}|{{.State.ExitCode}}|{{.State.FinishedAt}}|{{.State.OOMKilled}}"

// containerEnd is how a stopped task container ended.
type containerEnd struct {
	exitCode  int32
	finished  bool // the container genuinely ran to an end
	oomKilled bool // the kernel's OOM killer killed a process in it
}

// parseContainerState reads `docker inspect -f containerStateFormat` output.
// finished is true only for status "exited" with a real FinishedAt
// timestamp; exitCode is meaningful only then.
//
// Reading .State.ExitCode alone is a trap: Docker reports ExitCode 0 for a
// container that never ran to completion — a "created" container that was
// never started carries ExitCode 0 and FinishedAt 0001-01-01T00:00:00Z,
// indistinguishable from a clean exit by the code alone.
//
// Exit code 137 is not an OOM signal either: it is any SIGKILL, including a
// container killed by a Docker daemon shutdown or an operator. OOMKilled is
// the kernel's own verdict; it is set whether the limit hit was the
// container's or one above it (a worker with no container memory limit).
func parseContainerState(state string) containerEnd {
	parts := strings.Split(strings.TrimSpace(state), "|")
	if len(parts) != 4 {
		return containerEnd{exitCode: -1}
	}
	end := containerEnd{exitCode: -1, oomKilled: parts[3] == "true"}
	if parts[0] != "exited" || strings.HasPrefix(parts[2], "0001-01-01") {
		return end
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return end
	}
	end.exitCode, end.finished = int32(code), true
	return end
}
