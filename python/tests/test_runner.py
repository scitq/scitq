import pytest
import json
import sys
from unittest import mock
from scitq2 import runner
from scitq2.runner import run, find_param_class_from_func
from scitq2.param import Param, ParamSpec


class _StubWorkflow:
    """Stands in for Workflow: run() requires the function to return one,
    then compiles it against the server."""

    def __init__(self):
        self.compiled = False

    def compile(self, client, **kw):
        self.compiled = True


@pytest.fixture
def no_server(monkeypatch):
    """Let run() reach and compile the workflow without a server: the
    workflow is a stub, the client a mock, and a preset template run id skips
    ad-hoc run registration."""
    monkeypatch.setattr(runner, "Workflow", _StubWorkflow)
    monkeypatch.setattr(runner, "Scitq2Client", mock.MagicMock)
    monkeypatch.setenv("SCITQ_TEMPLATE_RUN_ID", "1")

# --- Dummy classes and functions ---
class MyParams(metaclass=ParamSpec):
    foo = Param.string(required=True)
    bar = Param.integer(default=42)

def workflow_with_params(params: MyParams):
    print(f"RUN: foo={params.foo}, bar={params.bar}")
    return _StubWorkflow()

def workflow_no_params():
    print("RUN: no params")
    return _StubWorkflow()

# --- Tests ---
def test_find_param_class_from_func():
    assert find_param_class_from_func(workflow_with_params) is MyParams
    assert find_param_class_from_func(workflow_no_params) is None


def test_run_with_params(monkeypatch, capsys, no_server):
    monkeypatch.setattr(sys, "argv", ["script.py", "--values", '{"foo": "abc"}'])
    run(workflow_with_params)
    captured = capsys.readouterr()
    assert "foo=abc" in captured.out
    assert "bar=42" in captured.out


def test_run_params_schema(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["script.py", "--params"])
    run(workflow_with_params)
    captured = capsys.readouterr()
    schema = json.loads(captured.out)
    assert isinstance(schema, list)
    assert any(p["name"] == "foo" and p["required"] for p in schema)


def test_run_no_params(monkeypatch, capsys, no_server):
    monkeypatch.setattr(sys, "argv", ["script.py"])
    run(workflow_no_params)
    captured = capsys.readouterr()
    assert "no params" in captured.out


def test_run_values_missing_for_param(monkeypatch):
    monkeypatch.setattr(sys, "argv", ["script.py"])
    with pytest.raises(SystemExit):
        run(workflow_with_params)


def test_run_values_for_paramless(monkeypatch):
    monkeypatch.setattr(sys, "argv", ["script.py", "--values", '{"foo": "abc"}'])
    with pytest.raises(SystemExit):
        run(workflow_no_params)
