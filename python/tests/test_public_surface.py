"""Recorded public surface of the Python DSL and the YAML template format.

compat/python.txt must match the code exactly, so every change shows up as a
reviewed diff. Regenerate it with:

    SCITQ_UPDATE_SURFACE=1 pytest tests/test_public_surface.py

Lines may be added in a minor release; removing one breaks users of the
previous release (see docs/reference/compatibility.md). A parameter's
`optional` line exists only while it has a default, so making a parameter
required removes a line.
"""
import inspect
import os
from pathlib import Path

import scitq2
from scitq2 import yaml_runner

SURFACE_FILE = Path(__file__).resolve().parents[2] / "compat" / "python.txt"


def _callable_params(name, obj):
    target = obj.__init__ if inspect.isclass(obj) else obj
    try:
        sig = inspect.signature(target)
    except (TypeError, ValueError):
        return []
    lines = []
    for p in sig.parameters.values():
        if p.name == "self" or p.kind in (p.VAR_POSITIONAL, p.VAR_KEYWORD):
            continue
        lines.append(f"dsl {name}({p.name})")
        if p.default is not p.empty:
            lines.append(f"dsl {name}({p.name}) optional")
    return lines


def python_surface():
    lines = []
    for name in scitq2.__all__:
        lines.append(f"dsl {name}")
        obj = getattr(scitq2, name)
        if callable(obj):
            lines.extend(_callable_params(name, obj))
    for fmt in yaml_runner.SUPPORTED_FORMATS:
        lines.append(f"yaml format {fmt}")
    sections = {
        "top": yaml_runner.TOPLEVEL_KEYS,
        "step": yaml_runner.STEP_NATIVE_KEYS,
        "worker_pool": yaml_runner.WORKER_POOL_KEYS,
        "param": yaml_runner.PARAM_ENTRY_KEYS,
        "chain": yaml_runner.CHAIN_ENTRY_KEYS,
    }
    for section, keys in sections.items():
        lines.extend(f"yaml {section} {k}" for k in keys)
    for source, keys in yaml_runner.ITERATOR_SCHEMAS.items():
        lines.extend(f"yaml iterator {source} {k}" for k in keys)
    return sorted(set(lines))


def test_python_surface_recorded():
    current = python_surface()
    if os.environ.get("SCITQ_UPDATE_SURFACE"):
        SURFACE_FILE.write_text("\n".join(current) + "\n")
        return
    recorded = set(SURFACE_FILE.read_text().split("\n")) - {""}
    added = sorted(set(current) - recorded)
    removed = sorted(recorded - set(current))
    assert not added and not removed, (
        f"{SURFACE_FILE} is out of date.\n"
        f"Added:\n  " + "\n  ".join(added) + "\n"
        f"Removed:\n  " + "\n  ".join(removed) + "\n"
        "Removals break users of the previous release "
        "(docs/reference/compatibility.md). For deliberate changes, rerun with "
        "SCITQ_UPDATE_SURFACE=1 and commit the file."
    )
