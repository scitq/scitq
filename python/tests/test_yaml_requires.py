"""Tests for the YAML `requires:` keyword. Modules list companion
modules that should always accompany them — typically a one-off setup
step for a compute step. The yaml_runner auto-injects missing modules
and auto-wires `depends:` so template authors don't have to repeat
themselves.
"""
import pytest
from unittest.mock import patch

from scitq2 import yaml_runner


def _load_mock(modules):
    """Return a fake `_load_module_by_ref` that looks up `modules` by path."""
    def _loader(ref, pipeline_dir=None, script_root=None):
        path = ref.split('@', 1)[0]
        for ext in (".yaml", ".yml"):
            if path.endswith(ext):
                path = path[:-len(ext)]
                break
        return modules.get(path)
    return _loader


def test_auto_injects_required_module_and_wires_depends():
    mods = {
        "metagenomics/meteor2_catalog": {"name": "meteor2_catalog", "per_sample": False},
        "metagenomics/meteor2": {"name": "meteor2", "requires": ["metagenomics/meteor2_catalog"]},
    }
    data = {"steps": [{"import": "metagenomics/meteor2", "inputs": "fastp.fastqs"}]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    assert len(data["steps"]) == 2, "prep module should be prepended"
    assert data["steps"][0]["import"] == "metagenomics/meteor2_catalog", "prep comes first"
    assert data["steps"][1]["import"] == "metagenomics/meteor2"
    # depends: was auto-wired using the required module's name
    assert data["steps"][1]["depends"] == "meteor2_catalog"


def test_no_duplicate_when_user_already_imported():
    mods = {
        "metagenomics/meteor2_catalog": {"name": "meteor2_catalog"},
        "metagenomics/meteor2": {"name": "meteor2", "requires": ["metagenomics/meteor2_catalog"]},
    }
    # User put the catalog right where they wanted it
    data = {"steps": [
        {"import": "genomics/fastp"},
        {"import": "metagenomics/meteor2_catalog"},
        {"import": "metagenomics/meteor2"},
    ]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    # No injection — user's positioning is preserved
    import_paths = [s.get("import") for s in data["steps"]]
    assert import_paths == [
        "genomics/fastp",
        "metagenomics/meteor2_catalog",
        "metagenomics/meteor2",
    ]
    # But depends is still wired on the meteor2 step
    assert data["steps"][2]["depends"] == "meteor2_catalog"


def test_transitive_requires_chain():
    mods = {
        "a/deep_prep": {"name": "deep_prep"},
        "a/mid_prep":  {"name": "mid_prep",  "requires": ["a/deep_prep"]},
        "a/leaf":      {"name": "leaf",      "requires": ["a/mid_prep"]},
    }
    data = {"steps": [{"import": "a/leaf"}]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    imports = [s.get("import") for s in data["steps"]]
    # deep_prep must come before mid_prep, which must come before leaf
    assert imports == ["a/deep_prep", "a/mid_prep", "a/leaf"]
    # leaf depends directly on its required module (mid_prep) — not on the
    # transitive one (deep_prep); that's mid_prep's responsibility.
    assert data["steps"][2]["depends"] == "mid_prep"
    # mid_prep depends on deep_prep
    assert data["steps"][1]["depends"] == "deep_prep"


def test_inline_requires_on_a_raw_step():
    mods = {
        "util/prep": {"name": "prep"},
    }
    data = {"steps": [
        {"name": "compute", "container": "alpine:3", "command": "x",
         "requires": ["util/prep"]}
    ]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    assert len(data["steps"]) == 2
    assert data["steps"][0]["import"] == "util/prep"
    assert data["steps"][1]["depends"] == "prep"


def test_existing_depends_is_merged_not_clobbered():
    mods = {
        "util/prep": {"name": "prep"},
    }
    data = {"steps": [
        {"import": "util/prep"},
        {"name": "compute", "container": "alpine:3", "command": "x",
         "depends": "something_else",
         "requires": ["util/prep"]}
    ]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    # The user's existing depends is kept; auto-wired one is appended.
    deps = data["steps"][-1]["depends"]
    assert "something_else" in deps
    assert "prep" in deps


def test_multiple_requires_all_listed():
    mods = {
        "util/a": {"name": "a"},
        "util/b": {"name": "b"},
        "util/c": {"name": "c", "requires": ["util/a", "util/b"]},
    }
    data = {"steps": [{"import": "util/c"}]}

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    imports = [s.get("import") for s in data["steps"]]
    # Both prereqs injected before the user's step, user's step last
    assert imports[-1] == "util/c"
    assert set(imports[:-1]) == {"util/a", "util/b"}
    # Both names show up in depends
    deps = data["steps"][-1]["depends"]
    assert set(deps) == {"a", "b"}


def test_no_requires_does_nothing():
    mods = {"genomics/fastp": {"name": "fastp"}}
    data = {"steps": [{"import": "genomics/fastp"}]}
    expected = [dict(s) for s in data["steps"]]

    with patch.object(yaml_runner, "_load_module_by_ref", _load_mock(mods)):
        yaml_runner._expand_requires(data)

    assert data["steps"] == expected, "steps should be untouched when no module declares requires"


def test_empty_steps_is_safe():
    data = {}
    yaml_runner._expand_requires(data)
    # Must not raise; also must not invent a `steps:` field from nothing.
    assert "steps" not in data or data["steps"] in (None, [])
