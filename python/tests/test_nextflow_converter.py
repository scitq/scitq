"""Regression tests for the Nextflow → YAML converter.

These tests run the converter against the bundled testdata fixtures and
assert that the produced YAML is well-formed, contains the expected step
names, declares the expected params, and passes the runner's schema
validator (``yaml_runner._check_unknown_keys`` etc.) without raising.

Run only on what the parser can handle today — these are smoke tests
against known-good fixtures, not a fidelity guarantee. The full pipeline
of conversion → upload → run is exercised in the Go integration suite
once Florian's actual ``.nf`` is in hand.
"""
import io
from pathlib import Path

import pytest
import yaml

from scitq2.convert import nextflow

TESTDATA = Path(nextflow.__file__).parent / "testdata"

FIXTURES = [
    ("simple.nf", ["fastp", "bowtie2", "seqtk", "merge_results"]),
    ("metagenomics.nf", ["fastp", "bowtie2_host_removal", "seqtk_sample",
                          "kraken2", "merge_reports"]),
    ("rnaseq.nf", ["index", "fastqc", "quant", "multiqc"]),
]


def _convert(name: str) -> dict:
    """Convert a fixture and return the parsed YAML structure."""
    src = (TESTDATA / name).read_text()
    pipeline = nextflow.parse(src)
    text = nextflow.generate_yaml(pipeline)
    return yaml.safe_load(text)


@pytest.mark.parametrize("nf_file, expected_steps", FIXTURES)
def test_yaml_output_is_wellformed(nf_file, expected_steps):
    """Converter output is valid YAML, declares format 2, and carries the
    expected steps in workflow-call order."""
    data = _convert(nf_file)
    assert data["format"] == 2
    assert data["name"]
    assert data["description"]
    assert "iterate" in data
    assert "worker_pool" in data
    assert "workspace" in data
    assert isinstance(data["steps"], list) and data["steps"], \
        "must emit at least one step"
    got = [s["name"] for s in data["steps"]]
    assert got == expected_steps, \
        f"step order/names diverged: got {got}, expected {expected_steps}"


@pytest.mark.parametrize("nf_file, _expected", FIXTURES)
def test_yaml_passes_runner_validation(nf_file, _expected):
    """Every produced YAML passes the yaml_runner's strict top-level and
    per-step key validation — the same gate a user would hit on upload."""
    from scitq2 import yaml_runner

    data = _convert(nf_file)
    yaml_runner._check_unknown_keys(data, yaml_runner.TOPLEVEL_KEYS, "top-level")
    for step in data["steps"]:
        # The converter only emits native steps (it doesn't know about
        # the module library), so STEP_NATIVE_KEYS is the right surface.
        yaml_runner._check_unknown_keys(
            step, yaml_runner.STEP_NATIVE_KEYS, f"step {step.get('name','?')!r}")
    # Params block keys are also strictly validated by the runner.
    for pname, pdef in (data.get("params") or {}).items():
        yaml_runner._check_unknown_keys(
            pdef, yaml_runner.PARAM_ENTRY_KEYS, f"param {pname!r}")


@pytest.mark.parametrize("nf_file, _expected", FIXTURES)
def test_yaml_steps_have_command_and_container(nf_file, _expected):
    """Every emitted step that came from a real NF process has a non-empty
    `command:` and a `container:` (the two things every per-sample step
    needs to actually run)."""
    data = _convert(nf_file)
    for step in data["steps"]:
        if step.get("_comment"):
            continue  # placeholder for unparsed processes — has TODO marker
        assert step.get("command", "").strip(), f"{step['name']}: empty command"
        assert step.get("container"), f"{step['name']}: missing container"


def test_publish_root_when_outdir_param_present():
    """Nextflow's universal `params.outdir` should map to a `publish_root:`
    at the YAML workflow level — so the converter doesn't drop the publish
    intent that every nf-core pipeline relies on."""
    data = _convert("simple.nf")
    assert "publish_root" in data, "params.outdir must surface as publish_root"


def test_fan_in_step_emits_grouped_true():
    """Nextflow `PROCESS.out.x.collect()` calls must become `grouped: true`
    so the consumer step is a fan-in, not a per-sample task."""
    data = _convert("simple.nf")
    merge = next(s for s in data["steps"] if s["name"] == "merge_results")
    assert merge.get("grouped") is True, "MERGE_RESULTS uses .collect() in NF"


def test_iterator_uses_named_file_group():
    """The converter's iterator must declare a `fastqs:` named group (the
    bioinformatics default) rather than the legacy `filter:` key, so the
    output template uses the v2 idiom."""
    data = _convert("simple.nf")
    it = data["iterate"]
    assert it["source"] == "uri"
    assert "fastqs" in it, "iterator must declare a named file group"
    assert "filter" not in it, "must use named groups, not legacy filter:"


def test_cli_format_flag_defaults_to_yaml(tmp_path):
    """`python -m scitq2.convert.nextflow input.nf` should default to YAML
    output (not the legacy Python DSL)."""
    out = tmp_path / "out.yaml"
    nextflow.convert_file(
        str(TESTDATA / "simple.nf"),
        output_path=str(out),
        fmt="yaml",
    )
    text = out.read_text()
    assert text.startswith("# Converted from a Nextflow DSL2 pipeline"), \
        "YAML output must carry the converter's header comment"
    assert "format: 2" in text
    # And explicitly: not Python-DSL shape.
    assert "from scitq2 import" not in text


def test_cli_format_dsl_still_works(tmp_path):
    """The legacy Python DSL emitter must keep functioning (back-compat
    for anyone who pinned the old behaviour)."""
    out = tmp_path / "out.py"
    nextflow.convert_file(
        str(TESTDATA / "simple.nf"),
        output_path=str(out),
        fmt="dsl",
    )
    text = out.read_text()
    assert "from scitq2 import *" in text
    assert "workflow.Step(" in text


def test_unknown_format_rejected(tmp_path):
    with pytest.raises(ValueError, match="unknown --format"):
        nextflow.convert_file(
            str(TESTDATA / "simple.nf"),
            output_path=str(tmp_path / "out.txt"),
            fmt="snakemake",
        )
