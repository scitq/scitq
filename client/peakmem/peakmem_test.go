package peakmem

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The container/bare readers hit real filesystem paths that don't
// exist in a unit-test sandbox. We can't fake `/sys/fs/cgroup/…`
// (root-owned), so this file exercises the parser primitives with
// fixture files under t.TempDir() and hits the real readers with
// known-bad inputs to lock in their zero-on-failure contract.

// TestReadProcessPeakMB_Pid0IsZero: pid 0 is the sentinel for
// "unknown / process gone". Must return 0 without touching /proc.
func TestReadProcessPeakMB_Pid0IsZero(t *testing.T) {
	if v := ReadProcessPeakMB(0); v != 0 {
		t.Fatalf("pid 0 should return 0, got %d", v)
	}
	if v := ReadProcessPeakMB(-1); v != 0 {
		t.Fatalf("negative pid should return 0, got %d", v)
	}
}

// TestReadProcessPeakMB_SelfReturnsPositive: reading our own /proc
// entry should yield a positive number of MB. Not a fixed value —
// depends on the test runner's own resident-set — but the go test
// binary is meaningfully bigger than a rounding error.
func TestReadProcessPeakMB_SelfReturnsPositive(t *testing.T) {
	self := os.Getpid()
	got := ReadProcessPeakMB(self)
	if got <= 0 {
		// On darwin there is no /proc/<pid>/status; skip rather than
		// fail so the test suite stays runnable off-Linux.
		t.Skipf("ReadProcessPeakMB(self)=%d — /proc/<pid>/status not available on this platform?", got)
	}
	// Sanity: at least 1 MB (the go test binary + runtime + this test
	// harness), at most a comfortable ceiling — 10 GB is way beyond
	// what any test harness needs.
	if got > 10*1024 {
		t.Errorf("suspiciously large VmHWM: %d MB", got)
	}
}

// TestReadDockerPeakMB_UnknownCidIsZero: readers must return 0 on
// missing cgroup files rather than panicking or returning garbage.
// This is the "not observed" contract downstream code relies on.
func TestReadDockerPeakMB_UnknownCidIsZero(t *testing.T) {
	// A 64-char nonsense CID that no docker install would ever create.
	fakeCid := "0000000000000000000000000000000000000000000000000000000000000000"
	if v := ReadDockerPeakMB(fakeCid); v != 0 {
		t.Fatalf("unknown CID must return 0, got %d", v)
	}
	if v := ReadDockerPeakMB(""); v != 0 {
		t.Fatalf("empty CID must return 0, got %d", v)
	}
}

// TestReadBytesFileAsMB_ParsesInteger: the cgroup counter files hold
// one decimal integer (bytes). Feed a temp file with a known value
// and verify the MB conversion.
func TestReadBytesFileAsMB_ParsesInteger(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "memory.peak")
	// 1234 MB = 1_293_942_784 bytes; exact so the // 1024^2 division
	// lands on 1234 with no rounding surprise.
	const wantMB = 1234
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%d\n", wantMB*1024*1024)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if got := readBytesFileAsMB(p); got != wantMB {
		t.Fatalf("got %d MB, want %d", got, wantMB)
	}
}

// TestReadBytesFileAsMB_MissingIsZero: cgroup v2 exposes `max` for
// unlimited values which parses as non-numeric; and the file may be
// missing on unsupported kernels. Both cases must return 0.
func TestReadBytesFileAsMB_MissingIsZero(t *testing.T) {
	if got := readBytesFileAsMB("/nonexistent/memory.peak"); got != 0 {
		t.Errorf("missing file should return 0, got %d", got)
	}
	// "max" sentinel — cgroup v2 uses this for unlimited limits.
	dir := t.TempDir()
	p := filepath.Join(dir, "memory.max")
	_ = os.WriteFile(p, []byte("max\n"), 0o644)
	if got := readBytesFileAsMB(p); got != 0 {
		t.Errorf("non-numeric content should return 0, got %d", got)
	}
}

// TestReadBytesFileAsMB_ZeroBytesIsZero: a container that literally
// never allocated anything reads 0 bytes. We treat that as "not
// observed" to match the general "0 = NULL server-side" contract.
// This does mean a rounding down to zero (< 1 MB used) also reads
// as unobserved — acceptable for the "which task hit N GB" question.
func TestReadBytesFileAsMB_ZeroBytesIsZero(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "memory.peak")
	_ = os.WriteFile(p, []byte("0\n"), 0o644)
	if got := readBytesFileAsMB(p); got != 0 {
		t.Errorf("0-byte reading should map to 0 MB, got %d", got)
	}
}
