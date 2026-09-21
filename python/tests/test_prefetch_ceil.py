"""Tests for the `>=` / `>` prefix on `prefetch` percent strings, which
switches the recruiter's rounding rule from floor to ceil so a small
worker still ends up with at least 1 prefetch slot.

The DSL side stores the flag on `TaskSpec.prefetch_ceil`; `build_recruiter`
forwards it into `prefetch_percent_ceil` on the recruiter row; the server's
`computePrefetchForRecruiterWorker` decides which arithmetic to use.
"""
import pytest

from scitq2.workflow import TaskSpec


def _ts(prefetch):
    return TaskSpec(mem=5, prefetch=prefetch)


# ---------------- bare percent → floor (default, unchanged) ----------------


def test_bare_percent_is_floor():
    ts = _ts("25%")
    assert ts.prefetch == pytest.approx(0.25)
    assert ts.prefetch_ceil is False


# ---------------- >= / > prefix → ceil ----------------


def test_gte_percent_sets_ceil_flag():
    ts = _ts(">=25%")
    assert ts.prefetch == pytest.approx(0.25)
    assert ts.prefetch_ceil is True


def test_gt_percent_sets_ceil_flag():
    # `>25%` is accepted as an alias for `>=25%` — the ceil rounding
    # already gives "strictly more than 25%" whenever the exact value
    # is fractional, and treating both operators the same avoids a
    # useless notation split at the workflow author's expense.
    ts = _ts(">25%")
    assert ts.prefetch == pytest.approx(0.25)
    assert ts.prefetch_ceil is True


def test_gte_with_whitespace():
    ts = _ts(">= 50%")
    assert ts.prefetch == pytest.approx(0.5)
    assert ts.prefetch_ceil is True


def test_gte_with_bare_number_does_not_ceil():
    # `>=1` is not a percent — it means "at least a static prefetch of 1".
    # The recruit-time rounding rule does not apply because there is no
    # percent multiplication, so the ceil flag stays off. The value is
    # taken as a scalar (matches the pre-feature "prefetch=1" behaviour).
    ts = _ts(">=1")
    assert ts.prefetch == pytest.approx(1.0)
    assert ts.prefetch_ceil is False


# ---------------- non-percent inputs unchanged ----------------


def test_integer_prefetch_no_ceil():
    ts = _ts(2)
    assert ts.prefetch == pytest.approx(2.0)
    assert ts.prefetch_ceil is False


def test_none_prefetch_no_ceil():
    ts = _ts(None)
    assert ts.prefetch == pytest.approx(0.0)
    assert ts.prefetch_ceil is False


# ---------------- build_recruiter forwards the flag ----------------


def test_build_recruiter_forwards_ceil_flag():
    # The DSL's build_recruiter is what puts prefetch_percent_ceil onto
    # the create_recruiter call; without this hop the server would never
    # see the flag. Test the intermediate `options` dict directly.
    from scitq2.recruit import WorkerPool

    pool = WorkerPool(max_recruited=1)
    opts = pool.build_recruiter(TaskSpec(mem=5, prefetch=">=25%"))
    assert opts["prefetch_percent"] == 25
    assert opts.get("prefetch_percent_ceil") is True

    # Floor path (the default) omits the flag entirely — server-side
    # NOT NULL DEFAULT FALSE takes care of the missing key.
    opts2 = pool.build_recruiter(TaskSpec(mem=5, prefetch="25%"))
    assert opts2["prefetch_percent"] == 25
    assert "prefetch_percent_ceil" not in opts2
