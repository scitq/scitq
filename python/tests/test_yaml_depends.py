"""Tests for the YAML template `depends:` keyword, which wires a
task-level dependency on another step by name. This lets a setup step
(e.g. a one-off catalog download) be a real ordering prerequisite for
per-sample compute tasks, even when data flows via `resource:` rather
than `inputs:`.
"""
import pytest

from scitq2 import yaml_runner
from scitq2.workflow import Step, Workflow


def _minimal_workflow():
    """Build a bare-metal Workflow the way yaml_runner does internally."""
    return Workflow(name="test", version="1.0.0", container="alpine:3")


def _params():
    class P:
        pass
    return P()


def _step_def(name, **extras):
    d = {
        "name": name,
        "container": "alpine:3",
        "command": "echo hi",
    }
    d.update(extras)
    return d


def test_depends_string_resolves_to_step(monkeypatch):
    wf = _minimal_workflow()
    step_map = {}

    # Build the prep step first; it must land in step_map.
    prep = yaml_runner._build_step(
        wf, _step_def("prep"), step_map, _params(),
    )
    assert prep is not None
    step_map[prep.name] = prep

    # A second step with `depends: prep` must carry that dep through.
    compute = yaml_runner._build_step(
        wf, _step_def("compute", depends="prep"), step_map, _params(),
    )
    assert compute is not None
    # The task added for `compute` should carry depends pointing at the prep Step.
    task = compute.tasks[0]
    assert task.depends is not None
    # Normalise: Step → its list of tasks
    assert isinstance(task.depends, list)
    assert all(isinstance(d, Step) or hasattr(d, "task_id") for d in task.depends)
    # The single resolved dependency should reference the prep step.
    # complete_with_task converts a Step to [Step.task], so element is a Task
    # whose step attribute is the prep Step.
    from scitq2.workflow import Task
    dep = task.depends[0]
    assert isinstance(dep, Task)
    assert dep.step is prep


def test_depends_list_multiple_steps():
    wf = _minimal_workflow()
    step_map = {}

    a = yaml_runner._build_step(wf, _step_def("a"), step_map, _params())
    step_map["a"] = a
    b = yaml_runner._build_step(wf, _step_def("b"), step_map, _params())
    step_map["b"] = b

    c = yaml_runner._build_step(
        wf, _step_def("c", depends=["a", "b"]), step_map, _params(),
    )
    task = c.tasks[0]
    assert task.depends is not None
    # Two dependencies registered, one per named step.
    assert len(task.depends) == 2


def test_depends_unknown_step_raises():
    wf = _minimal_workflow()
    step_map = {}

    with pytest.raises(ValueError, match="unknown step 'ghost'"):
        yaml_runner._build_step(
            wf, _step_def("compute", depends="ghost"), step_map, _params(),
        )


def test_depends_rejects_bad_type():
    wf = _minimal_workflow()
    step_map = {}

    with pytest.raises(ValueError, match="'depends' must be a string or list"):
        yaml_runner._build_step(
            wf, _step_def("compute", depends=42), step_map, _params(),
        )


def test_no_depends_leaves_field_unset():
    wf = _minimal_workflow()
    step_map = {}

    s = yaml_runner._build_step(wf, _step_def("solo"), step_map, _params())
    task = s.tasks[0]
    # No depends keyword ⇒ Task.depends defaults to None.
    assert task.depends is None
