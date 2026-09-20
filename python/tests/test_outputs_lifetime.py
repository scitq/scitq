"""Tests for the `lifetime` attribute of Outputs (spec: workspace auto-cleanup
of intermediate step outputs on workflow S).

Covers both the DSL surface (`Outputs(lifetime=...)`) and the YAML surface
(`outputs.lifetime` sibling of the named globs). Server-side sweep is
exercised by the Go integration test.
"""
import pytest
from types import SimpleNamespace

from scitq2 import yaml_runner as yr
from scitq2.workflow import Outputs


# ---------------- DSL surface ----------------


def test_outputs_lifetime_workflow_accepted():
    o = Outputs(lifetime="workflow", cleaned="*.clean.fq")
    assert o.lifetime == "workflow"
    assert o.globs == {"cleaned": "*.clean.fq"}


def test_outputs_lifetime_none_is_default():
    o = Outputs(cleaned="*.clean.fq")
    assert o.lifetime is None


def test_outputs_lifetime_composes_with_publish():
    o = Outputs(publish=True, publish_mode="copy",
                lifetime="workflow", cleaned="*.fq.gz")
    assert o.lifetime == "workflow"
    assert o.publish is True
    assert o.publish_mode == "copy"
    assert o.globs == {"cleaned": "*.fq.gz"}


def test_outputs_lifetime_task_reserved():
    # task-scope is a future feature — rejected until it's actually wired.
    with pytest.raises(ValueError, match="task-scope lifetime is reserved"):
        Outputs(lifetime="task")


def test_outputs_lifetime_unknown_rejected():
    with pytest.raises(ValueError, match="must be None or 'workflow'"):
        Outputs(lifetime="forever")


# ---------------- YAML runner surface ----------------
#
# The runner does `Outputs(**out_kwargs)` where out_kwargs is a dict copy of
# the YAML `outputs:` block plus resolved publish fields. A `lifetime:` key in
# that dict must survive through to Outputs, after {param} resolution.


def _fake_step(outputs_def, params=None):
    """Simulate the runner's outputs-handling path in isolation."""
    out_kwargs = dict(outputs_def)
    if "lifetime" in out_kwargs:
        resolved = yr._resolve_field(
            out_kwargs["lifetime"],
            params=params or SimpleNamespace(),
            itervar=None,
            extra_vars=None,
        )
        if resolved in (None, "", "none", "None"):
            out_kwargs.pop("lifetime", None)
        else:
            out_kwargs["lifetime"] = resolved
    return Outputs(**out_kwargs)


def test_yaml_bare_lifetime_workflow():
    o = _fake_step({"lifetime": "workflow", "cleaned": "*.clean.fq"})
    assert o.lifetime == "workflow"
    assert o.globs == {"cleaned": "*.clean.fq"}


def test_yaml_lifetime_from_param():
    class P:
        cleanup = "workflow"
    o = _fake_step({"lifetime": "{params.cleanup}", "cleaned": "*.fq"}, params=P())
    assert o.lifetime == "workflow"


def test_yaml_lifetime_empty_param_drops_lifetime():
    # An unset / empty param must land as "no lifetime" (today's behaviour),
    # not as an error — the whole point of parameterising it is to let a user
    # turn cleanup off from the launch UI.
    class P:
        cleanup = ""
    o = _fake_step({"lifetime": "{params.cleanup}", "cleaned": "*.fq"}, params=P())
    assert o.lifetime is None


def test_yaml_no_lifetime_key_leaves_default():
    o = _fake_step({"cleaned": "*.fq"})
    assert o.lifetime is None
