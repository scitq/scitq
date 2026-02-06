import random
import signal
import sys
import threading
import time
from typing import List, Optional, Tuple

import grpc
from scitq2.grpc_client import Scitq2Client


def run_debug(client: Scitq2Client, workflow_id: int) -> None:
    print(f"🪲 Debug mode enabled for workflow {workflow_id}")
    last_success: Optional[int] = None
    last_failed: Optional[int] = None
    last_retry_task: Optional[int] = None

    while True:
        tasks = client.list_tasks(workflow_id=workflow_id)
        all_tasks = client.list_tasks(workflow_id=workflow_id, show_hidden=True)
        pending = [t for t in tasks if t.status == "P"]
        failed = [t for t in tasks if t.status == "F"]
        retried_failed = [t for t in all_tasks if t.status == "F" and getattr(t, "hidden", False)]

        print("\n=== Debug Controller ===")
        print(f"Pending: {len(pending)} | Failed: {len(failed)} | Retried Failed: {len(retried_failed)}")
        if not pending and not failed:
            print("No pending or failed tasks.")

        default_choice = _default_choice(last_success, last_failed, pending, failed, client)
        _print_menu(default_choice, has_retried_failed=bool(retried_failed))
        choice = input(f"Select action [{default_choice}]: ").strip()
        if choice == "":
            choice = default_choice

        if choice == "1":
            _run_task(client, workflow_id, _first_pending(pending))
        elif choice == "2":
            task_id = _dependent_pending_default(client, last_success, pending)
            if task_id is None:
                print("No dependent pending task found; falling back to first pending.")
                _run_task(client, workflow_id, _first_pending(pending))
            else:
                _run_task(client, workflow_id, task_id)
        elif choice == "3":
            new_task = _retry_task(client, workflow_id, _latest_failed(failed))
            if new_task is not None:
                last_retry_task = new_task
                _run_task(client, workflow_id, new_task)
        elif choice == "4":
            _run_task(client, workflow_id, _random_task(pending))
        elif choice == "5":
            _retry_task(client, workflow_id, _random_task(failed))
        elif choice == "6":
            task_id = _prompt_task_id()
            if task_id is None:
                continue
            task = _find_task(tasks, task_id)
            if task is None:
                print(f"Task {task_id} not found in workflow.")
                continue
            if task.status == "P":
                _run_task(client, workflow_id, task_id)
            elif task.status == "F":
                _retry_task(client, workflow_id, task_id)
            else:
                print(f"Task {task_id} is in status {task.status}, cannot run or retry.")
        elif choice == "7":
            client.delete_workflow(workflow_id)
            print("Workflow deleted. Exiting debug mode.")
            return
        elif choice == "8":
            client.update_workflow_status(workflow_id=workflow_id, status="R")
            print("Workflow resumed. Exiting debug mode.")
            return
        else:
            print("Invalid choice.")
            continue

        last_success, last_failed = _refresh_last_outcome(client, workflow_id, last_success, last_failed)
        if last_retry_task is not None and not failed:
            last_failed = None


def _default_choice(last_success: Optional[int], last_failed: Optional[int], pending, failed, client: Scitq2Client) -> str:
    if last_failed is not None and failed:
        return "3"
    if last_success is not None:
        deps = client.list_dependent_pending_tasks(last_success)
        if deps:
            return "2"
    if pending:
        return "1"
    if failed:
        return "3"
    return "7"


def _print_menu(default_choice: str, has_retried_failed: bool) -> None:
    def line(idx: str, text: str) -> str:
        entry = f"{idx}) {text}"
        if default_choice == idx:
            return f"\x1b[1m{entry}\x1b[0m"
        return entry

    if has_retried_failed and default_choice == "1":
        first_label = "Run latest (retried) pending task"
    else:
        first_label = "Run latest pending task"
    print(line("1", first_label))
    if has_retried_failed and default_choice == "2":
        print(line("2", "Run (retried) pending task dependent on latest success"))
    else:
        print(line("2", "Run pending task dependent on latest success"))
    print(line("3", "Retry latest failed task"))
    print(line("4", "Run random pending task"))
    print(line("5", "Retry random failed task"))
    print(line("6", "Select task explicitly"))
    print(line("7", "Quit debug and delete workflow"))
    print(line("8", "Quit debug and resume normal execution"))
    print(f"(default: {default_choice})")


