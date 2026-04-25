"""Tests for grouped (`grouped: true`) steps consuming directly from the
iterator. Without this fix the workflow author had to insert a no-op
pass-through step to give the resolver an upstream Step output to look
up; with it `inputs: sample.fastqs` on a grouped step resolves to the
union of that file group across every iteration.
"""
from scitq2 import yaml_runner


def _make_iterations(n_samples=3, group="fastqs"):
    """Synthesise n iteration dicts shaped like `_build_iterations` produces.

    Each `_sample` exposes `file_groups[<group>]` with a paired-fastq pair —
    enough for `_resolve_inputs` to walk and concatenate.
    """
    class FakeSample:
        def __init__(self, name):
            self.file_groups = {
                group: [
                    f"azure://workspace/{name}/{name}.1.fastq.gz",
                    f"azure://workspace/{name}/{name}.2.fastq.gz",
                ],
            }
            # v1-compat attr fallback also exercised by the resolver
            self.fastqs = self.file_groups[group]

    return [
        {"sample": f"s{i}", "_sample": FakeSample(f"s{i}")}
        for i in range(n_samples)
    ]


def test_grouped_iterator_resolves_to_union():
    iterations = _make_iterations(n_samples=3)
    out = yaml_runner._resolve_inputs(
        "sample.fastqs", step_map={}, grouped=True,
        itervar=None, iterations=iterations,
    )
    # 3 samples × 2 fastqs each = 6 URIs, in sample-then-pair order
    assert len(out) == 6
    assert out[0].endswith("s0/s0.1.fastq.gz")
    assert out[1].endswith("s0/s0.2.fastq.gz")
    assert out[5].endswith("s2/s2.2.fastq.gz")


def test_per_sample_iterator_still_returns_one_sample():
    iterations = _make_iterations(n_samples=3)
    # Per-sample resolution: itervar set, iterations ignored
    out = yaml_runner._resolve_inputs(
        "sample.fastqs", step_map={}, grouped=False,
        itervar=iterations[1], iterations=iterations,
    )
    assert len(out) == 2
    assert out[0].endswith("s1/s1.1.fastq.gz")


def test_grouped_iterator_unknown_group_raises():
    iterations = _make_iterations(n_samples=2, group="fastqs")
    try:
        yaml_runner._resolve_inputs(
            "sample.contigs", step_map={}, grouped=True,
            itervar=None, iterations=iterations,
        )
    except ValueError as e:
        assert "no file group 'contigs'" in str(e)
    else:
        raise AssertionError("expected ValueError for missing group")


def test_grouped_step_falls_back_when_no_iterations():
    # No iterations available → should fall through to step-map lookup,
    # which has no matching step, so the standard error fires.
    try:
        yaml_runner._resolve_inputs(
            "sample.fastqs", step_map={}, grouped=True,
            itervar=None, iterations=None,
        )
    except ValueError as e:
        assert "Cannot resolve input" in str(e)
    else:
        raise AssertionError("expected ValueError when nothing resolves")
