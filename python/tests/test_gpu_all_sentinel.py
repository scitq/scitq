"""Tests for the `task_spec.gpu="all"` sentinel
(spec: gpu_all_sentinel.md).

The sentinel means "size the worker by whatever the recruiter picks,
then give the lone task every device on that host." It survives all
the way to the SubmitTask request as a separate bool, and the server
resolves it to a concrete `min_gpu` at task assignment.

These tests cover the Python-side contract: parser shape, recruiter
sizing, mutual-exclusion with concurrency>1, and the
satisfy-the-concurrency-driver rule.
"""
import pytest

from scitq2.workflow import TaskSpec
from scitq2.recruit import WorkerPool, W


# ---------------- parser ----------------


def test_gpu_all_string_lowercase():
    ts = TaskSpec(gpu="all")
    assert ts.gpu == "all"


def test_gpu_all_string_uppercase_is_normalised():
    ts = TaskSpec(gpu="ALL")
    assert ts.gpu == "all"


def test_gpu_all_with_whitespace():
    ts = TaskSpec(gpu="  all  ")
    assert ts.gpu == "all"


def test_gpu_integer_paths_unchanged():
    # The sentinel string is a separate code path — numeric inputs
    # must keep producing integers as before. gpu>=1 now drives
    # concurrency on its own; gpu=0/None still needs a sibling
    # driver (we use cpu=1 here).
    assert TaskSpec(gpu=2).gpu == 2
    assert TaskSpec(gpu=True).gpu == 1
    assert TaskSpec(cpu=1, gpu=0).gpu == 0
    assert TaskSpec(cpu=1, gpu=None).gpu is None


def test_gpu_invalid_string_rejected():
    with pytest.raises(ValueError, match="integer count"):
        TaskSpec(gpu="some")


# ---------------- concurrency interaction ----------------


def test_gpu_all_satisfies_concurrency_driver_rule():
    # Today TaskSpec() requires at least one of {concurrency, cpu,
    # mem, numa}. gpu="all" inherently implies concurrency=1, so it
    # also satisfies that requirement on its own.
    ts = TaskSpec(gpu="all")
    assert ts.gpu == "all"
    assert ts.concurrency is None  # the recruiter forces it to 1


def test_gpu_all_with_concurrency_1_is_allowed():
    # Explicit redundancy is fine — the user is restating what
    # gpu="all" already implies.
    ts = TaskSpec(gpu="all", concurrency=1)
    assert ts.gpu == "all"
    assert ts.concurrency == 1


def test_gpu_all_with_concurrency_gt_1_rejected():
    # "Every task gets every device" on a fixed-GPU host is
    # incoherent; reject at parse time so the mistake never reaches
    # the server.
    with pytest.raises(ValueError, match="exclusive"):
        TaskSpec(gpu="all", concurrency=2)


def test_numeric_gpu_alone_is_a_valid_driver():
    # gpu=N>=1 alone satisfies the concurrency-driver rule: the
    # recruiter sizes concurrency = floor(flavor.gpu_count /
    # gpu_per_task) just like cpu_per_task does on the CPU axis.
    ts = TaskSpec(gpu=2)
    assert ts.gpu == 2
    # gpu=0 still doesn't qualify — it's "no GPU at all", not a driver.
    with pytest.raises(ValueError, match="at least one of"):
        TaskSpec(gpu=0)


# ---------------- recruiter sizing ----------------


def test_recruiter_for_gpu_all_is_static_concurrency_1():
    pool = WorkerPool(W.has_gpu == True)  # noqa: E712
    ts = TaskSpec(gpu="all")
    options = pool.build_recruiter(ts)
    # gpu="all" → exclusive: concurrency pinned to 1, no per-task
    # ratios (cpu/mem/disk/gpu_per_task) should leak in.
    assert options["concurrency"] == 1
    assert "cpu_per_task" not in options
    assert "memory_per_task" not in options
    assert "disk_per_task" not in options
    assert "gpu_per_task" not in options


def test_recruiter_gpu_all_ignores_per_task_resources():
    # When the workflow author writes both cpu=4 AND gpu="all", the
    # cpu=4 is informational only (it might still satisfy a
    # worker_pool.match filter the author wrote separately). The
    # recruiter still pins concurrency=1 because gpu="all" is
    # exclusive — per-task ratios would only matter if we were
    # packing multiple tasks onto one worker, which we explicitly
    # aren't.
    pool = WorkerPool(W.has_gpu == True)  # noqa: E712
    ts = TaskSpec(cpu=4, mem=16, gpu="all")
    options = pool.build_recruiter(ts)
    assert options["concurrency"] == 1
    assert "cpu_per_task" not in options


def test_recruiter_integer_gpu_unaffected():
    # Regression guard: existing gpu=N integer paths still flow
    # through the dynamic-concurrency branch with gpu_per_task=N,
    # not into the gpu="all" branch.
    pool = WorkerPool(W.has_gpu == True)  # noqa: E712
    ts = TaskSpec(cpu=1, gpu=2)
    options = pool.build_recruiter(ts)
    assert "concurrency" not in options
    assert options.get("cpu_per_task") == 1
    assert options.get("gpu_per_task") == 2


# ---------------- pool-doesn't-target-gpu warning ----------------


def test_warn_when_task_wants_gpu_but_pool_has_no_gpu_filter():
    import warnings as _w

    pool = WorkerPool(W.cpu >= 8)   # cpu filter only — no W.has_gpu
    ts = TaskSpec(cpu=1, gpu=1)

    with _w.catch_warnings(record=True) as recorded:
        _w.simplefilter("always")
        pool.build_recruiter(ts)
        assert any("worker_pool has no GPU filter" in str(w.message)
                   for w in recorded), \
            f"expected a no-GPU-filter warning, got: {[str(w.message) for w in recorded]}"


def test_warn_also_fires_for_gpu_all():
    import warnings as _w

    pool = WorkerPool(W.cpu >= 8)
    ts = TaskSpec(gpu="all")

    with _w.catch_warnings(record=True) as recorded:
        _w.simplefilter("always")
        pool.build_recruiter(ts)
        assert any("worker_pool has no GPU filter" in str(w.message)
                   for w in recorded)


def test_no_warning_when_pool_targets_gpu():
    import warnings as _w

    pool = WorkerPool(W.has_gpu == True)  # noqa: E712
    ts = TaskSpec(cpu=1, gpu=1)

    with _w.catch_warnings(record=True) as recorded:
        _w.simplefilter("always")
        pool.build_recruiter(ts)
        gpu_warnings = [w for w in recorded
                        if "worker_pool has no GPU filter" in str(w.message)]
        assert not gpu_warnings, f"unexpected warning: {gpu_warnings}"


def test_no_warning_when_task_doesnt_want_gpu():
    import warnings as _w

    pool = WorkerPool(W.cpu >= 8)
    ts = TaskSpec(cpu=1, mem=8)   # no gpu at all

    with _w.catch_warnings(record=True) as recorded:
        _w.simplefilter("always")
        pool.build_recruiter(ts)
        gpu_warnings = [w for w in recorded
                        if "worker_pool has no GPU filter" in str(w.message)]
        assert not gpu_warnings
