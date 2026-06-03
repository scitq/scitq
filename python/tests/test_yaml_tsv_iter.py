"""Tests for the TSV/CSV-driven iterator (spec: addition_from_nextflow.md C).

`source: tsv` reads tabular sample metadata; each row materialises one
iteration and columns become substitutable as `{<iter-name>.<col>}` and
accessible via attribute syntax (`<iter-name>.<col>`) in expression-aware
fields (`when:`, `cond:`, `task_spec` …).
"""
import os
import tempfile
import textwrap
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr


# ---------------- inline content path ----------------


def test_tsv_content_basic_iteration():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': textwrap.dedent("""\
            sample_name\tdepth_gb\tassembly_file
            A\t30\ts3://bucket/A.fa
            B\t12\ts3://bucket/B.fa
            C\t100\ts3://bucket/C.fa
        """),
    }
    items, src = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert src == 'tsv'
    assert len(items) == 3
    # Iter tag (first column by default)
    assert items[0]['SAMPLE'] == 'A'
    assert items[1]['SAMPLE'] == 'B'
    assert items[2]['SAMPLE'] == 'C'
    # Per-column dotted keys
    assert items[0]['sample.sample_name'] == 'A'
    assert items[0]['sample.depth_gb'] == '30'
    assert items[0]['sample.assembly_file'] == 's3://bucket/A.fa'


def test_tsv_csv_with_comma_separator():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'sep': ',',
        'content': "sample_name,n_reads\nA,500000\nB,1500000\n",
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert len(items) == 2
    assert items[0]['sample.n_reads'] == '500000'
    assert items[1]['sample.n_reads'] == '1500000'


def test_tsv_explicit_key_column():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'key': 'sample_id',
        'content': "label\tsample_id\ndataA\tS001\ndataB\tS002\n",
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert items[0]['SAMPLE'] == 'S001'
    assert items[1]['SAMPLE'] == 'S002'


def test_tsv_invalid_key_column():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'key': 'nonexistent',
        'content': "a\tb\n1\t2\n",
    }
    with pytest.raises(ValueError, match="key column 'nonexistent' not in"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_empty_content():
    # Just a header, no rows: legitimate (zero iterations).
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "a\tb\n",
    }
    items, src = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert items == []
    assert src is None  # zero-row tsv collapses to no-source signal


def test_tsv_no_header_rejected():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "",
    }
    with pytest.raises(ValueError, match="no header row"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_duplicate_key_rejected():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "id\tvalue\nA\tx\nA\ty\n",
    }
    with pytest.raises(ValueError, match="duplicate value 'A'"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_requires_uri_or_content():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
    }
    with pytest.raises(ValueError, match="exactly one of `uri:` or `content:`"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_rejects_both_uri_and_content():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'uri': '/tmp/x.tsv',
        'content': "a\tb\n1\t2\n",
    }
    with pytest.raises(ValueError, match="exactly one of `uri:` or `content:`"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_missing_cell_resolves_to_empty_string():
    # csv.DictReader returns None for missing trailing cells; we coerce
    # to empty string so `when: "{sample.preqc_done}"` reads as falsy.
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "a\tb\tc\nx\ty\n",   # third column missing on data row
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert items[0]['sample.c'] == ''


# ---------------- uri path (local file) ----------------


def test_tsv_uri_local_file(tmp_path):
    p = tmp_path / "samples.tsv"
    p.write_text("sample_name\tdepth_gb\nA\t30\nB\t12\n")
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'uri': str(p),
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert [it['SAMPLE'] for it in items] == ['A', 'B']
    assert items[0]['sample.depth_gb'] == '30'


def test_tsv_uri_csv_extension_auto_detects_comma(tmp_path):
    p = tmp_path / "samples.csv"
    p.write_text("sample_name,depth_gb\nA,30\nB,12\n")
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'uri': str(p),
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert [it['SAMPLE'] for it in items] == ['A', 'B']


def test_tsv_uri_remote_uri_rejected_in_v1():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'uri': 's3://bucket/samples.tsv',
    }
    with pytest.raises(ValueError, match="remote URI .* is not yet supported"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_uri_with_param_substitution(tmp_path):
    # The path is templated against params, just like other iterator inputs.
    p = tmp_path / "samples.tsv"
    p.write_text("sample_name\tdepth_gb\nA\t30\n")
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'uri': '{params.sheet}',
    }
    items, _ = yr._build_single_iterator(
        iter_def, params=SimpleNamespace(sheet=str(p)))
    assert items[0]['SAMPLE'] == 'A'


# ---------------- schema validation ----------------


def test_tsv_rejects_unknown_keys():
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "a\tb\n1\t2\n",
        'group_by': 'folder',  # not in tsv schema
    }
    with pytest.raises(ValueError, match="doesn't recognise"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


# ---------------- F' namespace synthesis from dotted keys ----------------


def test_build_eval_locals_synthesises_namespace_from_dotted_keys():
    itervar = {
        'SAMPLE': 'A',
        'sample.depth_gb': '30',
        'sample.assembly_file': 's3://bucket/A.fa',
    }
    locs = yr._build_eval_locals(itervar=itervar)
    assert 'sample' in locs
    assert locs['sample'].depth_gb == '30'
    assert locs['sample'].assembly_file == 's3://bucket/A.fa'


def test_expression_uses_synthesised_namespace_for_attribute_access():
    itervar = {
        'SAMPLE': 'A',
        'sample.depth_gb': '150',
    }
    # Numeric comparison through the SmartStr-wrapped attribute.
    assert yr._eval_template_expression(
        "sample.depth_gb > 100", itervar=itervar) is True
    assert yr._eval_template_expression(
        "sample.depth_gb < 100", itervar=itervar) is False


def test_expression_string_attribute_equality():
    itervar = {
        'SAMPLE': 'A',
        'sample.read_type': 'long',
    }
    assert yr._eval_template_expression(
        "sample.read_type == 'long'", itervar=itervar) is True
    assert yr._eval_template_expression(
        "sample.read_type == 'short'", itervar=itervar) is False


def test_expression_membership_against_namespace():
    itervar = {
        'SAMPLE': 'A',
        'sample.platform': 'illumina',
    }
    assert yr._eval_template_expression(
        "sample.platform in ('illumina', 'pacbio')", itervar=itervar) is True


def test_expression_regex_against_namespace():
    itervar = {
        'SAMPLE': 'A',
        'sample.path': '/data/sample1.bam',
    }
    assert yr._eval_template_expression(
        "sample.path ~ '\\.bam$'", itervar=itervar) is True


# ---------------- substitution path still works ----------------


def test_substitution_of_dotted_iter_key_works_via_resolve_refs():
    # `{sample.depth_gb}` in a command must substitute to the value.
    # _resolve_refs treats the dotted form as a flat key lookup.
    itervar = {
        'SAMPLE': 'A',
        'sample.depth_gb': '30',
    }
    out = yr._resolve_refs("depth={sample.depth_gb}", params=SimpleNamespace(), itervar=itervar)
    assert out == "depth=30"
