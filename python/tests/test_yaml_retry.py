"""A step's `retry:` (its own or its module's) reaches its tasks; the
template's top-level `retry:` is the default for steps that set none."""
import textwrap
import pytest
import yaml as _yaml

from scitq2 import yaml_runner as yr
from scitq2.workflow import Workflow

pytestmark = pytest.mark.usefixtures("fake_server")


def _run(yaml_src):
    Workflow.last_created = None
    data = _yaml.safe_load(textwrap.dedent(yaml_src))
    yr.run_yaml(data, params_values={}, standalone=True,
                dry_run=True, no_recruiters=True, verbose=False)
    return Workflow.last_created


def _retry(wf, step):
    return wf._steps[step].tasks[0].retry


def test_step_retry_reaches_tasks():
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        container: alpine
        retry: 1
        steps:
          - name: own
            retry: 2
            command: 'echo hi'
          - name: default
            command: 'echo hi'
    """)
    assert _retry(wf, "own") == 2
    assert _retry(wf, "default") == 1


def test_module_retry_reaches_tasks(monkeypatch):
    module = {"name": "m", "version": "1.0.0", "container": "alpine",
              "command": "echo hi", "retry": 2}
    monkeypatch.setattr(yr, "_load_public_import", lambda name: dict(module))
    wf = _run("""
        format: 2
        name: t
        version: "1.0.0"
        steps:
          - import: ns/m
    """)
    assert _retry(wf, "m") == 2
