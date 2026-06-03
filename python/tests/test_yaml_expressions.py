"""Tests for the extended expression evaluator (spec: addition_from_nextflow.md F').

The evaluator widens what `when:` and `cond:` accept beyond the original
arithmetic-only `_eval_arithmetic`: comparison operators (`== != < <= > >=`),
regex match (`~`), boolean (`and`/`or`/`not`), membership (`in`/`not in`),
plus auto-coercion of numeric-looking strings via `_SmartStr`.
"""
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr


# ---------- helpers ----------


def _ev(template, params=None, itervar=None, extra_vars=None):
    return yr._eval_template_expression(template, params, itervar, extra_vars)


# ---------- comparison operators ----------


def test_equality_string_against_quoted_literal():
    itervar = {'SAMPLE': 'long'}
    assert _ev("SAMPLE == 'long'", itervar=itervar) is True
    assert _ev("SAMPLE == 'short'", itervar=itervar) is False


def test_inequality_operator():
    itervar = {'SAMPLE': 'long'}
    assert _ev("SAMPLE != 'short'", itervar=itervar) is True
    assert _ev("SAMPLE != 'long'", itervar=itervar) is False


def test_numeric_comparison_against_int_literal():
    itervar = {'N_READS': '1500000'}
    assert _ev("N_READS >= 1_000_000", itervar=itervar) is True
    assert _ev("N_READS < 1_000_000", itervar=itervar) is False
    assert _ev("N_READS > 1_000_000", itervar=itervar) is True


def test_numeric_comparison_against_float_literal():
    itervar = {'DEPTH_GB': '3.14'}
    assert _ev("DEPTH_GB > 3.0", itervar=itervar) is True
    assert _ev("DEPTH_GB <= 3.0", itervar=itervar) is False


def test_numeric_comparison_non_numeric_string_falls_back_to_str_order():
    # Non-numeric string: comparison stays string-typed, no coercion.
    itervar = {'LABEL': 'beta'}
    assert _ev("LABEL < 'gamma'", itervar=itervar) is True
    assert _ev("LABEL > 'alpha'", itervar=itervar) is True


# ---------- boolean operators ----------


def test_boolean_and_or_not():
    itervar = {'PLATFORM': 'illumina', 'N': '50'}
    assert _ev("PLATFORM == 'illumina' and N > 10", itervar=itervar) is True
    assert _ev("PLATFORM == 'pacbio' or N > 10", itervar=itervar) is True
    assert _ev("not (N > 100)", itervar=itervar) is True


# ---------- membership ----------


def test_in_operator_with_tuple():
    params = SimpleNamespace(profile='full')
    assert _ev("params.profile in ('full', 'extended')", params=params) is True
    assert _ev("params.profile in ('minimal', 'lite')", params=params) is False


def test_not_in_operator():
    params = SimpleNamespace(profile='minimal')
    assert _ev("params.profile not in ('full', 'extended')", params=params) is True


# ---------- regex match (~) ----------


def test_regex_match_basic_anchored():
    itervar = {'PATH': '/data/sample1.bam'}
    assert _ev("PATH ~ '\\.bam$'", itervar=itervar) is True
    assert _ev("PATH ~ '\\.fastq$'", itervar=itervar) is False


def test_regex_match_with_quantifier():
    itervar = {'PLATFORM': 'illumina-novaseq-x'}
    assert _ev("PLATFORM ~ '^illumina'", itervar=itervar) is True
    assert _ev("PLATFORM ~ 'pacbio'", itervar=itervar) is False


def test_regex_match_negated_via_not():
    itervar = {'PLATFORM': 'pacbio'}
    assert _ev("not (PLATFORM ~ '^illumina')", itervar=itervar) is True


# ---------- params (Namespace) access ----------


def test_params_attribute_access():
    params = SimpleNamespace(oral=True, n_threads=8)
    assert _ev("params.oral", params=params) is True
    assert _ev("params.n_threads > 4", params=params) is True
    assert _ev("params.n_threads * 2", params=params) == 16


def test_params_with_outer_braces_is_stripped():
    # Cosmetic compatibility: existing workflows often write
    # `when: "{params.oral}"`. The outer braces should be transparent.
    params = SimpleNamespace(oral=True)
    assert _ev("{params.oral}", params=params) is True


# ---------- arithmetic preserved ----------


def test_arithmetic_still_works():
    itervar = {'CPU': '4'}
    assert _ev("CPU + 4", itervar=itervar) == 8
    assert _ev("max(CPU, 8)", itervar=itervar) == 8
    assert _ev("int(CPU) * 16", itervar=itervar) == 64


# ---------- fallbacks (non-expression input flows through) ----------


