"""Regression: URI.find must skip directory entries.

fetch.List on the server appends a trailing '/' to directory entries
(fetch/fetch.go:891). Without filtering, URI.find iterates folders as
if they were files — every folder becomes a separate "sample" event
with the folder's basename as its tag, and the folder's URI shows up
as that sample's file list. cegat_import.yaml hit this when the
listed level contained both real files (md5sums.tsv, …) and per-sample
subfolders: one task ran against the actual fastq, a sibling task ran
against the bare folder URI.
"""
from unittest.mock import patch

from scitq2.uri import URI


def _stub_client(files):
    """A Scitq2Client stand-in that returns a fixed list from fetch_list,
    so URI.find runs entirely in-process."""
    class _Stub:
        def fetch_list(self, _uri):
            return list(files)
    return _Stub


def test_group_by_folder_drops_trailing_slash_entries():
    """The cegat_import.yaml regression in isolation: a parent listing
    that mixes files and subfolders shouldn't yield a "sample" whose
    only file is the folder URI.

    Pre-fix: URIs ending in '/' became their own sample group via
    `path.name` on a trailing-slash path (which PurePosixPath resolves
    to the folder's basename). Post-fix: they're dropped upstream and
    `group_by='folder'` only sees real files."""
    stub = _stub_client([
        "s3://rnd/input/cegat/S17769/S18155/mgshot_S18155Nr1.1.fastq.gz",
        "s3://rnd/input/cegat/S17769/S18155/mgshot_S18155Nr1.2.fastq.gz",
        "s3://rnd/input/cegat/S17769/S18156/mgshot_S18156Nr1.1.fastq.gz",
        # An empty placeholder folder (some tooling leaves these behind).
        "s3://rnd/input/cegat/S17769/empty_dir/",
    ])
    with patch("scitq2.uri.Scitq2Client", stub):
        result = URI.find(
            "s3://rnd/input/cegat/S17769/",
            group_by="folder",
            field_map={"sample_accession": "folder.name", "fastqs": "file.uris"},
        )
    sample_names = sorted(s.sample_accession for s in result)
    # 'empty_dir' must NOT appear as a sample.
    assert sample_names == ["S18155", "S18156"], sample_names
    # Each surviving sample carries only real file URIs.
    for s in result:
        for u in s.fastqs:
            assert not u.endswith("/"), f"folder leaked into fastqs: {u}"


def test_group_by_folder_handles_only_folders_at_listed_level():
    """When the listed directory contains nothing but subfolders (the
    cegat_import.yaml shape — at the project root, all entries are
    per-sample directories), pre-fix produced one bogus "sample" per
    subfolder with empty file lists. Post-fix the result is empty —
    matching the YAML semantic that the recursive listing happens at
    the next level."""
    stub = _stub_client([
        "s3://rnd/input/cegat/S17769/S18155/",
        "s3://rnd/input/cegat/S17769/S18156/",
    ])
    with patch("scitq2.uri.Scitq2Client", stub):
        result = URI.find(
            "s3://rnd/input/cegat/S17769/",
            group_by="folder",
            field_map={"sample_accession": "folder.name", "fastqs": "file.uris"},
        )
    assert result == []


def test_group_by_folder_pure_file_listing_is_unchanged():
    """Backward-compat: a listing with no directories produces the same
    grouping as before — one sample per parent folder, all files
    included."""
    stub = _stub_client([
        "s3://rnd/raw/A/a1.fastq.gz",
        "s3://rnd/raw/A/a2.fastq.gz",
        "s3://rnd/raw/B/b1.fastq.gz",
    ])
    with patch("scitq2.uri.Scitq2Client", stub):
        result = URI.find(
            "s3://rnd/raw/",
            group_by="folder",
            field_map={"sample_accession": "folder.name", "fastqs": "file.uris"},
        )
    samples = sorted((s.sample_accession, len(s.fastqs)) for s in result)
    assert samples == [("A", 2), ("B", 1)]
