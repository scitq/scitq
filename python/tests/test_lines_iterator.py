"""Tests for the extended `lines` iterator (content/item/tag) and the
`file_content` param type — the wiring behind the
`--param sample_list=@local-file.txt` flow used by megahit_assembly.

Three layers exercised:
  1. _derive_lines_tag — pure function, easy to unit-test.
  2. file_content param round-trip through Param.parse.
  3. _build_single_iterator with `source: lines`, both bare and
     item-mode (URI.find monkey-patched so the test doesn't touch the
     network or a real bucket).
"""
import os
import tempfile

import pytest

from scitq2 import yaml_runner
from scitq2.param import Param, ParamSpec, FileContent


# ---------------------------------------------------------------------------
# _derive_lines_tag
# ---------------------------------------------------------------------------

def test_derive_lines_tag_uses_fallback_when_provided():
    # URI.find resolved the glob and gave us the folder name back —
    # that's the authoritative source for the tag.
    assert yaml_runner._derive_lines_tag(
        line="s3://rnd/raw/SCAPIS/ERR123/*.fastq.gz",
        uris=["s3://rnd/raw/SCAPIS/ERR123/ERR123_1.fastq.gz"],
        kind="folder",
        fallback="ERR123",
    ) == "ERR123"


def test_derive_lines_tag_uses_first_uri_when_no_fallback():
    # Empty fallback but URIs present — derive parent folder from
    # the first matched URI.
    tag = yaml_runner._derive_lines_tag(
        line="ignored",
        uris=["s3://rnd/raw/SCAPIS/ERR456/ERR456_1.fastq.gz"],
        kind="folder",
        fallback="",
    )
    assert tag == "ERR456"


def test_derive_lines_tag_strips_glob_from_line_when_no_uris():
    # No URIs at all — fall back to the line, stripping any glob suffix.
    tag = yaml_runner._derive_lines_tag(
        line="s3://rnd/raw/SCAPIS/ERR789/*.fastq.gz",
        uris=[],
        kind=None,
        fallback="",
    )
    assert tag == "ERR789"


def test_derive_lines_tag_no_uris_no_glob_returns_basename():
    tag = yaml_runner._derive_lines_tag(
        line="s3://bucket/path/SAMPLE",
        uris=[],
        kind="folder",
        fallback="",
    )
    assert tag == "SAMPLE"


def test_derive_lines_tag_rejects_unknown_kind():
    with pytest.raises(ValueError, match="Unsupported `tag:` value"):
        yaml_runner._derive_lines_tag("x", ["uri"], kind="basename", fallback="x")


# ---------------------------------------------------------------------------
# file_content param round-trip
# ---------------------------------------------------------------------------

def test_file_content_param_round_trip():
    """A multi-line string ships through Param.parse as a FileContent value
    (a str subclass) — the runner downstream treats it like a string."""
    class P(metaclass=ParamSpec):
        manifest = Param.file_content(required=True, help="t")

    payload = "line1\nline2 with spaces\nline3\n"
    parsed = P.parse({"manifest": payload})
    val = parsed._values["manifest"]
    assert isinstance(val, FileContent)
    assert isinstance(val, str)  # for downstream code that doesn't care about the marker
    assert val == payload


def test_file_content_schema_exposes_type():
    class P(metaclass=ParamSpec):
        manifest = Param.file_content(required=False, help="hi")

    schema = P.schema()
    entry = next(p for p in schema if p["name"] == "manifest")
    assert entry["type"] == "file_content"
    assert entry["required"] is False
    assert entry["help"] == "hi"


# ---------------------------------------------------------------------------
# lines iterator — bare mode (no item:) preserves existing behavior
# ---------------------------------------------------------------------------

def _empty_params():
    """Shared empty ParamSpec for iterator tests — _resolve_refs needs
    something with a _values dict even when no params are referenced."""
    class P(metaclass=ParamSpec):
        pass
    return P.parse({})


def test_lines_bare_from_file_yields_one_iter_per_line(tmp_path):
    f = tmp_path / "samples.txt"
    f.write_text("sampleA\nsampleB\n\nsampleC\n")
    iter_def = {"name": "sample", "source": "lines", "file": str(f)}
    items, source = yaml_runner._build_single_iterator(iter_def, _empty_params())
    assert source is None  # bare mode has no file groups → no source tagging
    assert [it["SAMPLE"] for it in items] == ["sampleA", "sampleB", "sampleC"]


def test_lines_bare_from_content_yields_one_iter_per_line():
    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": "x1\n  x2  \n\nx3\n",
    }
    items, _ = yaml_runner._build_single_iterator(iter_def, _empty_params())
    # Whitespace stripped, blank lines dropped.
    assert [it["SAMPLE"] for it in items] == ["x1", "x2", "x3"]


def test_lines_rejects_both_file_and_content(tmp_path):
    f = tmp_path / "s.txt"
    f.write_text("a\n")
    iter_def = {
        "name": "sample",
        "source": "lines",
        "file": str(f),
        "content": "a\nb",
    }
    with pytest.raises(ValueError, match="exactly one of"):
        yaml_runner._build_single_iterator(iter_def, _empty_params())