def test_non_expression_string_returns_as_is():
    # A bare literal that isn't a valid expression nor a known name flows
    # through unchanged — the safety hatch for non-expression inputs like
    # paths, URLs, opaque tags.
    assert _ev("not an expression at all") == "not an expression at all"
    assert _ev("/some/path/to/file.txt") == "/some/path/to/file.txt"


def test_undefined_name_returns_original_expr():
    # Reference to a name that wasn't bound. We reject (the AST walker
    # rejects unauthorised names) and return the original.
    assert _ev("foo + bar") == "foo + bar"


def test_empty_string_returns_empty_string():
    assert _ev("") == ""


def test_call_to_disallowed_function_is_rejected():
    # eval/exec/open/etc. — even if user managed to type one, it's rejected
    # at the AST walk and the original string flows through.
    assert _ev("__import__('os')") == "__import__('os')"
    assert _ev("open('/etc/passwd')") == "open('/etc/passwd')"


# ---------- _looks_like_expression heuristic ----------


def test_looks_like_expression_recognises_operators():
    assert yr._looks_like_expression("a == 'b'") is True
    assert yr._looks_like_expression("x >= 10") is True
    assert yr._looks_like_expression("a and b") is True
    assert yr._looks_like_expression("x in (1, 2)") is True
    assert yr._looks_like_expression("path ~ '\\.bam$'") is True


def test_looks_like_expression_skips_bare_refs():
    # Single ref shapes should NOT be treated as expressions — they keep
    # going through the existing template-substitution path.
    assert yr._looks_like_expression("params.oral") is False
    assert yr._looks_like_expression("{params.oral}") is False
    assert yr._looks_like_expression("SAMPLE") is False
    assert yr._looks_like_expression("metaphlan_4.0") is False


# ---------- SmartStr behaviour ----------


def test_smartstr_equality_stays_string_typed():
    s = yr._SmartStr("42")
    assert s == "42"
    assert s != 42  # no coercion for ==


def test_smartstr_ordering_coerces_against_number():
    s = yr._SmartStr("42")
    assert s < 100
    assert s > 10
    assert s <= 42
    assert s >= 42


def test_smartstr_ordering_against_string_stays_string():
    s = yr._SmartStr("apple")
    assert s < "banana"
    assert s > "almond"


def test_smartstr_with_python_digit_separators():
    s = yr._SmartStr("1_000_000")
    assert s >= 1_000_000
    assert s < 2_000_000


# ---------- preprocessor for `~` ----------


def test_preprocess_regex_match_simple():
    assert yr._preprocess_regex_match("a ~ 'b'") == "__re_match__(a, 'b')"


def test_preprocess_regex_match_with_dotted_name():
    out = yr._preprocess_regex_match("sample.path ~ '\\.bam$'")
    assert out == "__re_match__(sample.path, '\\.bam$')"


def test_preprocess_regex_match_unchanged_when_no_tilde():
    s = "params.profile in ('full', 'extended')"
    assert yr._preprocess_regex_match(s) == s


# ---------- cond block integration ----------


def test_cond_block_with_expression_picks_true_branch():
    # cond: <expr> with true/false branches.
    val = {
        'cond': "params.depth_gb > 100",
        'true': 'big',
        'false': 'small',
    }
    params = SimpleNamespace(depth_gb=150)
    assert yr._resolve_cond(val, params) == 'big'


def test_cond_block_with_expression_picks_false_branch():
    val = {
        'cond': "params.depth_gb > 100",
        'true': 'big',
        'false': 'small',
    }
    params = SimpleNamespace(depth_gb=12)
    assert yr._resolve_cond(val, params) == 'small'


def test_cond_block_with_membership_expression():
    val = {
        'cond': "params.profile in ('full', 'extended')",
        'true': 'thorough',
        'false': 'quick',
    }
    params = SimpleNamespace(profile='extended')
    assert yr._resolve_cond(val, params) == 'thorough'


def test_legacy_cond_block_with_param_ref_still_works():
    # The old single-ref `cond:` shape must keep working unchanged.
    val = {
        'cond': "{params.oral}",
        True: 'has_oral',
        False: 'no_oral',
    }
    params = SimpleNamespace(oral=True)
    assert yr._resolve_cond(val, params) == 'has_oral'

    params = SimpleNamespace(oral=False)
    assert yr._resolve_cond(val, params) == 'no_oral'


def test_legacy_cond_block_with_enum_value():
    # `cond:` matching against a specific enum value (e.g. metaphlan version).
    val = {
        'cond': "{params.metaphlan_index}",
        'mpa_vJun23': 'four',
        'mpa_vOct22': 'three',
    }
    params = SimpleNamespace(metaphlan_index='mpa_vJun23')
    assert yr._resolve_cond(val, params) == 'four'
