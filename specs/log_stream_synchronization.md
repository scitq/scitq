# Log Stream Synchronization

## Problem

The worker client sends task logs (stdout/stderr) via a **streaming RPC** (`SendTaskLogs`) and task status updates via **unary RPCs** (`UpdateTaskStatus`). These are independent gRPC calls with no ordering guarantee. When a fast task completes, the status update can arrive at the server before the log stream delivers all its messages — or even before the first message.

This caused two classes of bugs:

1. **Quality score extraction failures**: the server extracted quality scores from incomplete log files (missing trailing lines).
2. **CI test flakiness**: the race was timing-dependent, causing intermittent failures that required 10+ retries.

## Solution: Three-layer synchronization

### Layer 1 — Client: `CloseAndRecv()` (primary fix)

The client calls `stream.CloseAndRecv()` instead of `stream.CloseSend()` after sending all log lines. `CloseAndRecv()` blocks until the server's `SendAndClose(Ack)` response arrives, which only happens after the server has written every log line to disk and processed the EOF. Only then does the client send the status update.

**This is the critical fix.** It transforms two independent RPCs into a sequenced pair: logs are guaranteed on disk before any status update is sent.

```
Client:                          Server (SendTaskLogs):
  Send(line 1) ──────────────►    Recv → write to file
  Send(line 2) ──────────────►    Recv → write to file
  Send(line 3) ──────────────►    Recv → write to file
  CloseAndRecv() ─── EOF ────►    Recv → EOF → SendAndClose(Ack)
       ◄─── Ack ──────────────
  UpdateTaskStatus("S") ─────►  Server (UpdateTaskStatus):
                                   logs guaranteed on disk
                                   extract quality score
                                   commit status
```

### Layer 2 — Client: pipe read ordering

`logWg.Wait()` runs before `cmd.Wait()`. Go's `cmd.Wait()` closes pipe read ends, so calling it first could discard unread data in the pipe buffer. The correct order:

1. Wait for scanner goroutines to finish reading all pipe data (`logWg.Wait()`)
2. Then call `cmd.Wait()` to collect exit status

### Layer 3 — Client: process group signals

Bare tasks run with `Setpgid: true` and signals are sent to the process group (`syscall.Kill(-pid, sig)`) rather than the process alone. This ensures `sh -c sleep 600` kills both `sh` and `sleep`, preventing orphaned children from holding pipes open (which would block `logWg.Wait()` indefinitely).

### Layer 4 — Server: `activeLogStreams` (defense in depth)

The server tracks open log streams in a `sync.Map`. Before committing S/F status, `UpdateTaskStatus` checks if a log stream is active for the task and waits up to 5 seconds for it to close. With `CloseAndRecv()` in place, this wait is almost always a no-op, but it provides a safety net against edge cases.

## Impact

- CI tests went from ~14 attempts to pass (Build #118) to first-attempt success (Build #120)
- Test suite runtime dropped from >5 minutes to ~3 minutes (elimination of retry/sleep workarounds)
- Quality score extraction is now deterministic
