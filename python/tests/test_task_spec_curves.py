"""Tests for the per-attempt resource curve on TaskSpec
(spec: addition_from_nextflow.md A — Retry with resource escalation).

Phase A2-1 covers the Python data model and the curve→initial-submission /
curve→recruiter sizing logic. Server-side retry-decision + worker-side
failure classification land in later phases.
"""
import pytest

from scitq2.workflow import TaskSpec


# ---------------- scalar back-compat (today's behaviour, unchanged) ----------


def test_scalar_cpu_creates_singleton_curve():
    ts = TaskSpec(cpu=4)
    assert ts.cpu == 4
    assert ts.cpu_curve == [4.0]
    assert ts.max_cpu == 4
    assert ts.resources_at_attempt(0) == (4.0, None, None)


def test_scalar_mem_disk():
    ts = TaskSpec(cpu=4, mem=8, disk=100)
    assert ts.cpu_curve == [4.0]
    assert ts.mem_curve == [8.0]
    assert ts.disk_curve == [100.0]
    assert ts.max_mem == 8
    assert ts.max_disk == 100


def test_concurrency_only_unchanged():
    ts = TaskSpec(concurrency=4)
    assert ts.concurrency == 4
    assert ts.cpu_curve is None
    assert ts.max_cpu is None


# ---------------- curve form ----------------


def test_curve_form_cpu():
    ts = TaskSpec(cpu=[4, 8, 16])
    assert ts.cpu == 4              # back-compat scalar is curve[0]
    assert ts.cpu_curve == [4.0, 8.0, 16.0]
    assert ts.max_cpu == 16


def test_curve_form_mem_only():
    # Only mem escalates; cpu stays flat — the common OOM pattern.
    ts = TaskSpec(cpu=16, mem=[40, 80, 160])
    assert ts.cpu == 16
    assert ts.cpu_curve == [16.0]
    assert ts.mem_curve == [40.0, 80.0, 160.0]
    assert ts.max_mem == 160


def test_curve_form_all_three():
    ts = TaskSpec(cpu=[4, 8], mem=[16, 32], disk=[100, 200])
    assert ts.resources_at_attempt(0) == (4.0, 16.0, 100.0)
    assert ts.resources_at_attempt(1) == (8.0, 32.0, 200.0)


# ---------------- resources_at_attempt clamp ----------------


def test_resources_at_attempt_clamps_beyond_curve():
    ts = TaskSpec(cpu=16, mem=[40, 80, 160])
    assert ts.resources_at_attempt(0) == (16.0, 40.0, None)
    assert ts.resources_at_attempt(1) == (16.0, 80.0, None)
    assert ts.resources_at_attempt(2) == (16.0, 160.0, None)
    # Beyond curve length → stay at the largest value.
    assert ts.resources_at_attempt(5) == (16.0, 160.0, None)


def test_resources_at_attempt_negative_clamps_to_zero():
    ts = TaskSpec(cpu=[4, 8])
    assert ts.resources_at_attempt(-1) == (4.0, None, None)


# ---------------- validation ----------------


def test_empty_curve_rejected():
    with pytest.raises(ValueError, match="curve must be non-empty"):
        TaskSpec(cpu=[])


def test_non_monotonic_curve_rejected():
    # Retry should never need *less* than the prior attempt.
    with pytest.raises(ValueError, match="monotonically non-decreasing"):
        TaskSpec(cpu=[16, 32, 8])


def test_zero_and_negative_curve_values_rejected():
    with pytest.raises(ValueError, match="must be all positive"):
        TaskSpec(cpu=[4, 0])
    with pytest.raises(ValueError, match="must be all positive"):
        TaskSpec(cpu=[-1])


def test_non_numeric_curve_element_rejected():
    with pytest.raises(ValueError, match="not a number"):
        TaskSpec(cpu=[4, "huge"])


def test_invalid_value_type_rejected():
    with pytest.raises(ValueError, match="must be a number or list"):
        TaskSpec(cpu={'bad': 'shape'})


def test_curve_with_numa_rejected():
    with pytest.raises(ValueError, match="mutually exclusive"):
        TaskSpec(mem=[40, 80], numa=1)


def test_yaml_numeric_string_accepted():
    # YAML interpolation can hand us numeric-looking strings — coerce
    # so `cpu: "{params.threads}"` keeps working.
    ts = TaskSpec(cpu="8")
    assert ts.cpu == 8.0
    assert ts.cpu_curve == [8.0]


def test_yaml_non_numeric_string_rejected():
    with pytest.raises(ValueError, match="must be a number or list"):
        TaskSpec(cpu="huge")


def test_flat_curves_are_legal():
    # mem: [40, 40, 40] is degenerate but technically monotonic — allow it
    # rather than surprise the user with a validation error.
    ts = TaskSpec(mem=[40, 40, 40])
    assert ts.mem_curve == [40.0, 40.0, 40.0]


# ---------------- equality + str ----------------


def test_equality_includes_curve():
    a = TaskSpec(cpu=4, mem=[40, 80])
    b = TaskSpec(cpu=4, mem=[40, 80])
    assert a == b


def test_equality_distinguishes_curve_from_scalar():
    a = TaskSpec(cpu=4, mem=40)
    b = TaskSpec(cpu=4, mem=[40])
    # Both normalise to the same singleton curve — equal.
    assert a == b


def test_equality_distinguishes_different_curves():
    a = TaskSpec(cpu=4, mem=[40, 80])
    b = TaskSpec(cpu=4, mem=[40, 160])
    assert a != b


def test_str_shows_curve_in_canonical_form():
    ts = TaskSpec(cpu=16, mem=[40, 80, 160], disk=200)
    s = str(ts)
    assert "cpu=16" in s            # singleton collapses to scalar
    assert "mem=[40.0, 80.0, 160.0]" in s
    assert "disk=200" in s
