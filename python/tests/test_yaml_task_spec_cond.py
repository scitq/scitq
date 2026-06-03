"""Tests for the structured cond: block in task_spec (spec:
addition_from_nextflow.md E).

E reuses the existing top-level cond support in `_resolve_task_spec`
plus F's widened expression evaluator. The result: workflow authors can
branch the whole spec on a data-driven expression without writing
per-field templated strings (which would have fragmented recruitment
into 1-flavor-per-task)."""
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr


def _resolve(ts_def, params=None, itervar=None, extra_vars=None):
    return yr._resolve_task_spec(
        ts_def,
        params=params or SimpleNamespace(),
        itervar=itervar,
        extra_vars=extra_vars,
    )


# ---------------- expression cond on iter context ----------------


def test_cond_expression_true_branch():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        False: {'cpu': 16, 'mem': 64},
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '150'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 32, 'mem': 256}


def test_cond_expression_false_branch():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        False: {'cpu': 16, 'mem': 64},
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '12'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 16, 'mem': 64}


def test_cond_branches_with_string_keys():
    # YAML can serialise bools as strings in some shapes; both forms must work.
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        'true': {'cpu': 32, 'mem': 256},
        'false': {'cpu': 16, 'mem': 64},
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '12'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 16, 'mem': 64}


# ---------------- sibling fields apply uniformly across branches ----------------


def test_cond_with_outside_sibling_field():
    # `disk` outside the cond applies to whichever branch fires.
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        False: {'cpu': 16, 'mem': 64},
        'disk': 400,
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '150'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 32, 'mem': 256, 'disk': 400}

    itervar = {'SAMPLE': 'B', 'sample.depth_gb': '5'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 16, 'mem': 64, 'disk': 400}


def test_cond_sibling_disk_with_arithmetic_substitution():
    # The disk field is a substituted-then-evaluated arithmetic expression;
    # reaches the same _eval_arithmetic path as today's task_spec inputs.
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        False: {'cpu': 16, 'mem': 64},
        'disk': '{sample.depth_gb} * 4 + 50',
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '12'}
    out = _resolve(ts_def, itervar=itervar)
    # 12 * 4 + 50 = 98
    assert out['disk'] == '98'
    assert out['cpu'] == 16


# ---------------- back-compat: existing usage still works ----------------


def test_cond_with_param_ref_legacy():
    # The old shape (cond resolves to a param value, matches a key) still works.
    ts_def = {
        'cond': '{params.numa}',
        True: {'numa': 4},
        False: {'cpu': 32, 'mem': 64},
    }
    out = _resolve(ts_def, params=SimpleNamespace(numa=True))
    assert out == {'numa': 4}
    out = _resolve(ts_def, params=SimpleNamespace(numa=False))
    assert out == {'cpu': 32, 'mem': 64}


def test_cond_with_enum_param_value_legacy():
    ts_def = {
        'cond': '{params.profile}',
        'big':   {'cpu': 32, 'mem': 256},
        'small': {'cpu': 4,  'mem': 8},
    }
    out = _resolve(ts_def, params=SimpleNamespace(profile='big'))
    assert out['cpu'] == 32
    out = _resolve(ts_def, params=SimpleNamespace(profile='small'))
    assert out['cpu'] == 4


def test_flat_task_spec_pass_through():
    # No cond, no surprises — flat dict values resolve and pass through.
    ts_def = {'cpu': 8, 'mem': 16, 'disk': 100}
    out = _resolve(ts_def)
    assert out == {'cpu': 8, 'mem': 16, 'disk': 100}


def test_flat_task_spec_with_param_substitution():
    ts_def = {'cpu': '{params.threads}', 'mem': 16}
    out = _resolve(ts_def, params=SimpleNamespace(threads=8))
    assert out['cpu'] == '8'
    assert out['mem'] == 16


# ---------------- composes with regex / boolean / membership ----------------


def test_cond_membership_picks_branch():
    ts_def = {
        'cond': "sample.platform in ('illumina', 'mgi')",
        True: {'cpu': 16, 'mem': 32},
        False: {'cpu': 8, 'mem': 16},
    }
    itervar = {'SAMPLE': 'A', 'sample.platform': 'illumina'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 16, 'mem': 32}

    itervar = {'SAMPLE': 'B', 'sample.platform': 'pacbio'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 8, 'mem': 16}


def test_cond_regex_match_picks_branch():
    ts_def = {
        'cond': "sample.fastq_path ~ '\\.bam$'",
        True: {'cpu': 32, 'mem': 128},
        False: {'cpu': 16, 'mem': 64},
    }
    itervar = {'SAMPLE': 'A', 'sample.fastq_path': '/data/sample1.bam'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 32, 'mem': 128}


def test_cond_boolean_compound():
    ts_def = {
        'cond': "sample.platform == 'illumina' and sample.depth_gb > 50",
        True: {'cpu': 32, 'mem': 128},
        False: {'cpu': 16, 'mem': 64},
    }
    itervar = {
        'SAMPLE': 'A', 'sample.platform': 'illumina', 'sample.depth_gb': '120'
    }
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 32, 'mem': 128}

    itervar['sample.depth_gb'] = '10'
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 16, 'mem': 64}


# ---------------- error case: unmatched cond raises ----------------


def test_cond_no_matching_branch_raises():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        # no False branch
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '12'}
    with pytest.raises(ValueError, match="cond: no match"):
        _resolve(ts_def, itervar=itervar)


def test_cond_with_default_fallback():
    ts_def = {
        'cond': 'sample.depth_gb > 100',
        True: {'cpu': 32, 'mem': 256},
        'default': {'cpu': 8, 'mem': 16},
    }
    itervar = {'SAMPLE': 'A', 'sample.depth_gb': '5'}
    out = _resolve(ts_def, itervar=itervar)
    assert out == {'cpu': 8, 'mem': 16}
