#!/usr/bin/env python3
"""
Generate Markdown documentation for the scitq2 DSL.

This script inspects functions and classes from the main scitq2 module
and its biology submodule, and writes a clean Markdown reference
to docs/reference/python_dsl.md.

Usage:
    python python/tools/gen_dsl_doc.py
"""

import inspect
import textwrap
import importlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# Add near the top, right after MODULES
#SKIP_CLASSES = {"Client"}  # classes you don’t want documented
SKIP_CLASSES = []

OUTPUT_FILE = ROOT / "docs" / "reference" / "python_dsl.md"
MODULES = [
    ("scitq2", "Main DSL API"),
    ("scitq2.biology", "Biology extensions"),
]

def should_skip(obj):
    doc = inspect.getdoc(obj)
    if doc and ("NO_PUBLIC_DOC" in doc):
        return True
    return False


def format_docstring(doc: str, indent: int = 0) -> str:
    if not doc:
        return "_No documentation available._"
    doc = textwrap.dedent(doc).strip()
    lines = [(" " * indent) + line for line in doc.splitlines()]
    return "\n".join(lines)


def gen_function_doc(name, func, level="#"):
    if should_skip(func):
        print(f"Skipping {name} (NO_PUBLIC_DOC)")
        return ""
    sig = str(inspect.signature(func))
    doc = format_docstring(inspect.getdoc(func))
    return f"{level}# `{name}{sig}`\n\n{doc}\n"


def gen_class_doc(cls):
    if should_skip(cls):
        print(f"Skipping {cls.__name__} (NO_PUBLIC_DOC)")
        return ""
    out = [f"## `{cls.__name__}`\n"]
    out.append(format_docstring(inspect.getdoc(cls)))
    methods = inspect.getmembers(cls, predicate=inspect.isfunction)
    for mname, mfunc in methods:
        if mname.startswith("_"):
            continue
        if should_skip(mfunc):
            print(f"Skipping {mname} (NO_PUBLIC_DOC)")
            continue
        sig = str(inspect.signature(mfunc))
        doc = format_docstring(inspect.getdoc(mfunc), indent=2)
        out.append(f"### `{mname}{sig}`\n\n{doc}\n")
    return "\n".join(out)


def gen_section(module_name: str, title: str) -> str:
    out = [f"# {title}\n"]
    module = importlib.import_module(module_name)

    # --- Functions ---
    funcs = [m for m in inspect.getmembers(module, inspect.isfunction) if not m[0].startswith("_")]
    funcs = [f for f in funcs if not should_skip(f[1])]
    for name, func in [m for m in inspect.getmembers(module, inspect.isfunction) if not m[0].startswith("_")]:
        if should_skip(func):
            print(f"Skipping {name} (NO_PUBLIC_DOC)")
    if funcs:
        out.append("## Functions\n")
        for name, func in funcs:
            out.append(gen_function_doc(name, func, level="##"))
    else:
        out.append("_No public functions found._\n")

    # --- Classes ---
    classes = [
        m for m in inspect.getmembers(module, inspect.isclass)
        if not m[0].startswith("_") and m[0] not in SKIP_CLASSES
    ]
    classes = [c for c in classes if not should_skip(c[1])]
    for name, cls in [m for m in inspect.getmembers(module, inspect.isclass) if not m[0].startswith("_")]:
        if should_skip(cls):
            print(f"Skipping {name} (NO_PUBLIC_DOC)")
    if classes:
        out.append("\n## Classes\n")
        for name, cls in classes:
            out.append(gen_class_doc(cls))

    return "\n".join(out)


def main():
    OUTPUT_FILE.parent.mkdir(parents=True, exist_ok=True)
    with open(OUTPUT_FILE, "w", encoding="utf-8") as f:
        for mod, title in MODULES:
            f.write(gen_section(mod, title))
            f.write("\n\n")
    print(f"✓ DSL reference written to {OUTPUT_FILE}")


if __name__ == "__main__":
    main()