def _first_pending(pending):
    if not pending:
        print("No pending tasks.")
        return None
    return max(pending, key=lambda t: t.task_id).task_id


def _latest_failed(failed):
    if not failed:
        print("No failed tasks.")
        return None
    return max(failed, key=lambda t: t.task_id).task_id


def _random_task(tasks):
    if not tasks:
        print("No tasks available for selection.")
        return None
    return random.choice(tasks).task_id


def _dependent_pending_default(client: Scitq2Client, last_success: Optional[int], pending) -> Optional[int]:
    if last_success is None:
        return None
    deps = client.list_dependent_pending_tasks(last_success)
    if deps:
        return deps[0]
    return None


def _prompt_task_id() -> Optional[int]:
    raw = input("Enter task id: ").strip()
    if raw == "":
        return None
    try:
        return int(raw)
    except ValueError:
        print("Invalid task id.")
        return None


def _find_task(tasks, task_id: int):
    for t in tasks:
        if t.task_id == task_id:
            return t
    return None


def _run_task(client: Scitq2Client, workflow_id: int, task_id: Optional[int]) -> None:
    if task_id is None:
        return
    task = _find_task(client.list_tasks(workflow_id=workflow_id, show_hidden=True), task_id)
    if task is not None:
        _print_task_summary(task)
    while True:
        try:
            client.debug_assign_task(workflow_id=workflow_id, task_id=task_id)
            break
        except grpc.RpcError as e:
            msg = e.details() or str(e)
            if "no compatible worker" in msg.lower():
                if not _handle_no_worker(client, workflow_id, task_id):
                    return
                continue
            print(f"Failed to assign {_task_label(task_id, task)}: {msg}")
            return

    print(f"Assigned {_task_label(task_id, task)}. Streaming logs (Ctrl-C to return to menu).")
    _stream_task_logs(client, workflow_id, task_id)


def _retry_task(client: Scitq2Client, workflow_id: int, task_id: Optional[int]) -> Optional[int]:
    if task_id is None:
        return None
    try:
        new_task = client.debug_retry_task(task_id=task_id)
        print(
            f"Retried {_task_label(task_id, _find_task(client.list_tasks(workflow_id=workflow_id, show_hidden=True), task_id))}; "
            f"new task {new_task} is pending."
        )
        return new_task
    except grpc.RpcError as e:
        print(f"Failed to retry task {task_id}: {e.details() or str(e)}")
        return None


def _handle_no_worker(client: Scitq2Client, workflow_id: int, task_id: int) -> bool:
    task = _find_task(client.list_tasks(workflow_id=workflow_id), task_id)
    if task is None or task.step_id is None:
        print("Task or step not found.")
        return False

    while True:
        print("No compatible worker available.")
        print(f"r) Trigger recruitment{_recruiter_hint(client, task.step_id)}")
        print("w) Assign a worker manually")
        print("c) Cancel")
        choice = input("Select option [r/w/c]: ").strip().lower()
        if choice == "r":
            try:
                client.debug_recruit_step(workflow_id=workflow_id, step_id=task.step_id)
            except grpc.RpcError as e:
                print(f"Recruitment failed: {e.details() or str(e)}")
            return _wait_for_recruitment(client, workflow_id, task_id, task.step_id)
        elif choice == "w":
            return _assign_worker(client, task.step_id)
        elif choice == "c":
            return False
        else:
            print("Invalid choice.")


def _assign_worker(client: Scitq2Client, step_id: int) -> bool:
    workers = client.list_workers()
    available = [w for w in workers if w.status == "R"]
    if not available:
        print("No running workers available.")
        return False
    print("Available workers:")
    for w in available:
        step = w.step_id if w.step_id is not None else "-"
        print(f"  {w.worker_id}: {w.name} (step {step})")
    raw = input("Select worker id: ").strip()
    try:
        worker_id = int(raw)
    except ValueError:
        print("Invalid worker id.")
        return False
    try:
        client.update_worker(worker_id=worker_id, step_id=step_id)
        print(f"Worker {worker_id} assigned to step {step_id}.")
        return True
    except grpc.RpcError as e:
        print(f"Failed to update worker: {e.details() or str(e)}")
        return False


