"""Tests for `{unique_suffix}` — opt-in collision-avoidance placeholder
in publish paths.

Semantics (spec: docs/usage/yaml-templates.md):

  * Resolved ONCE at workflow.compile() time.
  * NEW workflow: list the target parent folder; pick "" if clean,
    else the first "-N" (N=1,2,…) whose parent is empty.
  * EXTEND workflow: derive from any existing task's publish URI
    by prefix-matching the template — no filesystem listing, no
    accidental new folder.
  * Zero cost when no publish template uses the placeholder.

These tests target the substitution + derivation logic directly so
we don't need a live server; the listing path (new workflow case)
is exercised by mocking the client's fetch_list.
"""
from types import SimpleNamespace

import pytest

from scitq2.workflow import Task, Step, Workflow, _ExtendContext


class _FakeClient:
    """Minimal stand-in for Scitq2Client covering the calls the
    resolver makes. `fetch_list` returns whatever's configured for
    the URI; every other method raises to catch accidental calls."""

    def __init__(self, folder_content):
        # folder_content: {uri_prefix: [list of entries]}. Empty list
        # or missing key means "folder is empty / doesn't exist".
        self._folder = folder_content
        self.list_calls = []

    def fetch_list(self, uri):
        self.list_calls.append(uri)
        return list(self._folder.get(uri, []))


def _make_workflow(publish_template, workflow_id=42, workflow_name='wf',
                   workspace_root='azure://ws', extend=None):
    """Assemble a minimal Workflow with one Step and one Task whose
    publish template contains `{unique_suffix}`. Bypasses the DSL's
    server round-trips so the resolver can be exercised in isolation.
    """
    wf = Workflow.__new__(Workflow)  # skip __init__ (needs kwargs we don't care about)
    wf._steps = {}
    wf.workflow_id = workflow_id
    wf.name = workflow_name
    wf.full_name = workflow_name
    wf.workspace_root = workspace_root
    wf.publish_suffix = None
    wf._extend = extend
    step = Step.__new__(Step)
    step.name = 's'
    step.tasks = []
    step.workflow = wf
    step.step_id = 1
    step.outputs_globs = {}
    # Bypass Task.__init__ — it dereferences step.naming_strategy and
    # other attributes we don't need for the resolver. The resolver
    # only reads task.publish.
    task = Task.__new__(Task)
    task.publish = publish_template
    step.tasks.append(task)
    wf._steps['s'] = step
    return wf


# ---------------- new workflow: listing-based resolution ----------------


def test_no_placeholder_no_resolution():
    """Zero cost when no publish template uses the placeholder —
    resolver returns immediately, publish_suffix stays None."""
    wf = _make_workflow('s3://bkt/fixed/{SAMPLE}/')  # no {unique_suffix}
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix is None
    assert client.list_calls == [], "no listings should have been performed"


def test_clean_target_resolves_to_empty():
    """Target folder is empty → suffix is "" (no path change).
    The URI the resolver lists is the concrete target folder,
    NOT the parent — parent-listing would give the same URI for
    every candidate and produce a degenerate loop."""
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/')
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == ''
    assert client.list_calls == ['s3://bkt/x/']


def test_first_collision_resolves_to_minus_one():
    """Target folder has content → -1 checked → empty → suffix = -1."""
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/')
    client = _FakeClient({'s3://bkt/x/': ['s3://bkt/x/S001/']})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == '-1'
    assert client.list_calls == ['s3://bkt/x/', 's3://bkt/x-1/']


def test_climbing_collisions_finds_first_free():
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/')
    client = _FakeClient({
        's3://bkt/x/':    ['e'],
        's3://bkt/x-1/':  ['e'],
        's3://bkt/x-2/':  ['e'],
    })
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == '-3'
    assert client.list_calls == [
        's3://bkt/x/', 's3://bkt/x-1/', 's3://bkt/x-2/', 's3://bkt/x-3/',
    ]


