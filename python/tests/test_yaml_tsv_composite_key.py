"""Tests for the composite-key extension to the `source: tsv`
iterator + the per-iter→grouped_by build-order fix that unlocks the
multi-coverage binning shape.

Two primitives co-shipped here because they're useless apart:
  - `key: [colA, colB]` on a TSV iterator produces a per-row tag of
    the form "A.B" and exposes A and B as both `{iter.colA}` and
    `{COLA}` aliases.
  - YAML runner builds grouped_by steps without step-reference inputs
    BEFORE the per-iter loop, so per-pair downstream tasks can resolve
    per-key upstream outputs (split_contigs grouped_by ref, sketched
    upstream of per-pair fairy coverage).

Together these let `binning_fairy_multicov.yaml` express
the (ref × query) Cartesian shape from Florian's Nextflow.
"""
from types import SimpleNamespace
import pytest
import yaml

from scitq2 import yaml_runner as yr


# ---------------- composite key in _build_single_iterator ----------------


def test_tsv_list_key_synthesises_dot_joined_tag():
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tA\nA\tB\nB\tA\nB\tB\n",
        'key': ['ref', 'query'],
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert len(items) == 4
    tags = [it['PAIR'] for it in items]
    assert tags == ['A.A', 'A.B', 'B.A', 'B.B']


def test_tsv_list_key_exposes_columns_as_uppercase_aliases():
    # Composite-key columns must be accessible both via the dotted
    # form (`{pair.ref}`) AND as a top-level uppercase alias (`{REF}`)
    # — the latter is what `grouped_by: ref` looks for in the iter
    # var dict, and what `{REF}` template substitution resolves to.
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tX\nB\tY\n",
        'key': ['ref', 'query'],
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert items[0]['pair.ref'] == 'A'
    assert items[0]['pair.query'] == 'X'
    assert items[0]['REF'] == 'A'
    assert items[0]['QUERY'] == 'X'


def test_tsv_single_string_key_unchanged():
    # Single-column key is the historical case — must keep producing
    # plain tags and NOT inject the uppercase aliases (single-column
    # iterators address the key column via `{SAMPLE}` already).
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "name\tdepth\nS01\t10\nS02\t20\n",
        'key': 'name',
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert [it['SAMPLE'] for it in items] == ['S01', 'S02']
    assert 'NAME' not in items[0]   # no extra alias for single-key


def test_tsv_list_key_rejects_non_existent_column():
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tB\n",
        'key': ['ref', 'missing'],
    }
    with pytest.raises(ValueError, match="column 'missing' not in"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_list_key_rejects_empty_list():
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tB\n",
        'key': [],
    }
    with pytest.raises(ValueError, match="key list must be non-empty"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_list_key_catches_composite_duplicates():
    # Two rows with the SAME (ref, query) pair must error — the
    # composite tag is the uniqueness boundary, not the source row.
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tB\nA\tB\n",
        'key': ['ref', 'query'],
    }
    with pytest.raises(ValueError, match="duplicate composite tag 'A.B'"):
        yr._build_single_iterator(iter_def, params=SimpleNamespace())


def test_tsv_list_key_allows_repeated_single_column():
    # ref appears twice ('A','A'), query appears twice ('B','C'), but
    # the PAIR is unique. This is the Florian-mapping-file shape.
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\nA\tB\nA\tC\n",
        'key': ['ref', 'query'],
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert len(items) == 2
    assert [it['PAIR'] for it in items] == ['A.B', 'A.C']


# ---------------- multicov YAML end-to-end (dry-run) ----------------


MULTICOV_YAML = """
format: 2
name: tst-multicov
version: 0.0.1

params:
  input_uri:
    type: string
    required: true
  output_uri:
    type: string
    required: true
  mappings_tsv:
    type: text
    required: true
  location:
    type: provider_region
    required: true

iterate:
  name: pair
  source: tsv
  content: "{params.mappings_tsv}"
  key: [ref, query]

workspace: "{params.location}"
language: bash

steps:
  # grouped_by step at the TOP — must build BEFORE the per-iter
  # coverage step below so per-pair tasks can resolve split.split.
  - name: split
    container: alpine
    grouped_by: ref
    inputs:
      - "{params.input_uri}/{pair.ref}/asm.fa"
    command: "echo split {PAIR.REF} > /output/{PAIR.REF}.split.txt"
    outputs:
      out: "*.split.txt"
    task_spec:
      cpu: 1
      mem: 2

  - name: cov
    container: alpine
    inputs:
      - split.out
    command: "echo cov {PAIR} > /output/{PAIR}.cov.txt"
    outputs: { out: "*.cov.txt" }
    task_spec: { cpu: 1, mem: 2 }

  - name: collect
    container: alpine
    grouped_by: ref
    inputs:
      - cov.out
    command: "cat /input/*.cov.txt > /output/{PAIR.REF}.report.txt"
    outputs: { report: "*.report.txt" }
    task_spec: { cpu: 1, mem: 2 }
"""


