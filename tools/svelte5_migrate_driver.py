#!/usr/bin/env python3
"""Drive `npx sv migrate svelte-5` non-interactively on the ui/ package.

Why this exists
---------------
The official Svelte 5 migrator uses clack prompts. Clack:
  - requires a TTY (piped stdin is ignored);
  - draws prompts via cursor-positioning escape sequences that
    *don't* contain the prompt text as a contiguous substring
    unless the terminal reports a sane size.

So driving it from a plain shell pipe or from `printf | sv migrate`
silently hangs. We fork a pty, set a 120×40 window size on it
(so the prompt renderer emits readable text we can pattern-match),
then write the right keystrokes when each prompt's marker appears.

What it does
------------
1. Confirms the "Continue?" prompt (cursor starts on No → Left, Enter).
2. On the folder-selection prompt: deselects dist/docs/gen and keeps
   only src/ so we don't touch the build output or generated proto stubs.
3. Reaps the child and exits.

How to use
----------
    python3 tools/svelte5_migrate_driver.py

Prerequisites:
  - committed working tree (the migrator refuses otherwise);
  - npx available (it'll pull `sv` on first run);
  - macOS or Linux (pty/termios).

Caveat
------
After the migration finishes mechanically, you still have to:
  - run `npm run build` and fix the inevitable compile errors;
  - exercise the app end-to-end looking for *silent* reactivity
    regressions (let-was-state, $:-was-effect mixups, missing
    $bindable() on bind: props, etc.). Plan a couple of evenings.
"""

import fcntl
import os
import pty
import select
import struct
import sys
import termios
import time

# Repo-root relative; resolves at runtime so this works regardless of cwd.
_HERE = os.path.dirname(os.path.abspath(__file__))
_UI_DIR = os.path.abspath(os.path.join(_HERE, "..", "ui"))

CMD = ["npx", "sv", "migrate", "svelte-5", "-C", _UI_DIR]


def send(fd, seq, label=""):
    if label:
        sys.stderr.write(f"\n[driver] sending: {label}\n")
    os.write(fd, seq)


def main():
    pid, fd = pty.fork()
    if pid == 0:
        os.execvp(CMD[0], CMD)
        os._exit(1)

    # Without an explicit size the pty reports 0×0 and clack's prompt
    # renderer falls back to one-char-per-line mode, which breaks our
    # substring detection. Set a normal terminal size.
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))

    buf = bytearray()
    handled_continue = False
    handled_folders = False

    deadline = time.time() + 480  # hard cap so a stuck run can't sit forever

    while True:
        if time.time() > deadline:
            sys.stderr.write("\n[driver] TIMEOUT\n")
            os.kill(pid, 9)
            break

        r, _, _ = select.select([fd], [], [], 0.3)
        if r:
            try:
                data = os.read(fd, 4096)
            except OSError:
                break
            if not data:
                break
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
            buf.extend(data)

        text = buf.decode("utf-8", errors="replace")

        if not handled_continue and "Continue?" in text:
            time.sleep(2.0)  # let the prompt finish painting
            # Default cursor is on "No"; Left moves to "Yes", Enter confirms.
            send(fd, b"\x1b[D", "Left (Continue → Yes)")
            time.sleep(0.4)
            send(fd, b"\r", "Enter")
            handled_continue = True

        if handled_continue and not handled_folders and "folders should be migrated" in text:
            time.sleep(2.0)
            # Cursor starts on `dist` (index 0). Deselect dist/docs/gen,
            # keep src selected, confirm.
            send(fd, b" ", "Space (deselect dist)")
            time.sleep(0.3)
            send(fd, b"\x1b[B", "Down → docs")
            time.sleep(0.3)
            send(fd, b" ", "Space (deselect docs)")
            time.sleep(0.3)
            send(fd, b"\x1b[B", "Down → gen")
            time.sleep(0.3)
            send(fd, b" ", "Space (deselect gen)")
            time.sleep(0.3)
            send(fd, b"\x1b[B", "Down → src")
            time.sleep(0.3)
            send(fd, b"\r", "Enter (confirm)")
            handled_folders = True

        # Reap child; drain any tail output before exiting.
        try:
            done_pid, _ = os.waitpid(pid, os.WNOHANG)
            if done_pid == pid:
                while True:
                    r2, _, _ = select.select([fd], [], [], 0.3)
                    if not r2:
                        break
                    try:
                        d = os.read(fd, 4096)
                    except OSError:
                        break
                    if not d:
                        break
                    sys.stdout.buffer.write(d)
                    sys.stdout.buffer.flush()
                break
        except ChildProcessError:
            break

    try:
        os.close(fd)
    except OSError:
        pass


if __name__ == "__main__":
    main()
