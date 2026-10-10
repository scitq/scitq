package client

import (
	"regexp"
	"sync"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// stderrTailBuf keeps the LAST capacity bytes of stderr seen on the
// task's log stream. The definitive-failure classifier reads this
// tail against task.DefinitivePattern — a regex matched here instead
// of on every incoming line avoids pattern compilation per line and
// bounds memory per task.
//
// Writer is one goroutine (sendLogs for stderr); reader is one
// goroutine (classifyDefinitive, which runs after sendLogs has
// returned via logWg.Wait). A mutex covers the overlap: the writer
// is draining when the reader is about to look, so without the lock
// we'd race on the capacity-bounded slice.
type stderrTailBuf struct {
	mu       sync.Mutex
	capacity int
	buf      []byte
}

func newStderrTail(capacity int) *stderrTailBuf {
	return &stderrTailBuf{
		capacity: capacity,
		buf:      make([]byte, 0, capacity),
	}
}

// appendLine adds one scanner.Text() output + a newline, keeping only
// the last `capacity` bytes. Called once per stderr line.
func (t *stderrTailBuf) appendLine(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Append the new line + '\n'. If the total exceeds capacity, drop
	// the oldest bytes so only the tail survives.
	t.buf = append(t.buf, line...)
	t.buf = append(t.buf, '\n')
	if len(t.buf) > t.capacity {
		drop := len(t.buf) - t.capacity
		t.buf = t.buf[drop:]
	}
}

// snapshot returns a copy of the current tail — a copy so the caller
// can run regex matching without the mutex, and so a late sendLogs
// write doesn't race the regex scan.
func (t *stderrTailBuf) snapshot() []byte {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]byte, len(t.buf))
	copy(out, t.buf)
	return out
}

// extractExitCode pulls an integer exit code out of the task's error
// signals, trying three sources in order:
//  1. hooks.capturedExit if set (recoverTask's docker-inspect fallback)
//  2. *exec.ExitError via err.ExitCode() (the normal cmd.Wait path)
//  3. the "container exited with code N" string we produce ourselves
//     when overriding a kill-status
// Returns -1 when no exit code could be recovered (e.g. network
// failure before launch, context cancellation).
func extractExitCode(err error, hooks *taskHooks) int {
	if hooks != nil {
		if c := hooks.capturedExit.Load(); c >= 0 {
			return int(c)
		}
	}
	if err == nil {
		return -1
	}
	// Try *exec.ExitError.ExitCode(). errors.As walks the chain so a
	// wrapped ExitError still surfaces.
	type exitCoder interface {
		ExitCode() int
	}
	if ec, ok := err.(exitCoder); ok {
		return ec.ExitCode()
	}
	// Fallback: parse "container exited with code N" from our own
	// override string. Regex rather than Sscanf to be forgiving about
	// surrounding text.
	m := codeFromMsgRe.FindStringSubmatch(err.Error())
	if len(m) == 2 {
		var n int
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	return -1
}

var codeFromMsgRe = regexp.MustCompile(`container exited with code (\d+)`)

// classifyDefinitive returns "definitive" when the task's exit code
// matches one of task.DefinitiveExitCodes, OR when the task's
// stderr tail matches task.DefinitivePattern. Returns "" (empty)
// when neither signal fires, letting the caller fall through to the
// generic exec-failure classifier (oom / timeout / other).
//
// Rationale is the two-class task-failure model: retrying a
// definitive failure (input corrupt, unsupported option, missing
// reference) wastes cluster time. Retrying a transient failure
// (OOM at this attempt's resources, network blip, timeout) can
// succeed. The policy is set per-step via task_spec, so the step
// author declares what their tool means by its exit codes / error
// phrases — scitq doesn't guess.
func classifyDefinitive(err error, hooks *taskHooks, task *pb.Task, tail *stderrTailBuf) string {
	if err == nil || task == nil {
		return ""
	}
	// Exit-code match.
	if len(task.DefinitiveExitCodes) > 0 {
		code := extractExitCode(err, hooks)
		if code >= 0 {
			for _, want := range task.DefinitiveExitCodes {
				if code == int(want) {
					return "definitive"
				}
			}
		}
	}
	// stderr-tail regex match. Pattern compilation errors are logged
	// via the caller's path; here we silently fall through so a bad
	// regex can't crash the worker. Multiline mode is enabled by
	// default: operators write `^FATAL:` meaning "any line starting
	// with FATAL:", not "the stderr tail starts with FATAL:" — the
	// latter is useless because stderr is a stream of lines. (?m)
	// scopes to ^ and $ semantics, nothing else changes.
	if task.DefinitivePattern != nil && *task.DefinitivePattern != "" && tail != nil {
		re, reErr := regexp.Compile("(?m)" + *task.DefinitivePattern)
		if reErr == nil && re.Match(tail.snapshot()) {
			return "definitive"
		}
	}
	return ""
}