def _wait_for_recruitment(client: Scitq2Client, workflow_id: int, task_id: int, step_id: int) -> bool:
    spinner = [" .  ", " .. ", " ..."]
    idx = 0
    print("Recruitment requested ... (Ctrl-C to assign differently)")
    try:
        start = time.time()
        while True:
            line = f"\rRecruitment requested{spinner[idx]}"
            sys.stdout.write(line)
            sys.stdout.flush()
            idx = (idx + 1) % len(spinner)
            elapsed = time.time() - start
            if elapsed < 2:
                time.sleep(0.1)
            elif elapsed < 10:
                time.sleep(0.25)
            else:
                time.sleep(0.5)
            task = _find_task(client.list_tasks(workflow_id=workflow_id, show_hidden=True), task_id)
            if task is None:
                sys.stdout.write("\n")
                sys.stdout.flush()
                print(f"Task {task_id} not found.")
                return False
            if task.status in ("A", "C", "D", "O", "R", "U", "V"):
                sys.stdout.write("\rRecruitment succeeded waiting for task to start   \n")
                sys.stdout.flush()
                _stream_task_logs(client, workflow_id, task_id)
                return True
            if task.status in ("S", "F"):
                sys.stdout.write("\rRecruitment succeeded waiting for task to start   \n")
                sys.stdout.flush()
                tasks = client.list_tasks(workflow_id=workflow_id, show_hidden=True)
                refreshed = _find_task(tasks, task_id) or task
                print(f"{_task_label(task_id, refreshed)} finished with status {_format_status(refreshed, tasks)}.")
                return True
            if _has_worker_for_step(client, workflow_id, step_id):
                sys.stdout.write("\rRecruitment succeeded waiting for task to start   \n")
                sys.stdout.flush()
                return True
    except KeyboardInterrupt:
        sys.stdout.write("\rRecruitment interrupted.                           \n")
        sys.stdout.flush()
        while True:
            print("r) Trigger recruitment")
            print("w) Assign a worker manually")
            print("c) Cancel")
            choice = input("Select option [r/w/c]: ").strip().lower()
            if choice == "r":
                return _wait_for_recruitment(client, workflow_id, task_id, step_id)
            if choice == "w":
                return _assign_worker(client, step_id)
            if choice == "c":
                return False
            print("Invalid choice.")


def _has_worker_for_step(client: Scitq2Client, workflow_id: int, step_id: int) -> bool:
    workers = client.list_workers(workflow_id=workflow_id)
    for w in workers:
        if w.status == "R" and w.step_id is not None and w.step_id == step_id:
            return True
    return False


def _stream_task_logs(client: Scitq2Client, workflow_id: int, task_id: int) -> None:
    saw_output = {"stdout": False, "stderr": False}
    stop_event = threading.Event()
    calls = {}
    canceled_by_user = {"value": False}
    out_prefix = "\x1b[34m→\x1b[0m"
    err_prefix = "\x1b[31m→\x1b[0m"

    def stream(kind: str):
        try:
            call = client.stream_task_logs(task_id=task_id, log_type=kind)
            calls[kind] = call
            for entry in call:
                if stop_event.is_set():
                    break
                prefix = out_prefix if kind == "stdout" else err_prefix
                saw_output[kind] = True
                print(f"{prefix} {entry.log_text}")
        except grpc.RpcError as e:
            if canceled_by_user["value"]:
                return
            print(f"Log stream error ({kind}): {e.details() or str(e)}", file=sys.stderr)

    def handle_sigint(signum, frame):
        stop_event.set()
        canceled_by_user["value"] = True
        for call in calls.values():
            try:
                call.cancel()
            except Exception:
                pass
        print("\nInterrupted. Returning to menu.")

    old_handler = signal.signal(signal.SIGINT, handle_sigint)

    threads: List[threading.Thread] = []
    for kind in ("stdout", "stderr"):
        t = threading.Thread(target=stream, args=(kind,), daemon=True)
        threads.append(t)
        t.start()

    try:
        for t in threads:
            t.join()
    finally:
        signal.signal(signal.SIGINT, old_handler)

    if not saw_output["stdout"] and not saw_output["stderr"]:
        print("Log streams ended with no output.")
    final = _find_task(client.list_tasks(workflow_id=workflow_id, show_hidden=True), task_id)
    if final is not None:
        if final.status == "R":
            print(f"Letting {_task_label(task_id, final)} run in the background.")
        elif final.status in ("A", "C", "D", "O", "U", "V"):
            print(f"{_task_label(task_id, final)} is still running (status {final.status}).")
        else:
            tasks = client.list_tasks(workflow_id=workflow_id, show_hidden=True)
            refreshed = _find_task(tasks, task_id) or final
            print(f"{_task_label(task_id, refreshed)} finished with status {_format_status(refreshed, tasks)}.")


