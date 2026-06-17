"""Tests for Param.float — covers the three surfaces the type touches:

  1. Direct Python DSL use (Param.float in a ParamSpec) — parsing,
     defaults, choices, scientific notation, str-to-float coercion.
  2. YAML-templated workflows — `type: float` dispatch in
     yaml_runner._build_params_class.
  3. Schema output — what RunTemplate/UI/schema readers see (the type
     name surfaced via Param.schema()).
"""
import pytest

from scitq2.param import Param, ParamSpec
from scitq2.yaml_runner import _build_params_class


# ---- 1. Direct DSL use ----

class FloatParams(metaclass=ParamSpec):
    rate = Param.float(default=0.05, help="learning rate")
    threshold = Param.float(required=True, help="cutoff")


def test_float_parses_decimal_string():
    obj = FloatParams.parse({"threshold": "0.75"})
    assert obj.threshold == 0.75
    assert isinstance(obj.threshold, float)


def test_float_parses_scientific_notation():
    obj = FloatParams.parse({"threshold": "1e-4"})
    assert obj.threshold == 0.0001
    assert isinstance(obj.threshold, float)


def test_float_accepts_native_float():
    obj = FloatParams.parse({"threshold": 0.5})
    assert obj.threshold == 0.5


def test_float_accepts_native_int():
    # Real-world YAML often has "depth: 2" — should coerce to 2.0
    # without complaint, matching how Param.integer handles strings.
    obj = FloatParams.parse({"threshold": 2})
    assert obj.threshold == 2.0
    assert isinstance(obj.threshold, float)


def test_float_default_applied():
    obj = FloatParams.parse({"threshold": "1.0"})
    assert obj.rate == 0.05


def test_float_required_enforced():
    with pytest.raises(ValueError, match="Missing required parameter"):
        FloatParams.parse({})


def test_float_rejects_garbage():
    with pytest.raises(ValueError, match="Invalid value for parameter 'threshold'"):
        FloatParams.parse({"threshold": "not-a-number"})


def test_float_with_choices():
    class C(metaclass=ParamSpec):
        x = Param(typ=float, choices=[0.1, 0.5, 1.0])

    obj = C.parse({"x": "0.5"})
    assert obj.x == 0.5
    with pytest.raises(ValueError, match="not in"):
        C.parse({"x": "2.0"})


# ---- 2. YAML dispatch ----

def test_yaml_dispatch_float():
    cls = _build_params_class({
        "ratio": {"type": "float", "default": 0.5, "help": "fraction"},
        "tol": {"type": "float", "required": True},
    })
    obj = cls.parse({"tol": "1e-6"})
    assert obj.tol == 1e-6
    assert obj.ratio == 0.5


def test_yaml_float_required_missing():
    cls = _build_params_class({
        "tol": {"type": "float", "required": True},
    })
    with pytest.raises(ValueError, match="Missing required parameter"):
        cls.parse({})


# ---- 3. Schema surface ----

def test_schema_emits_float_type_name():
    """Operators / UI / RunTemplate read the schema() output to render
    inputs. The type name must be the stable "float" string — the UI's
    WfTemplatePage matches on `param.type === 'float'` to render a
    decimal-friendly number input (step="any"). If the name drifts
    (e.g. accidentally exposing "builtins.float" or "Float"), the
    UI falls back to a generic text input."""
    schema = FloatParams.schema()
    by_name = {p["name"]: p for p in schema}
    assert by_name["threshold"]["type"] == "float"
    assert by_name["threshold"]["required"] is True
    assert by_name["rate"]["type"] == "float"
    # Default carries its native type now (was previously stringified —
    # that broke `default: false` for booleans because "False" reads as
    # truthy in JS; fixing it for booleans also restores native floats
    # so the UI's number input gets a real number instead of "0.05").
    assert by_name["rate"]["default"] == 0.05
    assert isinstance(by_name["rate"]["default"], float)


def test_schema_preserves_native_default_types():
    """Catches the original bug: a boolean `default: false` must survive
    schema → JSON → UI as the literal `false`, NOT the string "False".
    The string form is truthy in JS so a checkbox bound via bind:checked
    flipped to ON, exactly the opposite of what the operator declared.
    Same hazard for `default: 0` (the string "0" is truthy too)."""
    class P(metaclass=ParamSpec):
        flag = Param.boolean(default=False)
        flag_true = Param.boolean(default=True)
        n = Param.integer(default=0)
        s = Param.string(default="hello")

    by_name = {p["name"]: p for p in P.schema()}
    assert by_name["flag"]["default"] is False
    assert by_name["flag_true"]["default"] is True
    assert by_name["n"]["default"] == 0
    assert isinstance(by_name["n"]["default"], int)
    assert by_name["s"]["default"] == "hello"
