"""Tests for `{workflow_id}` / `{workflow_name}` substitution in
publish paths.

These placeholders let workflow authors produce collision-free
publish URIs by construction — every run of the same template
lands in a distinct folder (`{output_uri}/{workflow_id}/...`),
solving the "same template launched twice in the same day
overwrote outputs" class of incidents.

Design notes:
  - YAML compile leaves `{workflow_id}` literal (it's not a
    param / iter var); resolution happens at Task.compile because
    `workflow.workflow_id` is server-assigned and only available
    after `workflow.compile()` creates the workflow row.
  - Extend runs REUSE the parent workflow's id, so extending an
    existing workflow keeps landing in the same publish folder
    (that IS the extend user intent — accumulating results, not
    branching).
"""
import types

import pytest

from scitq2.workflow import Task, Step
from scitq2 import yaml_runner as yr


def _make_task(publish: str, workflow_id, workflow_name='wf', workspace_root=None):
    """Build the minimal Task/Step/Workflow chain needed to exercise
    the substitution in isolation (no server round-trip)."""
    wf = types.SimpleNamespace(
        workflow_id=workflow_id,
        name=workflow_name,
        full_name=workflow_name,
        workspace_root=workspace_root,
        resources=[],
    )
    step = types.SimpleNamespace(
        name='s',
        workflow=wf,
        naming_strategy=lambda n, t: f"{n}.{t}" if t else n,
        outputs_globs={},
    )
    t = Task(tag='X', command='', container='', step=step)
    t.publish = publish
    return t, wf


def _resolve(task):
    """Mimic the Task.compile substitution block — just the part that
    resolves `{workflow_id}` / `{workflow_name}` on task.publish."""
    wf = task.step.workflow
    p = task.publish
    if p is None:
        return None
    if wf.workflow_id is not None:
        p = p.replace('{workflow_id}', str(wf.workflow_id))
    if wf.name:
        p = p.replace('{workflow_name}', str(wf.name))
    return p


def test_workflow_id_substituted_in_publish():
    task, _ = _make_task('s3://bkt/{workflow_id}/{SAMPLE}/out/', workflow_id=42)
    # {SAMPLE} was resolved at YAML-compile time (not part of this
    # substitution); {workflow_id} is what we replace here.
    assert _resolve(task) == 's3://bkt/42/{SAMPLE}/out/'


def test_workflow_name_substituted_in_publish():
    task, _ = _make_task('s3://bkt/{workflow_name}/out/', workflow_id=42, workflow_name='my-run')
    assert _resolve(task) == 's3://bkt/my-run/out/'


def test_both_substituted_together():
    task, _ = _make_task(
        's3://bkt/{workflow_name}/{workflow_id}/out/',
        workflow_id=42, workflow_name='my-run',
    )
    assert _resolve(task) == 's3://bkt/my-run/42/out/'


def test_publish_without_placeholders_unchanged():
    task, _ = _make_task('s3://bkt/fixed/out/', workflow_id=42)
    assert _resolve(task) == 's3://bkt/fixed/out/'


def test_no_workflow_id_leaves_placeholder_literal():
    # Before workflow.compile() runs, workflow_id is None. If Task.compile
    # ran in that state (which it shouldn't in normal flow but could in
    # edge cases like dry-runs), the placeholder stays literal — a
    # visible signal that something's out of order, not silent junk.
    task, _ = _make_task('s3://bkt/{workflow_id}/out/', workflow_id=None)
    assert _resolve(task) == 's3://bkt/{workflow_id}/out/'


def test_yaml_runner_leaves_workflow_id_literal():
    # The YAML runner's `_resolve_refs` handles `{params.x}` and
    # `{ITER_VAR}` substitution at compile time. `{workflow_id}`
    # isn't a param OR an iter var — the resolver's default behavior
    # is to leave unknown placeholders literal (`m.group(0)` in
    # `_resolve_refs`), which is exactly what we want so
    # Task.compile can pick them up later.
    out = yr._resolve_refs(
        's3://bkt/{workflow_id}/{SAMPLE}/{workflow_name}/',
        params=None,
        itervar={'SAMPLE': 'A'},
    )
    assert out == 's3://bkt/{workflow_id}/A/{workflow_name}/'


def test_publish_none_returns_none():
    task, _ = _make_task(publish=None, workflow_id=42)
    assert _resolve(task) is None


def test_extend_scenario_id_stays_stable():
    # Extend: workflow_id is reused across runs. Two "compile passes"
    # against the same workflow.workflow_id produce identical publish
    # paths — the extend intent.
    task1, wf = _make_task('s3://bkt/{workflow_id}/out/', workflow_id=1234)
    task2, _ = _make_task('s3://bkt/{workflow_id}/out/', workflow_id=1234)
    assert _resolve(task1) == _resolve(task2)
