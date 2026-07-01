"""Tests for the friendlier module-not-found error.

When `import: X` fails to resolve against the server library, the
runner appends a "did you mean" line based on:
  1. a known-rename table (e.g. `genetic/` → `genomics/` /
     `metagenomics/` after the library reorganisation, per
     docs/install.md);
  2. fuzzy nearest neighbours in the actual library (difflib), for
     typos and near-misses the rename table doesn't cover.

Regression pinned: alpha2 2026-07-01 support cycle around
`import: genetic/fastp` — the old error told the user to
`scitq module upload …` (misleading — the module exists, just at
a different path) instead of pointing at `genomics/fastp`.
"""
from scitq2 import yaml_runner as yr


LIBRARY = [
    'genomics/fastp',
    'genomics/multiqc',
    'genomics/seqtk_sample',
    'metagenomics/bowtie2_host_removal',
    'metagenomics/eskrim',
    'metagenomics/megahit',
    'metagenomics/metaphlan',
    'metagenomics/meteor2',
]


def test_rename_table_suggests_genomics_for_genetic():
    # `genetic/fastp` was renamed to `genomics/fastp` in the reorg;
    # the rename hint MUST hit that path (independent of fuzz score).
    assert yr._module_did_you_mean('genetic/fastp', LIBRARY) == ['genomics/fastp']


def test_rename_table_prefers_metagenomics_when_only_target_exists():
    # `genetic/megahit` could map to either `genomics/megahit` or
    # `metagenomics/megahit`; only the latter is in the library, so
    # only it should be suggested.
    assert yr._module_did_you_mean('genetic/megahit', LIBRARY) == ['metagenomics/megahit']


def test_typo_matches_by_fuzz():
    # `genetics/fastp` (extra `s`) isn't covered by the rename table
    # but the fuzz path should pick it up.
    suggestions = yr._module_did_you_mean('genetics/fastp', LIBRARY)
    assert 'genomics/fastp' in suggestions


def test_no_suggestion_when_nothing_close():
    # A truly unrelated name — better to say nothing than to send
    # the user chasing a false lead. The 0.6 threshold enforces this.
    assert yr._module_did_you_mean('total/nonsense', LIBRARY) == []


def test_empty_library_returns_empty():
    # Library listing failed / server unreachable — the caller falls
    # back to the base error without a "did you mean" line.
    assert yr._module_did_you_mean('genetic/fastp', []) == []


def test_bare_basename_below_threshold_no_hint():
    # `fastp` alone is below the fuzz threshold vs `genomics/fastp`
    # (~0.53) — intentional: without a namespace prefix the user
    # could mean any of several tools, and a wrong suggestion is
    # worse than no suggestion.
    assert yr._module_did_you_mean('fastp', LIBRARY) == []


def test_suggestion_cap_at_three():
    # A library full of near-matches shouldn't dump 20 suggestions.
    library = [f'genomics/tool{i}' for i in range(20)]
    suggestions = yr._module_did_you_mean('genomics/tool0', library)
    assert len(suggestions) <= 3
