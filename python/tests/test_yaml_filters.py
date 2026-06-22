"""Unit tests for YAML placeholder filters (`{var|filter:arg}`).

Filters live in `scitq2.yaml_runner._apply_filter` and are reached via
`_resolve_refs` when a `{...}` placeholder is being expanded. Tests
cover the wire-level filter function directly *and* the
end-to-end-via-placeholder path, since the placeholder regex and the
`|`/`:` splitting are part of the contract too — see
test_strip_via_placeholder_regex_accepts_slash for the slash-in-arg
case that motivated this filter.
"""
from types import SimpleNamespace

from scitq2.yaml_runner import _apply_filter, _resolve_refs


# ---------------------------------------------------------------------------
# strip filter — direct _apply_filter tests
# ---------------------------------------------------------------------------

def test_strip_no_arg_strips_whitespace_both_ends():
    """Default form: matches Python str.strip() — space, tab, \n, \r."""
    assert _apply_filter("  hello  ", "strip") == "hello"
    assert _apply_filter("\thello\n", "strip") == "hello"
    assert _apply_filter("hello", "strip") == "hello"
    assert _apply_filter("   ", "strip") == ""


def test_strip_with_single_char_arg():
    """`strip:/` removes trailing/leading slashes — the URI normalisation
    use case (e.g. `s3://bucket/path/` → `s3://bucket/path`)."""
    assert _apply_filter("/foo/", "strip:/") == "foo"
    assert _apply_filter("s3://bucket/path/", "strip:/") == "s3://bucket/path"
    assert _apply_filter("foo", "strip:/") == "foo"
    # str.strip(chars) only touches the ends — internal slashes survive.
    assert _apply_filter("/foo/bar/", "strip:/") == "foo/bar"


def test_strip_with_multi_char_set_arg():
    """`strip:./` follows Python semantics: each listed char is in the
    strip set, NOT the literal substring "./". So "./foo./" → "foo"
    (both '.' and '/' get stripped from both ends)."""
    assert _apply_filter("./foo./", "strip:./") == "foo"
    assert _apply_filter(".../foo...", "strip:./") == "foo"
    assert _apply_filter("/.foo./", "strip:./") == "foo"


def test_strip_equals_form_matches_colon_form():
    """Mirror `format=…` style — `=` and `:` are interchangeable separators."""
    assert _apply_filter("/foo/", "strip=/") == "foo"
    assert _apply_filter("  hi  ", "strip=") == "hi"


def test_strip_empty_arg_falls_back_to_whitespace():
    """`strip:` (empty arg) behaves like the no-arg form. Guards against
    silent str.strip('') which is a no-op in Python."""
    assert _apply_filter("  hi  ", "strip:") == "hi"
    assert _apply_filter("  hi  ", "strip=") == "hi"


def test_strip_preserves_internal_characters():
    """Sanity: only the ends are touched, internal chars are inviolate."""
    assert _apply_filter("//a//b//", "strip:/") == "a//b"
    assert _apply_filter("xxabcxx", "strip:x") == "abc"


# ---------------------------------------------------------------------------
# Placeholder-regex integration — confirms `/` survives the outer parser
# all the way to _apply_filter (the question that motivated this work).
# ---------------------------------------------------------------------------

def test_strip_via_placeholder_regex_accepts_slash():
    """End-to-end: `{params.uri|strip:/}` parses through the placeholder
    regex (`[^\\s\\'"{},]+`), `|` split, and `:` filter-arg handling
    without snagging on the slash."""
    params = SimpleNamespace(uri="s3://bucket/path/")
    assert _resolve_refs("{params.uri|strip:/}", params) == "s3://bucket/path"


def test_strip_via_placeholder_default_form():
    """`{params.x|strip}` — no arg, no colon — should still resolve."""
    params = SimpleNamespace(x="  spaced  ")
    assert _resolve_refs("{params.x|strip}", params) == "spaced"


def test_strip_composes_with_other_filters():
    """Filters chain left-to-right. `strip` should compose with the
    existing `lower`/`upper` filters without surprises."""
    params = SimpleNamespace(x="  HELLO  ")
    assert _resolve_refs("{params.x|strip|lower}", params) == "hello"
    assert _resolve_refs("{params.x|lower|strip}", params) == "hello"


def test_strip_with_arg_after_default():
    """The `:` in the filter arg must not collide with the `:default`
    syntax used for the ref part. `{params.foo:bar|strip:/}` should
    apply the default "bar" to params.foo, then strip "/" from "bar".
    Since "bar" has no slashes, it stays "bar"."""
    params = SimpleNamespace()  # no `foo` attribute → falls back to default
    assert _resolve_refs("{params.foo:bar|strip:/}", params) == "bar"


def test_strip_unknown_filter_still_passes_through():
    """Regression: adding `strip` shouldn't have side effects on
    unrelated filters. An unknown filter returns the value unchanged."""
    assert _apply_filter("foo", "totallyunknown") == "foo"
