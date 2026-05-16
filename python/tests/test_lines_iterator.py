"""Tests for the extended `lines` iterator (content/item/tag) and the
`text` param type — the wiring behind the
`--param sample_list=@local-file.txt` flow used by megahit_assembly.

Three layers exercised:
  1. _derive_lines_tag — pure function, easy to unit-test.
  2. text param round-trip through Param.parse.
  3. _build_single_iterator with `source: lines`, both bare and
     item-mode (each line passed through literally — worker-side
     glob expansion).
"""
import os
import tempfile

import pytest

from scitq2 import yaml_runner
from scitq2.param import Param, ParamSpec, Text


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
# text param round-trip
# ---------------------------------------------------------------------------

def test_text_param_round_trip():
    """A multi-line string ships through Param.parse as a Text value
    (a str subclass) — the runner downstream treats it like a string."""
    class P(metaclass=ParamSpec):
        manifest = Param.text(required=True, help="t")

    payload = "line1\nline2 with spaces\nline3\n"
    parsed = P.parse({"manifest": payload})
    val = parsed._values["manifest"]
    assert isinstance(val, Text)
    assert isinstance(val, str)  # for downstream code that doesn't care about the marker
    assert val == payload


def test_text_schema_exposes_type():
    class P(metaclass=ParamSpec):
        manifest = Param.text(required=False, help="hi")

    schema = P.schema()
    entry = next(p for p in schema if p["name"] == "manifest")
    assert entry["type"] == "text"
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

def test_lines_item_mode_passes_glob_through():
    """Each line is passed through *literally* as the file group's value;
    no submission-time URI.find / S3 list happens. The worker's downloader
    expands the glob at task-start. Tag derived from the line's parent
    folder (the leaf folder before the glob)."""
    line_a = "s3://bucket/SAMP_A/*.fq.gz"
    line_b = "s3://bucket/SAMP_B/*.fq.gz"
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
    # The file group contains the *literal* glob — exactly what gets
    # stored on task.inputs. The worker expands at download time.
    assert items[0]["_sample"].file_groups["fastqs"] == [line_a]
    assert items[1]["_sample"].file_groups["fastqs"] == [line_b]


def test_lines_item_mode_no_uri_find_call(monkeypatch):
    """Submission must not call URI.find — that's the whole point of the
    pass-through design. Sanity check so future code changes don't
    accidentally reintroduce per-line S3 listing."""
    from scitq2 import uri

    called = []
    def fail_find(*args, **kwargs):
        called.append((args, kwargs))
        raise AssertionError("URI.find must not be called in lines+item: pass-through mode")
    monkeypatch.setattr(uri.URI, "find", staticmethod(fail_find))

    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": "s3://b/SAMP/*.fq.gz\n",
        "item": "fastqs",
    }
    items, _ = yaml_runner._build_single_iterator(iter_def, _empty_params())
    assert len(items) == 1
    assert called == []  # belt + braces


def test_lines_item_mode_literal_uri_no_glob():
    """A line without a glob is also passed through verbatim. The
    worker's downloader handles literal URIs natively (it already does
    for the URI iterator output)."""
    line = "s3://bucket/SAMP_X/SAMP_X_1.fq.gz"
    iter_def = {
        "name": "sample",
        "source": "lines",
        "content": line + "\n",
        "item": "fastqs",
    }
    items, _ = yaml_runner._build_single_iterator(iter_def, _empty_params())
    assert len(items) == 1
    assert items[0]["_sample"].file_groups["fastqs"] == [line]


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
