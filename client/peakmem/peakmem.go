// Package peakmem reads the kernel-tracked peak resident memory of a
// finished task. Two entry points cover the two ways scitq runs a
// command:
//
//   - Docker task: the container's cgroup carries a `memory.peak`
//     file (cgroup v2, kernel 5.19+) or `memory.max_usage_in_bytes`
//     (v1). ReadDockerPeakMB tries both, in order, and returns 0 on
//     failure.
//
//   - Bare task: /proc/<pid>/status carries a `VmHWM` field with the
//     process's high-water resident-set size. ReadProcessPeakMB parses
//     that and returns 0 on any read/parse error.
//
// Both readers are best-effort by design: peak_mem_mb ends up in the
// task row as NULL when we can't read it, which the MCP surface treats
// as "not observed" rather than "zero used". There's no per-second
// sampling anywhere in this package — the kernel has been tracking
// the peak all along; we read the counter once, at task terminal.
package peakmem

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ReadDockerPeakMB reads the peak memory a docker container used, in
// MB (integer, rounded down). Tries the cgroup v2 file first, then the
// v1 fallback; both silently return 0 on any read error so a caller
// can always ship the returned value up without a nil check.
//
// containerID is the full 64-char id, not the truncated "docker ps"
// display id. Callers typically get it from `docker inspect` or the
// output of `docker run --cidfile <path>`.
func ReadDockerPeakMB(containerID string) int32 {
	if containerID == "" {
		return 0
	}
	// cgroup v2 (systemd cgroup driver — the modern default). Two
	// path shapes cover the systemd and cgroupfs drivers; we try
	// both.
	for _, path := range []string{
		"/sys/fs/cgroup/system.slice/docker-" + containerID + ".scope/memory.peak",
		"/sys/fs/cgroup/docker/" + containerID + "/memory.peak",
	} {
		if mb := readBytesFileAsMB(path); mb > 0 {
			return mb
		}
	}
	// cgroup v1 fallback.
	for _, path := range []string{
		"/sys/fs/cgroup/memory/docker/" + containerID + "/memory.max_usage_in_bytes",
		"/sys/fs/cgroup/memory/system.slice/docker-" + containerID + ".scope/memory.max_usage_in_bytes",
	} {
		if mb := readBytesFileAsMB(path); mb > 0 {
			return mb
		}
	}
	return 0
}

// ReadProcessPeakMB parses /proc/<pid>/status for VmHWM (peak resident
// memory). Returns 0 on any read/parse failure, or if the process has
// already been reaped and its /proc entry is gone.
//
// VmHWM is reported in kilobytes ("VmHWM:  12345 kB"); we convert to
// MB (integer, rounded down) so the value fits an int32 comfortably
// even for terabyte-class processes.
func ReadProcessPeakMB(pid int) int32 {
	if pid <= 0 {
		return 0
	}
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		// "VmHWM:\t  12345 kB"
		parts := strings.Fields(line)
		// parts[0] = "VmHWM:", parts[1] = <kb>, parts[2] = "kB"
		if len(parts) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0
		}
		return int32(kb / 1024)
	}
	return 0
}

// readBytesFileAsMB reads a cgroup counter file (one integer, in
// bytes) and returns the value in MB. Files that don't exist or that
// carry a non-numeric value (e.g. the cgroup v2 "max" sentinel when
// no limit is set) return 0. Ignoring these silently is correct:
// peak_mem_mb is best-effort, and the alternative — a caller-side nil
// check on every reader path — is worse ergonomics for the same
// observable outcome.
func readBytesFileAsMB(path string) int32 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return int32(n / (1024 * 1024))
}
