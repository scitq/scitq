package server

// Test seams for behaviour the production code couldn't otherwise be
// exercised against without terminating the test binary or polluting
// shared output. These are exported so integration tests in
// `tests/integration` (a separate package) can override them. They are
// not part of the supported runtime API; the rest of the codebase
// should never read them.

import "io"

// GateExit returns the current exit hook used by the SIGUSR1 graceful
// drain. Tests use this to swap in a recorder, then restore on cleanup.
func GateExit() func(int) { return gateExit }

// SetGateExit replaces the SIGUSR1 graceful-drain exit hook. Default is
// `os.Exit`. Tests must restore the previous value via t.Cleanup().
func SetGateExit(f func(int)) { gateExit = f }

// GateStdout / SetGateStdout: same pattern for the stdout writer the
// gate uses to emit its drained-line JSON. Tests substitute a buffer.
func GateStdout() io.Writer        { return gateStdout }
func SetGateStdout(w io.Writer)    { gateStdout = w }
