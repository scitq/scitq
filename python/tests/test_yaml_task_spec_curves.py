"""Tests for per-attempt resource curves (`cpu: [4,8,16]` etc.) declared in
YAML task_spec blocks.

The DSL side of curves (TaskSpec._normalize_curve, max_cpu, resources_at_attempt)
is covered by test_task_spec_curves.py. These tests lock in that a YAML author
writing `cpu: [4, 8]` in a task_spec block reaches the same TaskSpec state as
the Python `TaskSpec(cpu=[4, 8])` call — no runner-side translation needed.

Regression guard: it is easy to teach _resolve_task_spec / _resolve_field a new
list-flattening or string-coercion rule that would silently break curves. If
that happens, these tests fail loudly.
"""
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr
from scitq2.workflow import TaskSpec


def _resolve(ts_def, params=None, itervar=None, extra_vars=None):
    return yr._resolve_task_spec(
        ts_def,
        params=params or SimpleNamespace(),
        itervar=itervar,
        extra_vars=extra_vars,
    )


def _spec(ts_def, **kw):
    """Resolve then instantiate — the two-step path a real YAML step takes."""
    return TaskSpec(**_resolve(ts_def, **kw))


# ---------------- bare list is preserved through resolution ----------------


def test_yaml_bare_list_cpu_curve():
    ts = _spec({'cpu': [4, 8, 16]})
    assert ts.cpu_curve == [4.0, 8.0, 16.0]
    assert ts.cpu == 4          # scalar back-compat is curve[0]
    assert ts.max_cpu == 16     # what the recruiter sizes on


def test_yaml_bare_list_all_three():
    ts = _spec({'cpu': [4, 8], 'mem': [16, 32], 'disk': [100, 200]})
    assert ts.cpu_curve == [4.0, 8.0]
    assert ts.mem_curve == [16.0, 32.0]
    assert ts.disk_curve == [100.0, 200.0]


def test_yaml_mixed_scalar_and_curve():
    # cpu stays flat while mem escalates — the common OOM-only shape.
    ts = _spec({'cpu': 8, 'mem': [16, 32, 64]})
    assert ts.cpu_curve == [8.0]
    assert ts.mem_curve == [16.0, 32.0, 64.0]
    assert ts.max_cpu == 8      # flat curve worst-case == its single value


# ---------------- curve under a cond: block ----------------


def test_yaml_curve_under_cond_true_branch():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        'true': {'cpu': [16, 32], 'mem': [64, 128, 256]},
        'false': {'cpu': 4, 'mem': 16},
    }
    ts = _spec(ts_def, itervar={'sample.depth_gb': '150'})
    assert ts.cpu_curve == [16.0, 32.0]
    assert ts.mem_curve == [64.0, 128.0, 256.0]


def test_yaml_curve_under_cond_false_branch():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        'true': {'cpu': [16, 32], 'mem': [64, 128, 256]},
        'false': {'cpu': 4, 'mem': 16},
    }
    ts = _spec(ts_def, itervar={'sample.depth_gb': '12'})
    # Non-list branch: singleton curve — same as pre-curve behaviour.
    assert ts.cpu_curve == [4.0]
    assert ts.mem_curve == [16.0]


# ---------------- {params.x} interpolation inside a curve ----------------


def test_yaml_curve_with_param_interpolation():
    class P:
        cpu_base = 8
    ts = _spec({'cpu': ['{params.cpu_base}', '{params.cpu_base}*2']}, params=P())
    # After substitution + arithmetic, we get [8, 16] as floats.
    assert ts.cpu_curve == [8.0, 16.0]


def test_yaml_curve_with_arithmetic_and_scalar_sibling():
    class P:
        base = 4
    ts = _spec(
        {'cpu': ['{params.base}', '{params.base}*4'], 'mem': '{params.base}*10'},
        params=P(),
    )
    assert ts.cpu_curve == [4.0, 16.0]
    assert ts.mem_curve == [40.0]


# ---------------- validation errors surface from the DSL ----------------


def test_yaml_decreasing_curve_rejected():
    # DSL validation must reach the YAML author unchanged.
    with pytest.raises(ValueError, match="monotonically non-decreasing"):
        _spec({'cpu': [8, 4]})


def test_yaml_empty_curve_rejected():
    with pytest.raises(ValueError, match="non-empty"):
        _spec({'cpu': []})


def test_yaml_zero_in_curve_rejected():
    with pytest.raises(ValueError, match="all positive"):
        _spec({'cpu': [4, 0, 8]})
