"""Iterator-level `cond:` dispatch tests.

Templates often need to expose a parameter that switches the iterator
source (e.g. `data_source=uri` vs `data_source=list`). The top-level
iter block holds shared attributes (`name`, `match`, …) and per-source
branches under the cond. The runner picks one branch and inherits the
shared attributes — but only those the chosen branch's source can
actually use.

Regression context: blindly inheriting `match` into every branch tripped
the schema validator on the `lines` source ("doesn't recognise
['match']") because `match` only applies to file-group sources
(uri/ena/sra) where it filters discovered sample names via fnmatch.
The fix in yaml_runner._build_iterations gates the `match` inheritance
on the chosen branch's source being in FILE_GROUP_SOURCES.
"""
import types

from scitq2.yaml_runner import _build_iterations


def _params(**kw):
    return types.SimpleNamespace(**kw)


def test_match_does_not_leak_into_lines_branch():
    """The original failure: `match` set at the top + cond picks `lines`
    → `_build_single_iterator` raised "Iterator 'sample' (source='lines')
    doesn't recognise ['match']". The fix: only inherit `match` into
    branches whose source supports it (FILE_GROUP_SOURCES)."""
    iterate_def = {
        "name": "sample",
        "match": "SAMEA*",  # only meaningful for uri/ena/sra
        "cond": "{params.data_source}",
        "uri": {
            "source": "uri",
            "uri": "s3://example/",
            "group_by": "folder",
            "contigs": "*.contigs.fa",
        },
        "list": {
            "source": "lines",
            "content": "{params.sample_list}",
            "item": "contigs",
            "tag": "folder",
        },
    }
    # Should NOT raise. Content has a single line → one iteration.
    iterations, source_type = _build_iterations(
        iterate_def,
        _params(
            data_source="list",
            sample_list="s3://rnd/results/SAMEA113548013/*.contigs.fa",
        ),
    )
    assert source_type == "lines"
    assert len(iterations) == 1


def test_name_still_inherits_into_lines_branch():
    """`name` is the iteration-variable identifier — every source uses
    it. Make sure we didn't accidentally restrict that inheritance when
    tightening up `match`."""
    iterate_def = {
        "name": "sample",
        "cond": "{params.data_source}",
        "list": {
            "source": "lines",
            "content": "{params.sample_list}",
            "item": "contigs",
            "tag": "folder",
        },
    }
    iterations, source_type = _build_iterations(
        iterate_def,
        _params(
            data_source="list",
            sample_list="s3://rnd/x/SAMEA1/*.fa\ns3://rnd/x/SAMEA2/*.fa",
        ),
    )
    assert source_type == "lines"
    assert len(iterations) == 2


def test_match_inheritance_skipped_for_lines_even_if_uri_branch_absent():
    """The match-inheritance check looks at the *resolved* branch's
    source, not whether the parent had a uri branch. A workflow that
    only ever uses lines (but happens to inherit `match` from a
    template helper) shouldn't trip the schema validator."""
    iterate_def = {
        "name": "sample",
        "match": "SAMEA*",
        "cond": "{params.data_source}",
        "list": {
            "source": "lines",
            "content": "{params.sample_list}",
            "item": "contigs",
        },
    }
    iterations, source_type = _build_iterations(
        iterate_def,
        _params(data_source="list", sample_list="a/b/c.fa"),
    )
    assert source_type == "lines"
    assert len(iterations) == 1
