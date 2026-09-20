"""Tests for TaskSpec.mem_shared / disk_shared — the per-worker shared
overhead (Reading B) declared alongside mem/disk.

Locks in the Python data model:
  * scalar and curve forms both parse
  * validation (positive, monotonic non-decreasing) applies the same as
    for mem/disk curves
  * max_mem_shared / max_disk_shared expose the recruiter-sizing value
  * fields default to None when not declared (linear model preserved)
"""
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr
from scitq2.workflow import TaskSpec


# ---------------- scalar ----------------


def test_scalar_mem_shared_creates_singleton_curve():
    ts = TaskSpec(mem=5, mem_shared=15)
    assert ts.mem_shared == 15
    assert ts.mem_shared_curve == [15.0]
    assert ts.max_mem_shared == 15


def test_scalar_disk_shared_creates_singleton_curve():
    ts = TaskSpec(mem=5, disk=100, disk_shared=200)
    assert ts.disk_shared == 200
    assert ts.disk_shared_curve == [200.0]
    assert ts.max_disk_shared == 200


def test_no_shared_defaults_to_none():
    # No opt-in → curve stays None, scalar reads as None too. This is
    # what preserves the linear-model behaviour for callers that never
    # touch the feature.
    ts = TaskSpec(mem=5)
    assert ts.mem_shared is None
    assert ts.mem_shared_curve is None
    assert ts.max_mem_shared is None
    assert ts.disk_shared is None
    assert ts.disk_shared_curve is None
    assert ts.max_disk_shared is None


# ---------------- curve ----------------


def test_curve_mem_shared():
    ts = TaskSpec(mem=[5, 10, 20], mem_shared=[15, 15, 30])
    assert ts.mem_shared_curve == [15.0, 15.0, 30.0]
    assert ts.max_mem_shared == 30
    # Back-compat scalar attribute is curve[0].
    assert ts.mem_shared == 15


def test_curve_mem_shared_can_be_flat():
    # Common shape: per-task working set escalates, shared index doesn't.
    ts = TaskSpec(mem=[5, 10, 20], mem_shared=15)
    assert ts.mem_shared_curve == [15.0]
    assert ts.max_mem_shared == 15


def test_mem_scalar_with_shared_curve():
    ts = TaskSpec(mem=8, mem_shared=[15, 20, 30])
    assert ts.mem_curve == [8.0]
    assert ts.mem_shared_curve == [15.0, 20.0, 30.0]
    assert ts.max_mem_shared == 30


def test_both_dimensions_with_shared():
    ts = TaskSpec(mem=5, mem_shared=15, disk=100, disk_shared=200)
    assert ts.max_mem_shared == 15
    assert ts.max_disk_shared == 200


# ---------------- validation ----------------


def test_shared_negative_rejected():
    # Same rules as mem_curve: all-positive.
    with pytest.raises(ValueError, match="all positive"):
        TaskSpec(mem=5, mem_shared=-1)


def test_shared_zero_rejected():
    with pytest.raises(ValueError, match="all positive"):
        TaskSpec(mem=5, mem_shared=0)


def test_shared_decreasing_curve_rejected():
    with pytest.raises(ValueError, match="monotonically non-decreasing"):
        TaskSpec(mem=5, mem_shared=[15, 10])


def test_shared_empty_curve_rejected():
    with pytest.raises(ValueError, match="non-empty"):
        TaskSpec(mem=5, mem_shared=[])


# ---------------- YAML runner passthrough ----------------


def test_yaml_bare_mem_shared():
    # The runner spreads outputs of _resolve_task_spec into TaskSpec kwargs
    # (see yaml_runner.py: `TaskSpec(**ts_def)`). A `mem_shared:` sibling
    # of `mem:` must land as the kwarg — no extra plumbing needed.
    ts_def = {"mem": 5, "mem_shared": 15}
    resolved = yr._resolve_task_spec(ts_def, params=SimpleNamespace(),
                                     itervar=None, extra_vars=None)
    ts = TaskSpec(**resolved)
    assert ts.mem_shared == 15
    assert ts.max_mem_shared == 15


def test_yaml_curve_mem_shared():
    ts_def = {"mem": [5, 10, 20], "mem_shared": [15, 15, 30]}
    resolved = yr._resolve_task_spec(ts_def, params=SimpleNamespace(),
                                     itervar=None, extra_vars=None)
    ts = TaskSpec(**resolved)
    assert ts.mem_shared_curve == [15.0, 15.0, 30.0]


def test_yaml_shared_with_param_interpolation():
    class P:
        idx_gb = 15
    ts_def = {"mem": 5, "mem_shared": "{params.idx_gb}"}
    resolved = yr._resolve_task_spec(ts_def, params=P(),
                                     itervar=None, extra_vars=None)
    ts = TaskSpec(**resolved)
    assert ts.mem_shared == 15
