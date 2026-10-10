package client

import (
	"errors"
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// TestClassifyDefinitive_NoPolicyNoError returns empty, letting the
// caller fall through to the generic exec-failure classifier.
func TestClassifyDefinitive_NoPolicyNoError(t *testing.T) {
	task := &pb.Task{}
	if got := classifyDefinitive(nil, nil, task, nil); got != "" {
		t.Fatalf("no error should return empty, got %q", got)
	}
	if got := classifyDefinitive(errors.New("boom"), nil, task, nil); got != "" {
		t.Fatalf("no policy should return empty, got %q", got)
	}
}

// TestClassifyDefinitive_ExitCodeMatch: a task that exits with a code
// listed in definitive_exit_codes is classified definitive. Exit code
// extracted from the "container exited with code N" message that the
// executor produces when overriding a kill-status.
func TestClassifyDefinitive_ExitCodeMatch(t *testing.T) {
	task := &pb.Task{DefinitiveExitCodes: []int32{2, 42}}
	err := errors.New("container exited with code 2")
	if got := classifyDefinitive(err, nil, task, nil); got != "definitive" {
		t.Fatalf("exit code 2 in list [2,42] should be definitive, got %q", got)
	}
}

// TestClassifyDefinitive_ExitCodeMismatch: an exit code not in the
// list falls through — the caller runs the generic classifier.
func TestClassifyDefinitive_ExitCodeMismatch(t *testing.T) {
	task := &pb.Task{DefinitiveExitCodes: []int32{2, 42}}
	err := errors.New("container exited with code 1")
	if got := classifyDefinitive(err, nil, task, nil); got != "" {
		t.Fatalf("exit code 1 not in list [2,42] should fall through, got %q", got)
	}
}

// TestClassifyDefinitive_PatternMatch: when the stderr tail matches
// the step's regex, the failure is definitive regardless of exit code.
func TestClassifyDefinitive_PatternMatch(t *testing.T) {
	pattern := "^SCITQ_DEFINITIVE:"
	task := &pb.Task{DefinitivePattern: &pattern}
	tail := newStderrTail(4096)
	tail.appendLine("some ordinary output")
	tail.appendLine("SCITQ_DEFINITIVE: missing reference release232")
	err := errors.New("exit status 1")
	if got := classifyDefinitive(err, nil, task, tail); got != "definitive" {
		t.Fatalf("pattern match should return definitive, got %q", got)
	}
}

// TestClassifyDefinitive_PatternMismatch: no match in the tail → empty.
func TestClassifyDefinitive_PatternMismatch(t *testing.T) {
	pattern := "^SCITQ_DEFINITIVE:"
	task := &pb.Task{DefinitivePattern: &pattern}
	tail := newStderrTail(4096)
	tail.appendLine("ordinary error message")
	err := errors.New("exit status 1")
	if got := classifyDefinitive(err, nil, task, tail); got != "" {
		t.Fatalf("no pattern match should fall through, got %q", got)
	}
}

// TestStderrTail_WrapsAtCapacity: writing more than capacity bytes
// keeps only the tail. This matters because long-running tools can
// stream MBs of stderr; the classifier only needs the last few KB.
func TestStderrTail_WrapsAtCapacity(t *testing.T) {
	tail := newStderrTail(32)
	for i := 0; i < 100; i++ {
		tail.appendLine("0123456789") // 11 bytes each with newline
	}
	snap := tail.snapshot()
	if len(snap) > 32 {
		t.Fatalf("tail exceeded capacity: len=%d", len(snap))
	}
}

// TestStderrTail_PreservesLastLines: after capacity wraps, the most
// recent lines must still be present — the whole point.
func TestStderrTail_PreservesLastLines(t *testing.T) {
	tail := newStderrTail(64)
	for i := 0; i < 20; i++ {
		tail.appendLine("filler-line")
	}
	tail.appendLine("MARKER_LAST_LINE")
	snap := string(tail.snapshot())
	if !contains(snap, "MARKER_LAST_LINE") {
		t.Fatalf("tail lost the most recent marker line; snap=%q", snap)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
