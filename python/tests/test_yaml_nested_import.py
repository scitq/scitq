"""Test for nested `import:` module unwrapping.

Regression test: a wrapper module (`private/metaphlan`) that itself
`import:`s a public module (`metagenomics/metaphlan`) must have the
public module's `vars:` merged in BEFORE the wrapper's own additions, so
that any `cond:` in the wrapper that references a var defined by the
public module resolves correctly. Without nested unwrap, the iteration
order in `_build_step` puts the wrapper-defined `cond:` var first and
the public-defined `cond:`-target second — the cond fires before the
target is in `extra_vars` and falls through to a literal-string match
that fails.
"""
from scitq2 import yaml_runner


def test_nested_import_merges_inner_vars_first(monkeypatch):
    """`import: outer` where outer has `import: inner` should produce a
    merged step_def whose `vars` dict has inner's keys before outer's."""
    inner_module = {
        "name": "inner",
        "version": "1.0.0",
        "vars": {
            "FOO": "default-foo",
            "BAR": {
                "cond": "FOO",
                "default-foo": "bar-for-default",
                "custom": "bar-for-custom",
            },
        },
        "container": "inner-image",
        "command": "echo {FOO} {BAR}",
    }
    outer_module = {
        "import": "ns/inner",
        "version": "1.0.0",
        "vars": {
            "BAZ": {
                "cond": "FOO",
                "default-foo": "baz-for-default",
                "custom": "baz-for-custom",
            },
        },
    }

    def fake_load(name):
        if name == "ns/outer":
            return dict(outer_module)
        if name == "ns/inner":
            return dict(inner_module)
        raise ValueError(f"unknown module {name}")

    monkeypatch.setattr(yaml_runner, "_load_public_import", fake_load)

    step_def = {
        "import": "ns/outer",
        "name": "test_step",
        "vars": {"FOO": "custom"},
    }

    # Re-run the import-unwrap block in isolation. Mirrors the logic at
    # yaml_runner.py:1033 — keep this in sync if that block changes.
    if "import" in step_def:
        module_data = yaml_runner._load_public_import(step_def["import"])
        while "import" in module_data:
            nested = yaml_runner._load_public_import(module_data["import"])
            module_data = yaml_runner._merge_module_step(
                nested, module_data, exclude_key="import"
            )
        merged = yaml_runner._merge_module_step(
            module_data, step_def, exclude_key="import"
        )

    # Expected var order: inner's first (FOO, BAR), then outer's
    # additions (BAZ). Step-level FOO override updates value but keeps
    # FOO at position 0.
    assert list(merged["vars"].keys()) == ["FOO", "BAR", "BAZ"]
    assert merged["vars"]["FOO"] == "custom"
    # And the cond targets are still dicts ready for cond resolution
    assert merged["vars"]["BAR"]["cond"] == "FOO"
    assert merged["vars"]["BAZ"]["cond"] == "FOO"


def test_nested_import_resolves_cond_against_inner_var(monkeypatch):
    """End-to-end: after the merge, iterating vars in order resolves a
    cond: that targets an inner-module-defined var without falling
    through to the literal-string match that previously raised."""
    inner_module = {
        "name": "inner",
        "version": "1.0.0",
        "vars": {
            "VERSION": "v1",
            "FLAG": {"cond": "VERSION", "v1": "--one", "v2": "--two"},
        },
    }
    outer_module = {
        "import": "ns/inner",
        "vars": {
            "RESOURCE": {
                "cond": "VERSION",
                "v1": "/r/one.tgz",
                "v2": "/r/two.tgz",
            },
        },
    }

    def fake_load(name):
        if name == "ns/outer":
            return dict(outer_module)
        if name == "ns/inner":
            return dict(inner_module)
        raise ValueError(f"unknown module {name}")

    monkeypatch.setattr(yaml_runner, "_load_public_import", fake_load)

    step_def = {"import": "ns/outer", "vars": {"VERSION": "v2"}}
    module_data = yaml_runner._load_public_import(step_def["import"])
    while "import" in module_data:
        nested = yaml_runner._load_public_import(module_data["import"])
        module_data = yaml_runner._merge_module_step(
            nested, module_data, exclude_key="import"
        )
    merged = yaml_runner._merge_module_step(
        module_data, step_def, exclude_key="import"
    )

    # Iterate vars and resolve each, mirroring _build_step:1077.
    extra_vars = {}

    class _Params:
        pass

    params = _Params()
    for k, expr in merged["vars"].items():
        extra_vars[k] = yaml_runner._resolve_field(
            expr, params, None, step_fields=extra_vars, extra_vars=extra_vars
        )

    assert extra_vars["VERSION"] == "v2"
    assert extra_vars["FLAG"] == "--two"
    assert extra_vars["RESOURCE"] == "/r/two.tgz"
