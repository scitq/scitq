"""Tests for publish_mode wiring through Outputs / Task / Step / yaml_runner
(spec: addition_from_nextflow.md B — publish_mode: copy).

Worker dual-upload semantic (Go side) is exercised by the integration tests;
these unit tests cover the Python data model and YAML parsing.
"""
import pytest
from types import SimpleNamespace
from unittest.mock import MagicMock

from scitq2.workflow import Outputs, Task, Step, Workflow


# ---------------- Outputs ----------------


def test_outputs_default_publish_mode_is_none():
    o = Outputs(fastqs="*.fq.gz")
    assert o.publish_mode is None


def test_outputs_accepts_move():
    o = Outputs(publish=True, publish_mode="move")
    assert o.publish_mode == "move"


def test_outputs_accepts_copy():
    o = Outputs(publish="azure://results/", publish_mode="copy")
    assert o.publish_mode == "copy"


def test_outputs_rejects_unknown_mode():
    with pytest.raises(ValueError, match="publish_mode must be 'move' or 'copy'"):
        Outputs(publish=True, publish_mode="symlink")


def test_outputs_publish_mode_independent_of_publish_value():
    # The publish_mode field is technically allowed without `publish` set
    # (validated at YAML-runner level; this confirms the constructor is permissive).
    o = Outputs(publish_mode="copy")
    assert o.publish_mode == "copy"


# ---------------- Task ----------------


def test_task_defaults_publish_mode_to_none():
    step = MagicMock(spec=Step)
    step.naming_strategy = lambda n, t: f"{n}.{t}" if t else n
    step.name = "qc"
    t = Task(tag="A", command="echo", container="alpine", step=step)
    assert t.publish_mode is None


def test_task_carries_publish_mode():
    step = MagicMock(spec=Step)
    step.naming_strategy = lambda n, t: f"{n}.{t}" if t else n
    step.name = "qc"
    t = Task(tag="A", command="echo", container="alpine", step=step,
             publish="azure://results/", publish_mode="copy")
    assert t.publish_mode == "copy"


# ---------------- yaml_runner step parser ----------------


def test_yaml_runner_step_keys_accept_publish_mode():
    from scitq2.yaml_runner import STEP_NATIVE_KEYS
    assert 'publish_mode' in STEP_NATIVE_KEYS


def test_yaml_runner_rejects_invalid_publish_mode():
    """The yaml_runner validates publish_mode against the {move, copy, ''} set
    before constructing Outputs. Invalid values raise with a useful message."""
    from scitq2.yaml_runner import _build_step
    # This requires a full step build; easier to inline-test the validation
    # via a minimal path.
    import yaml as _yaml
    step_def = _yaml.safe_load("""
        name: qc
        command: "echo hi"
        container: alpine
        publish: true
        publish_mode: badvalue
    """)
    workflow = MagicMock(spec=Workflow)
    workflow.publish_root = "azure://x/"
    step_map = {}
    with pytest.raises(ValueError, match="must be 'move'"):
        _build_step(
            workflow,
            step_def,
            step_map,
            params=SimpleNamespace(),
        )


def test_yaml_runner_accepts_publish_mode_copy():
    from scitq2.yaml_runner import _build_step, _resolve_field
    import yaml as _yaml
    step_def = _yaml.safe_load("""
        name: qc
        command: "echo hi"
        container: alpine
        publish: "azure://results/"
        publish_mode: copy
    """)
    # Smoke-check that the validation passes; we don't need to mock the
    # Workflow fully — the validation is the very first thing inside the
    # `if outputs_def or publish or publish_mode:` branch.
    # We just verify _resolve_field returns "copy" for that field.
    resolved = _resolve_field(step_def['publish_mode'], params=SimpleNamespace())
    assert resolved == "copy"


def test_yaml_runner_publish_mode_from_param_substitution():
    """A YAML template can carry `publish_mode: "{params.mode}"` and let the
    operator decide. Substitution should still go through the validator."""
    from scitq2.yaml_runner import _resolve_field
    params = SimpleNamespace(mode="copy")
    resolved = _resolve_field("{params.mode}", params=params)
    assert resolved == "copy"
