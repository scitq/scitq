"""`fastq_pair_filtering: true` keeps only R1 of paired samples, at
iteration time, so single-end runs against paired data never fetch R2."""
from types import SimpleNamespace

import pytest

from scitq2 import yaml_runner
from scitq2.biology import Sample, SINGLE, AUTO
import scitq2.uri


PAIRED_FILES = ["s3://b/s1/s1_1.fastq.gz", "s3://b/s1/s1_2.fastq.gz"]
SINGLE_FILES = ["s3://b/s2/s2.fastq.gz"]


@pytest.fixture
def uri_samples(monkeypatch):
    def find(uri, **kwargs):
        return [SimpleNamespace(sample_accession="s1", fastqs=list(PAIRED_FILES)),
                SimpleNamespace(sample_accession="s2", fastqs=list(SINGLE_FILES))]
    monkeypatch.setattr(scitq2.uri.URI, "find", staticmethod(find))


def discover(flag):
    iter_def = {"source": "uri", "uri": "s3://b/", "fastq_pair_filtering": flag}
    return {s.sample_accession: s.fastqs for s in yaml_runner._discover_samples(iter_def, SimpleNamespace())}


@pytest.mark.parametrize("flag", [True, "true"])
def test_uri_keeps_r1_of_paired_samples_only(uri_samples, flag):
    assert discover(flag) == {"s1": ["s3://b/s1/s1_1.fastq.gz"], "s2": SINGLE_FILES}


def test_uri_without_filtering_keeps_every_file(uri_samples):
    assert discover(False) == {"s1": PAIRED_FILES, "s2": SINGLE_FILES}


def run_uris(library_layout, layout):
    return Sample("s", [{"run_accession": "SRR1", "library_layout": library_layout}], layout=layout).fastqs


def test_ena_sra_paired_sample_fetches_read1_only():
    assert run_uris("PAIRED", SINGLE) == ["run+fastq@only-read1://SRR1"]


def test_ena_sra_single_sample_or_no_filtering_is_unchanged():
    assert run_uris("SINGLE", SINGLE) == ["run+fastq://SRR1"]
    assert run_uris("PAIRED", AUTO) == ["run+fastq://SRR1"]
