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
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	peak, _ := ReadDockerPeakMBWithDiag(containerID)
	return peak
}

// ReadDockerPeakMBWithDiag is ReadDockerPeakMB plus a per-candidate
// diagnostic summary. Used by the executor to emit a worker_event when
// the read fails, so an operator can pinpoint the layout mismatch
// (missing path? path exists but reads 0? kernel too old for
// memory.peak?) without SSH-ing to the worker.
func ReadDockerPeakMBWithDiag(containerID string) (int32, []string) {
	if containerID == "" {
		return 0, nil
	}
	// Full set of candidate paths, ordered from most-common (systemd
	// cgroup driver, v2, kernel >= 5.19) to least-common (cgroupfs
	// driver, v1). Same list the streaming Sampler uses.
	paths := candidatePaths(containerID)
	tried := make([]string, 0, len(paths))
	for _, path := range paths {
		if mb := readBytesFileAsMB(path); mb > 0 {
			return mb, nil
		}
		// Record whether the path existed at all — helps distinguish
		// "wrong layout" (no path exists) from "kernel too old for
		// memory.peak / v2 without a peak counter" (path exists but
		// reads 0 or "max").
		if _, err := os.Stat(path); err == nil {
			tried = append(tried, path+"=present-but-zero")
		} else if os.IsNotExist(err) {
			tried = append(tried, path+"=missing")
		} else {
			tried = append(tried, fmt.Sprintf("%s=%v", path, err))
		}
	}
	// One-shot log per task terminal — we don't want to spam a worker's
	// log with a stanza per task on a broken host. Truncate CID so the
	// log line stays under a terminal width.
	shortCID := containerID
	if len(shortCID) > 12 {
		shortCID = shortCID[:12]
	}
	log.Printf("ℹ️ peakmem: no cgroup peak found for cid=%s (tried: %s)",
		shortCID, strings.Join(tried, ", "))
	return 0, tried
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

// readBytesFile reads a cgroup counter file (one integer, in bytes)
// and returns the raw byte value. Same failure semantics as
// readBytesFileAsMB — 0 on any read/parse error, and on the cgroup v2
// "max" sentinel.
func readBytesFile(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// Sampler tracks the peak memory of a running docker container by
// re-reading the cgroup counter on a fixed cadence for the container's
// lifetime. This exists because a one-shot read at cmd.Wait return is
// UNRELIABLE on cgroup v2 with the systemd driver: systemd tears down
// docker-<CID>.scope as soon as the container process exits, and the
// memory.peak file is gone before the caller gets to it, even though
// `docker inspect` still returns metadata (Docker keeps its own
// container record independent of the cgroup scope). By sampling
// while the container is alive, the last observed value is a tight
// approximation of the true peak: memory.peak is monotonic, so a stale
// reading is a lower bound rather than a wrong number.
//
// Zero-cost when the container never starts (goroutine exits after
// the CID-lookup deadline). Best-effort throughout: an unreadable
// file or a torn-down scope leaves Peak() at 0, and the caller sends
// NULL up.
type Sampler struct {
	stop     chan struct{}
	done     chan struct{}
	peak     atomic.Int64 // bytes
	stopOnce sync.Once
	tried    atomic.Pointer[[]string] // last diag list, for reporter.Event
}

// NewDockerSampler starts a background goroutine that polls the peak
// memory of a docker container identified by containerName. The
// goroutine first waits for the container to be inspectable (up to
// startupDeadline) so callers can construct the sampler at the same
// time they run `docker run` without having to wait for the CID to
// become discoverable first. interval is how often to re-read the
// cgroup counter while the container is alive; 1 s is a fine default
// (memory.peak is monotonic, so a 1 s lag on the "final" reading is a
// tight bound on an hours-long workload).
func NewDockerSampler(containerName string, interval, startupDeadline time.Duration) *Sampler {
	s := &Sampler{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.run(containerName, interval, startupDeadline)
	return s
}

func (s *Sampler) run(containerName string, interval, startupDeadline time.Duration) {
	defer close(s.done)

	// Wait for the container to become inspectable, then hold onto its
	// CID for the rest of the sampler's life. The container may need a
	// moment to appear in Docker's index after `docker run` starts.
	cid := s.waitForCID(containerName, startupDeadline)
	if cid == "" {
		return
	}

	// Precompute the candidate paths once. Same set the one-shot
	// reader tried; the sampler just re-reads them on a loop.
	paths := candidatePaths(cid)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Sample once immediately so a very-short-lived container has at
	// least a chance of contributing a reading.
	s.sample(paths)
	for {
		select {
		case <-s.stop:
			// One last reading — best effort, the scope may already be
			// gone by now on a fast exit.
			s.sample(paths)
			return
		case <-ticker.C:
			s.sample(paths)
		}
	}
}

func (s *Sampler) waitForCID(containerName string, deadline time.Duration) string {
	end := time.Now().Add(deadline)
	for {
		select {
		case <-s.stop:
			return ""
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}}", containerName).Output()
		cancel()
		if err == nil {
			cid := strings.TrimSpace(string(out))
			if cid != "" {
				return cid
			}
		}
		if time.Now().After(end) {
			return ""
		}
		// Short backoff. `docker run` fills its container record before
		// the workload starts, so the first inspect after ~100 ms is
		// almost always the one that succeeds.
		time.Sleep(200 * time.Millisecond)
	}
}

// sample tries each candidate path in order and updates peak if a
// higher value is found. Also records a per-path diagnostic list so
// callers can surface it in a worker_event if peak ends at 0.
func (s *Sampler) sample(paths []string) {
	diag := make([]string, 0, len(paths))
	for _, p := range paths {
		n := readBytesFile(p)
		if n > 0 {
			// Update peak monotonically (any goroutine could race here;
			// atomic CAS keeps this correct without a mutex).
			for {
				cur := s.peak.Load()
				if n <= cur {
					break
				}
				if s.peak.CompareAndSwap(cur, n) {
					break
				}
			}
			s.tried.Store(nil)
			return
		}
		if _, err := os.Stat(p); err == nil {
			diag = append(diag, p+"=present-but-zero")
		} else if os.IsNotExist(err) {
			diag = append(diag, p+"=missing")
		} else {
			diag = append(diag, fmt.Sprintf("%s=%v", p, err))
		}
	}
	// Only overwrite the diag list when NO reader succeeded this pass.
	// A later successful pass will clear it (see the return above).
	s.tried.Store(&diag)
}

// Stop signals the sampler to stop, waits for its final sample, and
// returns the highest observed peak in MB. Safe to call more than
// once — subsequent calls return the same value without blocking.
func (s *Sampler) Stop() int32 {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
	return int32(s.peak.Load() / (1024 * 1024))
}

// Diag returns the last-known list of tried cgroup paths (with a
// present/missing/zero annotation), or nil if the sampler observed a
// good reading at least once. Intended for the caller's worker_event
// on peak==0 outcomes.
func (s *Sampler) Diag() []string {
	p := s.tried.Load()
	if p == nil {
		return nil
	}
	return *p
}

// candidatePaths returns the memory-peak-counter paths for the four
// combinations of cgroup version × cgroup driver, in
// most-common-first order. Sampling this list per tick is cheap: only
// one of them exists on a given host, so the first hit short-circuits.
func candidatePaths(containerID string) []string {
	return []string{
		"/sys/fs/cgroup/system.slice/docker-" + containerID + ".scope/memory.peak",
		"/sys/fs/cgroup/docker/" + containerID + "/memory.peak",
		"/sys/fs/cgroup/memory/docker/" + containerID + "/memory.max_usage_in_bytes",
		"/sys/fs/cgroup/memory/system.slice/docker-" + containerID + ".scope/memory.max_usage_in_bytes",
	}
}