def test_exhausted_candidates_raises_loud():
    """11 candidates ('' + -1…-10) all collide → RuntimeError.
    Silent selection of -11 would hide a real problem (the operator's
    output_uri root has become a graveyard) — better to force a
    decision."""
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/')
    client = _FakeClient({
        f's3://bkt/x{s}/': ['e']
        for s in ([''] + [f'-{i}' for i in range(1, 11)])
    })
    with pytest.raises(RuntimeError, match=r"10\+ candidate suffixes"):
        wf._resolve_publish_suffix(client)


def test_workflow_scope_substitutions_in_prefix():
    """{workflow_name} in the prefix must be substituted before
    listing — otherwise we'd probe a URI containing the literal
    placeholder string."""
    wf = _make_workflow(
        's3://bkt/{workflow_name}{unique_suffix}/{PAIR.REF}/',
        workflow_name='binning',
    )
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == ''
    assert client.list_calls == ['s3://bkt/binning/']


def test_task_compile_substitutes_resolved_suffix():
    """After resolution, Task.compile's publish substitution swaps
    `{unique_suffix}` with the resolved value across every task."""
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/')
    wf.publish_suffix = '-2'
    task = wf._steps['s'].tasks[0]
    # Simulate the substitution block Task.compile runs.
    resolved = task.publish
    resolved = resolved.replace('{workflow_id}', str(wf.workflow_id))
    resolved = resolved.replace('{workflow_name}', wf.name)
    resolved = resolved.replace('{unique_suffix}', wf.publish_suffix or '')
    assert resolved == 's3://bkt/x-2/{PAIR.REF}/'


# ---------------- extend: derivation from existing tasks ----------------


def _make_extend_context(step_name, existing_publish):
    """Build a minimal _ExtendContext with one existing task whose
    resolved publish URI we want the resolver to parse."""
    step_ids = {step_name: 1}
    tasks_by_step = {1: {'task_1': (100, 'cmd', 'ctnr', 'S', existing_publish)}}
    return _ExtendContext(
        workflow_id=42,
        retry_failed_only=False,
        step_ids=step_ids,
        tasks_by_step=tasks_by_step,
        workflow_name='wf',
    )


def test_extend_derives_empty_suffix_from_clean_publish():
    ext = _make_extend_context('s', 's3://bkt/x/S001/')
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/', extend=ext)
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == ''
    assert client.list_calls == [], "extend must not list the filesystem"


def test_extend_derives_minus_one_from_suffixed_publish():
    ext = _make_extend_context('s', 's3://bkt/x-1/S001/')
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/', extend=ext)
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == '-1'
    assert client.list_calls == []


def test_extend_derives_workflow_name_prefix():
    """Prefix with {workflow_name} — the derivation must substitute
    workflow_name in the template prefix before matching the existing
    URI."""
    ext = _make_extend_context('s', 's3://bkt/binning-1/S001/')
    wf = _make_workflow(
        's3://bkt/{workflow_name}{unique_suffix}/{PAIR.REF}/',
        workflow_name='binning',
        extend=ext,
    )
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == '-1'


def test_extend_falls_through_to_new_when_existing_task_missing():
    """Extend context is present but has NO tasks for the step that
    uses the placeholder → derivation returns None → resolver falls
    through to the fresh listing path (documented behaviour)."""
    # An extend context whose tasks_by_step doesn't include our step.
    ext = _ExtendContext(
        workflow_id=42, retry_failed_only=False,
        step_ids={'s': 1}, tasks_by_step={}, workflow_name='wf',
    )
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/', extend=ext)
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == ''
    assert client.list_calls == ['s3://bkt/x/'], \
        "expected fallback to new-workflow listing when derivation fails"


def test_new_workflow_when_extend_has_no_matching_publish():
    """Extend context has tasks, but their existing publish URI
    doesn't start with our template's prefix — derivation returns
    None, resolver falls through to fresh listing."""
    ext = _make_extend_context('s', 's3://other/bucket/S001/')
    wf = _make_workflow('s3://bkt/x{unique_suffix}/{PAIR.REF}/', extend=ext)
    client = _FakeClient({})
    wf._resolve_publish_suffix(client)
    assert wf.publish_suffix == ''
    assert client.list_calls == ['s3://bkt/x/']
