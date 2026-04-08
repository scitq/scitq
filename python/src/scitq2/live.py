"""Live DSL mode — keeps the DSL running to observe results and submit new tasks."""

import time
import logging
from typing import Optional, List, Tuple
from scitq2.grpc_client import Scitq2Client

logger = logging.getLogger(__name__)


class LiveContext:
    """Provides wait/observe/stop/kill primitives for live DSL mode.

    The LiveContext connects to a running scitq server and lets the DSL
    script react to task results, submit new tasks, and control execution.
    """

    def __init__(self, client: Scitq2Client, poll_interval: float = 5.0):
        self.client = client
        self.poll_interval = poll_interval

    def wait(self, task) -> Optional[float]:
        """Block until a task reaches terminal status (S or F).

        Returns the quality_score on success, None if no quality defined.
        Raises RuntimeError on failure.
        When a task succeeds but quality_score is not yet populated (async extraction),
        polls a few more times to wait for it.
        """
        task_id = self._task_id(task)
        while True:
            t = self._get_task(task_id)
            if t is None:
                raise RuntimeError(f"Task {task_id} not found")
            if t.status == "S":
                score = getattr(t, "quality_score", None)
                if score is not None:
                    return score
                # Quality extraction is async — wait briefly for it
                for _ in range(5):
                    time.sleep(1)
                    t = self._get_task(task_id)
                    if t and getattr(t, "quality_score", None) is not None:
                        return t.quality_score
                return None  # no quality defined for this step
            if t.status == "F":
                raise RuntimeError(f"Task {task_id} failed")
            time.sleep(self.poll_interval)

    def wait_all(self, tasks) -> List[Tuple[int, Optional[float]]]:
        """Wait for all tasks to reach terminal status.

        Returns list of (task_id, quality_score) pairs.
        Failed tasks have quality_score = None.
        For succeeded tasks, waits briefly for async quality extraction.
        """
        task_ids = [self._task_id(t) for t in tasks]
        remaining = set(task_ids)
        results = {}
        # Track how many times we've seen S without quality
        quality_retries: dict = {}

        while remaining:
            for tid in list(remaining):
                t = self._get_task(tid)
                if t is None:
                    results[tid] = None
                    remaining.discard(tid)
                elif t.status == "S":
                    score = getattr(t, "quality_score", None)
                    if score is not None:
                        results[tid] = score
                        remaining.discard(tid)
                    else:
                        quality_retries[tid] = quality_retries.get(tid, 0) + 1
                        if quality_retries[tid] > 5:
                            results[tid] = None  # gave up
                            remaining.discard(tid)
                elif t.status == "F":
                    results[tid] = None
                    remaining.discard(tid)
            if remaining:
                time.sleep(self.poll_interval)

        return [(tid, results[tid]) for tid in task_ids]

    def observe(self, task) -> Optional[float]:
        """Non-blocking: return the current quality_score of a task, or None."""
        task_id = self._task_id(task)
        t = self._get_task(task_id)
        if t is None:
            return None
        try:
            return t.quality_score if t.HasField("quality_score") else None
        except ValueError:
            return None

    def stop(self, task):
        """Send SIGTERM (graceful stop) to a running task."""
        task_id = self._task_id(task)
        self.client.signal_task(task_id, "T")

    def kill(self, task):
        """Send SIGKILL (hard kill) to a running task."""
        task_id = self._task_id(task)
        self.client.signal_task(task_id, "K")

    def is_done(self, task) -> bool:
        """Check if a task has reached terminal status."""
        task_id = self._task_id(task)
        t = self._get_task(task_id)
        return t is not None and t.status in ("S", "F")

    def _task_id(self, task) -> int:
        """Extract task_id from a Task object or int."""
        if isinstance(task, int):
            return task
        if hasattr(task, "task_id"):
            return task.task_id
        raise TypeError(f"Expected task ID (int) or Task object, got {type(task)}")

    def _get_task(self, task_id: int):
        """Fetch a single task by ID. Retries on transient gRPC errors."""
        for attempt in range(3):
            try:
                tasks = self.client.list_tasks()
                for t in tasks:
                    if t.task_id == task_id:
                        return t
                return None  # task not in list (genuinely missing)
            except Exception as e:
                logger.warning(f"Error fetching task {task_id} (attempt {attempt+1}): {e}")
                if attempt < 2:
                    time.sleep(2)
        return None
