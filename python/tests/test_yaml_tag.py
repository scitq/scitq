"""Tests for `tag:` resolution at workflow and step levels.

Two paths covered:
  * Workflow-level `tag:` (at the top of the YAML) — accepts plain strings,
    `{params.x}` substitutions, and `cond:` blocks.
  * Step-level `tag:` (inside an individual step) — same shape; until this
    landed, an explicit step tag was silently dropped and the per-task tag
    came only from the iterator context.
"""
import textwrap
import pytest
import yaml as _yaml

from scitq2 import yaml_runner as yr
from scitq2.workflow import Workflow


# ---------------- helper ----------------


def _run(yaml_src, values=None):
    """Build the workflow described by `yaml_src` and return the Workflow.

    Uses run_yaml in standalone+dry-run mode (no server traffic) and grabs
    the result via Workflow.last_created — the same class attribute the
    DSL uses to remember the singleton workflow."""
    Workflow.last_created = None
    data = _yaml.safe_load(textwrap.dedent(yaml_src))
    yr.run_yaml(data, params_values=values or {}, standalone=True,
                dry_run=True, no_recruiters=True, verbose=False)
    return Workflow.last_created


# ---------------- workflow-level tag: ----------------


def test_workflow_tag_plain_string():
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        tag: "static-tag"
        container: alpine
        language: bash
        steps:
          - name: s
            command: 'echo hi'
    """)
    assert wf.tag == "static-tag"


def test_workflow_tag_with_param_substitution():
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        tag: "{params.project}/{params.depth}"
        container: alpine
        language: bash
        params:
          project:
            type: string
            default: "proj1"
          depth:
            type: string
            default: "20M"
        steps:
          - name: s
            command: 'echo hi'
    """)
    assert wf.tag == "proj1/20M"


def test_workflow_tag_cond_block():
    """A cond: block on the workflow-level tag picks the branch matching the
    cond's resolved value."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        tag:
          cond: "{params.mode}"
          dev:  "dev-{params.run}"
          prod: "prod-{params.run}"
        container: alpine
        language: bash
        params:
          mode:
            type: string
            default: "dev"
          run:
            type: string
            default: "001"
        steps:
          - name: s
            command: 'echo hi'
    """, values={"mode": "prod", "run": "042"})
    assert wf.tag == "prod-042"


def test_workflow_tag_cond_block_default_branch():
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        tag:
          cond: "{params.mode}"
          dev:  "dev"
          default: "fallback"
        container: alpine
        language: bash
        params:
          mode:
            type: string
            default: "anything"
        steps:
          - name: s
            command: 'echo hi'
    """, values={"mode": "weird"})
    assert wf.tag == "fallback"


# ---------------- step-level tag: ----------------


def test_step_tag_plain_string_overrides_default_itervar_tag():
    """Without an explicit step tag, scitq derives the per-task tag from the
    iter context. An explicit `tag:` on the step overrides that, even when
    iteration is in play."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        iterate:
          name: sample
          source: list
          values: [A, B]
        steps:
          - name: s
            tag: "fixed"
            command: 'echo {SAMPLE}'
    """)
    s = wf._steps['s']
    # Two iterations → two tasks; both carry the explicit tag (not the
    # iter-derived A/B). The tasks' full_name is `s.fixed` for both, which
    # is degenerate but documents the contract.
    tags = sorted(t.tag for t in s.tasks)
    assert tags == ["fixed", "fixed"]


def test_step_tag_with_iter_substitution():
    """Step-level tag can reference iter context — same pattern as command:."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        iterate:
          name: sample
          source: list
          values: [A, B]
        steps:
          - name: s
            tag: "tag-{SAMPLE}"
            command: 'echo {SAMPLE}'
    """)
    s = wf._steps['s']
    assert sorted(t.tag for t in s.tasks) == ["tag-A", "tag-B"]


def test_step_tag_cond_block_dispatches_on_iter():
    """A cond: block on a step tag dispatches per task — the cond is
    re-resolved for each iteration's itervar."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        iterate:
          name: kind
          source: list
          values: [A, B, A]    # duplicates are illegal as keys but legal as
                                # iter values for `list` source
        steps:
          - name: s
            tag:
              cond: "{KIND}"
              A: "alpha"
              B: "beta"
            command: 'echo {KIND}'
    """)
    s = wf._steps['s']
    assert sorted(t.tag for t in s.tasks) == ["alpha", "alpha", "beta"]


def test_step_tag_cond_block_with_params():
    """Step tag cond: can branch on a workflow-level param."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        params:
          mode:
            type: string
            default: "fast"
        iterate:
          name: sample
          source: list
          values: [A]
        steps:
          - name: s
            tag:
              cond: "{params.mode}"
              fast: "{SAMPLE}-quick"
              slow: "{SAMPLE}-thorough"
            command: 'echo {SAMPLE}'
    """, values={"mode": "slow"})
    s = wf._steps['s']
    assert [t.tag for t in s.tasks] == ["A-thorough"]


# ---------------- regression: legacy behaviour preserved ----------------


def test_no_step_tag_falls_back_to_itervar_derived():
    """Step without an explicit `tag:` still gets a tag composed of the
    iter context — the behaviour everyone relies on today."""
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        iterate:
          name: sample
          source: list
          values: [X, Y]
        steps:
          - name: s
            command: 'echo {SAMPLE}'
    """)
    s = wf._steps['s']
    assert sorted(t.tag for t in s.tasks) == ["X", "Y"]


def test_no_workflow_tag_means_workflow_tag_is_none():
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        language: bash
        steps:
          - name: s
            command: 'echo hi'
    """)
    assert wf.tag is None