def test_multicov_yaml_dry_run_succeeds():
    # End-to-end shape: per-pair iteration, per-ref grouped_by at the
    # head (split), per-pair downstream that references the per-ref
    # head (cov), per-ref tail that fans in per-pair (collect). This
    # is the exact topology binning_fairy_multicov.yaml uses.
    data = yaml.safe_load(MULTICOV_YAML)
    mappings = "ref\tquery\nA\tA\nA\tB\nB\tA\nB\tB\n"
    result = yr.run_yaml(data, params_values={
        'input_uri': 's3://b/i',
        'output_uri': 's3://b/o',
        'mappings_tsv': mappings,
        'location': 'azure.primary:swedencentral',
    }, dry_run=True, no_recruiters=True, standalone=False)
    # dry_run returns None on success (workflow created and deleted).
    # An assertion-driven failure would have already raised /
    # sys.exit'd inside the runner before reaching here, so just
    # reaching this line is the success signal.
    assert result is None


# ---------------- _iter_keys shape (the runner's marker) ----------------


def test_composite_tsv_sets_iter_keys_to_aliases():
    # `_iter_keys` lists the keys that ARE the iteration's identifier
    # components. For composite-TSV that's the UPPERCASE column
    # aliases (REF, QUERY) — NOT the composite (PAIR), NOT the
    # dotted-form data accessors (pair.ref_assembly etc.).
    # Tag-construction and grouped_by subset-matching both use
    # this to decide what's an "atom" of the iteration tag.
    iter_def = {
        'name': 'pair',
        'source': 'tsv',
        'content': "ref\tquery\textra\nA\tB\turi_a\nC\tD\turi_c\n",
        'key': ['ref', 'query'],
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert items[0]['_iter_keys'] == ['REF', 'QUERY']
    assert items[1]['_iter_keys'] == ['REF', 'QUERY']


def test_single_col_tsv_has_no_iter_keys_marker():
    # Single-column TSV preserves the historical "all columns in tag"
    # behaviour (no `_iter_keys` set). Composite-key is the only case
    # where the new marker is emitted — we don't want to silently
    # change tag shape for every existing TSV workflow.
    iter_def = {
        'name': 'sample',
        'source': 'tsv',
        'content': "id\tdepth\nA\t10\n",
        'key': 'id',
    }
    items, _ = yr._build_single_iterator(iter_def, params=SimpleNamespace())
    assert '_iter_keys' not in items[0]


# ---------------- tag construction with URI columns ----------------


# A regression for the bug where URI columns leaked into task tags
# and then got mangled by `.split('.')` in the subset matcher,
# breaking the grouped_by → per-iter → grouped_by chain.
URI_COLUMNS_YAML = """
format: 2
name: tst-uri-cols
version: 0.0.1

params:
  input_uri:
    type: string
    required: true
  output_uri:
    type: string
    required: true
  mappings_tsv:
    type: text
    required: true
  location:
    type: provider_region
    required: true

iterate:
  name: pair
  source: tsv
  content: "{params.mappings_tsv}"
  key: [ref, query]

workspace: "{params.location}"
language: bash

steps:
  - name: split
    container: alpine
    grouped_by: ref
    inputs:
      - "{pair.ref_assembly}"
    command: "echo {PAIR.REF} > /output/{PAIR.REF}.split.txt"
    outputs:
      out: "*.split.txt"
    task_spec:
      cpu: 1
      mem: 2

  - name: cov
    container: alpine
    inputs:
      - split.out
      - "{pair.query_r1}"
      - "{pair.query_r2}"
    command: "echo {PAIR} > /output/{PAIR}.cov.txt"
    outputs:
      out: "*.cov.txt"
    task_spec:
      cpu: 1
      mem: 2

  - name: collect
    container: alpine
    grouped_by: ref
    inputs:
      - cov.out
    command: "cat /input/*.cov.txt > /output/{PAIR.REF}.report.txt"
    outputs:
      report: "*.report.txt"
    task_spec:
      cpu: 1
      mem: 2
"""


def test_uri_columns_dont_leak_into_tags():
    # End-to-end with URI columns containing dots in their values
    # (s3://bucket/foo.contigs.fa). Pre-fix, the per-iter `cov`
    # tag included every column value joined with dots, and the
    # grouped_by `collect` step's subset-match failed because URI
    # fragments leaked into the tag components.
    data = yaml.safe_load(URI_COLUMNS_YAML)
    mappings = (
        "ref\tquery\tref_assembly\tquery_r1\tquery_r2\n"
        "A\tA\ts3://bkt/m/A/A.contigs.fa\ts3://bkt/q/A.1.fastq.gz\ts3://bkt/q/A.2.fastq.gz\n"
        "A\tB\ts3://bkt/m/A/A.contigs.fa\ts3://bkt/q/B.1.fastq.gz\ts3://bkt/q/B.2.fastq.gz\n"
        "B\tA\ts3://bkt/m/B/B.contigs.fa\ts3://bkt/q/A.1.fastq.gz\ts3://bkt/q/A.2.fastq.gz\n"
        "B\tB\ts3://bkt/m/B/B.contigs.fa\ts3://bkt/q/B.1.fastq.gz\ts3://bkt/q/B.2.fastq.gz\n"
    )
    # No exception = the per-iter `cov` step's subset-match against
    # the per-ref `split` step worked, AND the per-ref `collect`
    # step's subset-match against the per-pair `cov` step worked.
    # If URIs were still leaking into tags, the second match would
    # fail with "no upstream tasks match any iteration in this
    # group" — exactly the bug this fix targets.
    result = yr.run_yaml(data, params_values={
        'input_uri': 's3://b/i',
        'output_uri': 's3://b/o',
        'mappings_tsv': mappings,
        'location': 'azure.primary:swedencentral',
    }, dry_run=True, no_recruiters=True, standalone=False)
    assert result is None


# ---------------- _propagate_constant_cols ----------------


def test_propagate_constant_cols_includes_constant():
    # `grouped_by: ref` runs once per unique ref, with the synthetic
    # itervar built from the group's first iteration. Columns whose
    # value is CONSTANT across the group (e.g. `pair.ref_assembly`
    # in a ref-group) must be exposed in the synthetic itervar so
    # `{pair.col}` substitution works inside the step's inputs /
    # command — otherwise split_contigs has no way to reach the
    # assembly URI from the per-pair-iterator data.
    syn = {'REF': 'A', '_group_iterations': []}
    group = [
        {'REF': 'A', 'QUERY': 'B', 'pair.ref_assembly': 's3://x/A.fa', 'pair.query_r1': 's3://x/B.1.fq'},
        {'REF': 'A', 'QUERY': 'C', 'pair.ref_assembly': 's3://x/A.fa', 'pair.query_r1': 's3://x/C.1.fq'},
    ]
    yr._propagate_constant_cols(syn, group)
    # ref_assembly is constant → propagated.
    assert syn['pair.ref_assembly'] == 's3://x/A.fa'
    # query_r1 varies → NOT propagated (would be misleading).
    assert 'pair.query_r1' not in syn


def test_propagate_constant_cols_skips_existing_key():
    # Don't overwrite the grouped-by key value itself.
    syn = {'REF': 'A', '_group_iterations': []}
    group = [{'REF': 'A', 'pair.extra': 'one'}, {'REF': 'A', 'pair.extra': 'one'}]
    yr._propagate_constant_cols(syn, group)
    assert syn['REF'] == 'A'   # untouched
    assert syn['pair.extra'] == 'one'   # propagated


def test_propagate_constant_cols_skips_internal_keys():
    syn = {'REF': 'A', '_group_iterations': []}
    group = [{'REF': 'A', '_iter_keys': ['REF', 'QUERY'], '_source': 'tsv'}]
    yr._propagate_constant_cols(syn, group)
    # Internal keys (`_*`) are never propagated — they belong to the
    # iteration's machinery, not the workflow's substitution surface.
    assert '_iter_keys' not in syn or syn['_iter_keys'] == syn.get('_iter_keys', None)
    # Strictly: not added if it wasn't there before.
    assert syn['_group_iterations'] == []   # untouched
    # _iter_keys came from the group iteration but starts with _ → skipped.
    assert '_iter_keys' not in syn