def test_lines_rejects_neither_file_nor_content():
    iter_def = {"name": "sample", "source": "lines"}
    with pytest.raises(ValueError, match="exactly one of"):
        yaml_runner._build_single_iterator(iter_def, _empty_params())


# ---------------------------------------------------------------------------
# lines iterator — item mode (URI.find monkey-patched to avoid network)
# ---------------------------------------------------------------------------

class _FakeURIObject:
    """Minimal duck-type stand-in for URIObject — same dynamic attribute
    shape (sample_accession + fastqs) that the URI iterator emits today."""
    def __init__(self, sample_accession, fastqs):
        self.sample_accession = sample_accession
        self.fastqs = list(fastqs)


def _patch_uri_find(monkeypatch, mapping):
    """Make URI.find return entries from a {line -> [URIObject, ...]} dict."""
    from scitq2 import uri

    def fake_find(line, group_by=None, pattern=None, filter=None,
                  event_name=None, field_map=None):
        return list(mapping.get(line, []))

    monkeypatch.setattr(uri.URI, "find", staticmethod(fake_find))


def test_lines_item_mode_attaches_file_group_and_tag(monkeypatch):
    """Each line resolves to one sample folder; the resolved URIs land
    in sample.<item> and the tag is the folder name (default when item:
    is set)."""
    line_a = "s3://bucket/SAMP_A/*.fq.gz"
    line_b = "s3://bucket/SAMP_B/*.fq.gz"
    _patch_uri_find(monkeypatch, {
        line_a: [_FakeURIObject("SAMP_A", [
            "s3://bucket/SAMP_A/SAMP_A_1.fq.gz",
            "s3://bucket/SAMP_A/SAMP_A_2.fq.gz",
        ])],
        line_b: [_FakeURIObject("SAMP_B", [
            "s3://bucket/SAMP_B/SAMP_B_1.fq.gz",
            "s3://bucket/SAMP_B/SAMP_B_2.fq.gz",
        ])],
    })

    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": f"{line_a}\n{line_b}\n",
        "item": "fastqs",
    }
    items, source = yaml_runner._build_single_iterator(iter_def, _empty_params())

    assert source == "lines"
    assert len(items) == 2
    assert [it["SAMPLE"] for it in items] == ["SAMP_A", "SAMP_B"]
    # The named file group is what `inputs: sample.fastqs` will resolve to.
    for it, expected in zip(items, [
        ["s3://bucket/SAMP_A/SAMP_A_1.fq.gz", "s3://bucket/SAMP_A/SAMP_A_2.fq.gz"],
        ["s3://bucket/SAMP_B/SAMP_B_1.fq.gz", "s3://bucket/SAMP_B/SAMP_B_2.fq.gz"],
    ]):
        assert it["_sample"].file_groups["fastqs"] == expected


def test_lines_item_mode_skips_empty_glob_with_warning(monkeypatch, capsys):
    """A line whose glob resolves to nothing logs a warning and produces
    no iteration. Same behavior the URI iterator wouldn't have, but
    surfaced here because the user explicitly provided the line —
    silence would mask a typo."""
    bad_line = "s3://bucket/typo_*/*.fq.gz"
    good_line = "s3://bucket/SAMP_C/*.fq.gz"
    _patch_uri_find(monkeypatch, {
        bad_line: [],
        good_line: [_FakeURIObject("SAMP_C", ["s3://bucket/SAMP_C/SAMP_C_1.fq.gz"])],
    })

    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": f"{bad_line}\n{good_line}\n",
        "item": "fastqs",
    }
    items, _ = yaml_runner._build_single_iterator(iter_def, _empty_params())
    captured = capsys.readouterr()
    assert "matched no files" in captured.err
    assert len(items) == 1
    assert items[0]["SAMPLE"] == "SAMP_C"


def test_lines_item_mode_multiple_folders_per_line(monkeypatch):
    """A glob that resolves across N folders yields N iterations — one
    task per folder, each with that folder's matching files."""
    line = "s3://bucket/sample_*/*.fq.gz"
    _patch_uri_find(monkeypatch, {
        line: [
            _FakeURIObject("sample_1", ["s3://bucket/sample_1/sample_1.fq.gz"]),
            _FakeURIObject("sample_2", ["s3://bucket/sample_2/sample_2.fq.gz"]),
            _FakeURIObject("sample_3", ["s3://bucket/sample_3/sample_3.fq.gz"]),
        ],
    })

    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": line + "\n",
        "item": "fastqs",
    }
    items, _ = yaml_runner._build_single_iterator(iter_def, _empty_params())
    assert len(items) == 3
    assert [it["SAMPLE"] for it in items] == ["sample_1", "sample_2", "sample_3"]


# ---------------------------------------------------------------------------
# Schema validation — make sure 'content', 'item', 'tag' don't trip the
# strict iter-key checker we added earlier.
# ---------------------------------------------------------------------------

def test_lines_iterator_schema_accepts_new_keys():
    """The strict ITERATOR_SCHEMAS validator (in _build_single_iterator
    on line ~415) should accept content/item/tag without raising."""
    assert "content" in yaml_runner.ITERATOR_SCHEMAS["lines"]
    assert "item" in yaml_runner.ITERATOR_SCHEMAS["lines"]
    assert "tag" in yaml_runner.ITERATOR_SCHEMAS["lines"]
