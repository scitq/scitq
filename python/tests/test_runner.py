import pytest
import json
import sys
from unittest import mock
from scitq2.runner import run, find_param_class_from_func
from scitq2.param import Param, ParamSpec

# --- Dummy classes and functions ---
class MyParams(metaclass=ParamSpec):
    foo = Param.string(required=True)
    bar = Param.integer(default=42)

def workflow_with_params(params: MyParams):
    print(f"RUN: foo={params.foo}, bar={params.bar}")

def workflow_no_params():
    print("RUN: no params")

# --- Tests ---
def test_find_param_class_from_func():
    assert find_param_class_from_func(workflow_with_params) is MyParams
    assert find_param_class_from_func(workflow_no_params) is None


def test_run_with_params(monkeypatch, capsys):
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


def test_run_no_params(monkeypatch, capsys):
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
    with pytest.raises(ValueError):
        run(workflow_no_params)