def _refresh_last_outcome(client: Scitq2Client, workflow_id: int, last_success: Optional[int], last_failed: Optional[int]) -> Tuple[Optional[int], Optional[int]]:
    tasks = client.list_tasks(workflow_id=workflow_id)
    for t in sorted(tasks, key=lambda x: x.task_id, reverse=True):
        if t.status == "S":
            last_success = t.task_id
            break
    for t in sorted(tasks, key=lambda x: x.task_id, reverse=True):
        if t.status == "F":
            last_failed = t.task_id
            break
    return last_success, last_failed


def _task_label(task_id: int, task) -> str:
    if task is not None and getattr(task, "task_name", None):
        return f"task {task.task_name} [{task_id}]"
    return f"task {task_id}"


def _print_task_summary(task) -> None:
    bar = "\x1b[34m" + "-" * 64 + "\x1b[0m"
    title = f"Starting {_task_label(task.task_id, task)}"
    print(bar)
    print(title)
    if getattr(task, "command", None):
        lines = task.command.splitlines()
        snippet = lines[:10]
        for line in snippet:
            print(f"  cmd: {line}")
        if len(lines) > 10:
            print("  cmd: ...")
    if getattr(task, "container", None):
        print(f"  container: {task.container}")
    if getattr(task, "shell", None):
        print(f"  shell: {task.shell}")
    if getattr(task, "input", None):
        for v in task.input[:10]:
            print(f"  input: {v}")
        if len(task.input) > 10:
            print("  input: ...")
    if getattr(task, "resource", None):
        for v in task.resource[:10]:
            print(f"  resource: {v}")
        if len(task.resource) > 10:
            print("  resource: ...")
    if getattr(task, "output", None):
        print(f"  output: {task.output}")
    print(bar)


def _find_retry_child(tasks, task_id: int):
    for t in tasks:
        if getattr(t, "previous_task_id", None) == task_id:
            return t
    return None


def _format_status(task, tasks) -> str:
    if task.status == "S":
        return "\x1b[32mS (success)\x1b[0m"
    if task.status == "F":
        retry_child = _find_retry_child(tasks, task.task_id)
        if retry_child is not None:
            return (
                "\x1b[31mF (failure)\x1b[0m "
                f"but retried as {_task_label(retry_child.task_id, retry_child)}"
            )
        return "\x1b[31mF (failure)\x1b[0m"
    return task.status


def _recruiter_hint(client: Scitq2Client, step_id: int) -> str:
    try:
        recruiters = client.list_recruiters(step_id=step_id)
    except grpc.RpcError:
        return ""
    if not recruiters:
        return ""
    r = recruiters[0]
    parts = []
    if r.protofilter:
        parts.append(f"filter: {r.protofilter}")
    if getattr(r, "concurrency", None) is not None:
        parts.append(f"concurrency: {r.concurrency}")
    if getattr(r, "prefetch", None) is not None:
        parts.append(f"prefetch: {r.prefetch}")
    if getattr(r, "cpu_per_task", None) is not None:
        parts.append(f"CPU>{r.cpu_per_task}")
    if getattr(r, "memory_per_task", None) is not None:
        parts.append(f"mem>{r.memory_per_task}")
    if getattr(r, "disk_per_task", None) is not None:
        parts.append(f"disk>{r.disk_per_task}")
    hint = ", ".join(parts) if parts else "recruiter configured"
    suffix = " - and other options" if len(recruiters) > 1 else ""
    return f" ({hint}{suffix})"
