"""YAML pipeline runner for scitq.

Reads a declarative YAML pipeline definition and compiles it into
a scitq Workflow using the scitq2_modules system.

Usage:
    python -m scitq2.yaml_runner pipeline.yaml --values '{"key": "val"}'
    python -m scitq2.yaml_runner pipeline.yaml --params
    python -m scitq2.yaml_runner pipeline.yaml --values '...' --dry-run
"""
import argparse
import ast
import difflib
import hashlib
import importlib
import json
import os
import re
import sys
from datetime import date, datetime, timezone
from typing import Any, Dict, List, Optional, Tuple, Union

import yaml

from scitq2.workflow import Workflow, Outputs, TaskSpec, Step
from scitq2.language import Shell, Raw, Python
from scitq2.recruit import WorkerPool, W
from scitq2.param import Param, ParamSpec, ProviderRegion, Path


# ---------------------------------------------------------------------------
# Param resolution
# ---------------------------------------------------------------------------

PARAM_TYPES = {
    'string': str, 'integer': int, 'boolean': bool,
    'path': Path, 'provider_region': ProviderRegion, 'enum': str,
}


def _apply_filter(value: str, filter_name: str) -> str:
    """Apply a named filter to a string value."""
    if filter_name == 'name':
        # basename without extension: "azure://rnd/resource/igc2.tgz" -> "igc2"
        base = value.rstrip('/').rsplit('/', 1)[-1]
        if '|' in base:
            base = base.split('|')[0]
        return base.rsplit('.', 1)[0] if '.' in base else base
    elif filter_name == 'basename':
        # basename with extension: "azure://rnd/resource/igc2.tgz" -> "igc2.tgz"
        base = value.rstrip('/').rsplit('/', 1)[-1]
        if '|' in base:
            base = base.split('|')[0]
        return base
    elif filter_name == 'dir':
        # parent directory name: "azure://rnd/resource/igc2.tgz" -> "resource"
        parts = value.rstrip('/').rsplit('/', 2)
        return parts[-2] if len(parts) >= 2 else value
    elif filter_name == 'reads':
        # depth enum to read count: "2x20M" -> "20000000", "1x10M" -> "10000000"
        import re
        m = re.match(r'(\d+)x(\d+)M', value)
        return str(int(m.group(2)) * 1_000_000) if m else value
    elif filter_name == 'total_reads':
        # depth enum to total read count (paired doubles): "2x20M" -> "40000000"
        import re
        m = re.match(r'(\d+)x(\d+)M', value)
        return str(int(m.group(1)) * int(m.group(2)) * 1_000_000) if m else value
    elif filter_name == 'is_paired':
        # depth enum to paired boolean: "2x20M" -> "true", "1x10M" -> "false"
        return 'true' if value.startswith('2x') else 'false'
    elif filter_name == 'not':
        # boolean negation: "true" → "false", "false" → "true"
        return 'false' if str(value).lower() in ('true', '1', 'yes') else 'true'
    elif filter_name == 'lower':
        return value.lower()
    elif filter_name == 'upper':
        return value.upper()
    elif filter_name == 'int':
        return str(int(float(value)))
    elif filter_name.startswith('format=') or filter_name.startswith('format '):
        # Format with Python %-style: {IDX|format=%04d} → "0042"
        fmt = filter_name.split('=', 1)[1] if '=' in filter_name else filter_name[7:]
        try:
            # Try numeric formatting first
            if 'd' in fmt or 'x' in fmt or 'o' in fmt:
                return fmt % int(float(value))
            elif 'f' in fmt or 'e' in fmt:
                return fmt % float(value)
            else:
                return fmt % value
        except (ValueError, TypeError):
            return value
    else:
        return value


def _eval_arithmetic(expr: str) -> str:
    """Safely evaluate simple arithmetic: +, -, *, /, max(), min(), int().
    Only allows numbers, operators, and safe builtins."""
    allowed = set('0123456789.+-*/() ,')
    # Check all non-function chars are safe
    cleaned = expr.replace('max', '').replace('min', '').replace('int', '')
    if not all(c in allowed or c.isspace() for c in cleaned):
        return expr
    try:
        result = eval(expr, {"__builtins__": {}}, {"max": max, "min": min, "int": int})
        return str(int(result)) if isinstance(result, (int, float)) else str(result)
    except Exception:
        return expr


# ---------------------------------------------------------------------------
# Extended expression evaluator (F').
#
# Powers `when:` and the `cond:` field of cond blocks: comparison (== != < <=
# > >=), regex match (`~`), boolean (and/or/not), and membership (in/not in)
# operators on top of the arithmetic _eval_arithmetic supports. AST-walked
# safe eval — anything outside the allow-listed node set bounces back as the
# original string, so unrelated call sites (paths, command bodies, URLs) keep
# flowing through unchanged.

class _SmartStr(str):
    """str subclass that auto-coerces to int/float when an ordering comparison
    against a number is requested. TSV column values arrive as strings; this
    lets `{sample.n_reads >= 1_000_000}` compare numerically without forcing
    the workflow author to write `int(...)` everywhere. Equality, hashing,
    membership, and arithmetic stay string-typed."""

    __slots__ = ()

    def _as_number(self):
        s = self.replace('_', '')  # accept Python's 1_000_000 digit grouping
        try:
            return int(s)
        except (ValueError, TypeError):
            pass
        try:
            return float(s)
        except (ValueError, TypeError):
            pass
        return None

    def __lt__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n < other
        return super().__lt__(other)

    def __le__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n <= other
        return super().__le__(other)

    def __gt__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n > other
        return super().__gt__(other)

    def __ge__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n >= other
        return super().__ge__(other)

    # Arithmetic: coerce self to number only when the other operand is a
    # number AND self looks numeric. `'42' + 5` → 47; `'abc' + 'def'` →
    # 'abcdef' (str concat unchanged); `'42' + '5'` → '425' (str concat
    # unchanged — user must write int('5') if numeric is desired).

    def __add__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n + other
        return super().__add__(other)

    def __radd__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return other + n
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n - other
        return NotImplemented

    def __rsub__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return other - n
        return NotImplemented

    def __mul__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n * other
        return super().__mul__(other)

    def __rmul__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return other * n
        return super().__rmul__(other)

    def __truediv__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n / other
        return NotImplemented

    def __rtruediv__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return other / n
        return NotImplemented

    def __floordiv__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n // other
        return NotImplemented

    def __mod__(self, other):
        if isinstance(other, (int, float)) and not isinstance(other, bool):
            n = self._as_number()
            if n is not None:
                return n % other
        return NotImplemented


# Internal name for the regex-match operator implementation. We rewrite
# `a ~ b` to `__re_match__(a, b)` before AST parsing because `~` isn't a
# binary operator in Python's grammar.
_RE_MATCH_FUNC = '__re_match__'


def _re_match(value, pattern):
    """Implements the `~` operator: re.search of `pattern` against `str(value)`."""
    return bool(re.search(str(pattern), str(value)))


# Atom shapes accepted as operands of `~`. Simple shapes only — complex
# nested expressions with `~` need explicit parens around the operands.
_REGEX_ATOM = (
    r"(?:[A-Za-z_]\w*(?:\.\w+)*)"    # ident, ident.attr.chain
    r"|(?:'(?:\\'|[^'])*')"           # 'single-quoted'
    r"|(?:\"(?:\\\"|[^\"])*\")"       # "double-quoted"
    r"|(?:\d+(?:\.\d+)?)"             # int or float
    r"|(?:\([^()]+\))"                # ( … ) one level
)
_REGEX_MATCH_PATTERN = re.compile(rf"({_REGEX_ATOM})\s*~\s*({_REGEX_ATOM})")


def _preprocess_regex_match(expr: str) -> str:
    """Rewrite each `<atom> ~ <atom>` as `__re_match__(<atom>, <atom>)`.
    Repeats until stable to handle multiple ~ in one expression."""
    prev = None
    while expr != prev:
        prev = expr
        expr = _REGEX_MATCH_PATTERN.sub(rf"{_RE_MATCH_FUNC}(\1, \2)", expr)
    return expr


# The ast.Num / ast.Str / ast.NameConstant nodes were deprecated in 3.8 and
# removed in 3.14; include them via getattr so the allowlist works on every
# supported runtime.
_ALLOWED_EXPR_NODES = tuple(t for t in (
    ast.Expression,
    ast.Constant,
    getattr(ast, 'Num', None),
    getattr(ast, 'Str', None),
    getattr(ast, 'NameConstant', None),
    ast.Name, ast.Load,
    ast.BinOp, ast.UnaryOp,
    ast.Add, ast.Sub, ast.Mult, ast.Div, ast.Mod, ast.FloorDiv, ast.Pow,
    ast.USub, ast.UAdd,
    ast.Compare,
    ast.Eq, ast.NotEq, ast.Lt, ast.LtE, ast.Gt, ast.GtE, ast.In, ast.NotIn,
    ast.BoolOp, ast.And, ast.Or, ast.Not,
    ast.Call,
    ast.Tuple, ast.List, ast.Set,
    ast.Attribute,
) if t is not None)

# Functions that may appear as a direct call target. `__re_match__` is the
# internal implementation of `~`, added by the pre-processor.
_ALLOWED_CALL_NAMES = frozenset({'max', 'min', 'int', 'float', 'str', 'len', _RE_MATCH_FUNC})


def _is_safe_expr_tree(tree: ast.AST, allowed_names: set) -> bool:
    """Walk the expression AST. Any unsupported node type or any name we
    didn't bind in the locals dict rejects the expression."""
    for node in ast.walk(tree):
        if not isinstance(node, _ALLOWED_EXPR_NODES):
            return False
        if isinstance(node, ast.Call):
            if not isinstance(node.func, ast.Name):
                return False
            if node.func.id not in _ALLOWED_CALL_NAMES:
                return False
        if isinstance(node, ast.Name):
            if node.id not in allowed_names and node.id not in _ALLOWED_CALL_NAMES:
                return False
    return True


def _build_eval_locals(params=None, itervar=None, extra_vars=None):
    """Assemble the locals namespace for _eval_expression. String values get
    wrapped in _SmartStr so a TSV-column comparison like `sample.n_reads >=
    1_000_000` auto-coerces.

    Dotted keys (e.g. `sample.depth_gb`) get aggregated into a synthesised
    `SimpleNamespace` under the prefix, so expressions can write
    `sample.depth_gb > 100` natively (Python attribute access) while
    template substitutions of the same key (`{sample.depth_gb}`) keep going
    through the existing flat-key path in `_resolve_refs`."""
    locals_dict = {
        _RE_MATCH_FUNC: _re_match,
        'max': max, 'min': min,
        'int': int, 'float': float, 'str': str, 'len': len,
        'True': True, 'False': False, 'None': None,
    }
    if params is not None:
        locals_dict['params'] = params
    if itervar:
        for k, v in itervar.items():
            if k in locals_dict:
                continue
            locals_dict[k] = _SmartStr(v) if isinstance(v, str) else v
    if extra_vars:
        for k, v in extra_vars.items():
            if k in locals_dict:
                continue
            locals_dict[k] = _SmartStr(v) if isinstance(v, str) else v

    # Synthesise namespaces from `<prefix>.<attr>` keys. Only one level of
    # nesting (no `a.b.c`); attr must be a valid Python identifier so it can
    # be accessed via attribute syntax. Skip prefixes that already have a
    # complex (non-scalar) value bound — don't clobber an existing namespace
    # or callable.
    from types import SimpleNamespace as _SN
    ns_attrs: Dict[str, Dict[str, Any]] = {}
    for k, v in list(locals_dict.items()):
        if not isinstance(k, str) or '.' not in k:
            continue
        if k.count('.') != 1:
            continue
        prefix, attr = k.split('.', 1)
        if not attr.isidentifier():
            continue
        ns_attrs.setdefault(prefix, {})[attr] = v
    for prefix, attrs in ns_attrs.items():
        existing = locals_dict.get(prefix)
        if existing is not None and not isinstance(existing, (str, int, float, bool, type(None))):
            continue
        locals_dict[prefix] = _SN(**attrs)
    return locals_dict


def _eval_expression(expr, locals_dict):
    """Safely evaluate an expression string with the given locals.

    Returns the Python result, or the original `expr` string when parsing
    fails or the AST contains an unsupported construct. The string-fallback
    is the safety hatch: a path, URL, or plain value that isn't an
    expression flows through unchanged."""
    if not isinstance(expr, str):
        return expr
    expr = expr.strip()
    if not expr:
        return expr
    # Cosmetic: a leading-and-trailing `{…}` is the same syntactic wrapper
    # the rest of the YAML uses for substitutions; treat it as no-op here.
    if expr.startswith('{') and expr.endswith('}'):
        inner = expr[1:-1].strip()
        # Only strip when the braces actually balance over the whole string
        # (avoid breaking `{a} + {b}` shaped expressions, though those don't
        # land here in practice).
        depth = 0
        balanced = True
        for ch in expr[:-1]:
            if ch == '{':
                depth += 1
            elif ch == '}':
                depth -= 1
                if depth == 0:
                    balanced = False
                    break
        if balanced:
            expr = inner

    expr = _preprocess_regex_match(expr)

    try:
        tree = ast.parse(expr, mode='eval')
    except SyntaxError:
        return expr

    if not _is_safe_expr_tree(tree, set(locals_dict.keys())):
        return expr

    try:
        return eval(
            compile(tree, '<expression>', 'eval'),
            {'__builtins__': {}},
            locals_dict,
        )
    except Exception:
        return expr


def _eval_template_expression(template, params=None, itervar=None, extra_vars=None):
    """Top-level entry for `when:` and `cond:` evaluation.

    The template can be a bare expression (`params.profile in ('a', 'b')`)
    or the same wrapped in `{…}` for consistency with template-substitution
    syntax. Bare names `params`, `<ITER_KEY>`, and any extra_vars entry
    resolve directly; string values auto-coerce in numeric comparisons via
    _SmartStr."""
    locals_dict = _build_eval_locals(params, itervar, extra_vars)
    return _eval_expression(template, locals_dict)


# Token patterns the expression evaluator handles that a bare ref does not.
# When `_looks_like_expression` returns True, _resolve_cond and friends route
# to _eval_template_expression first; otherwise they keep using the existing
# substitution path.
_EXPR_SIGNALS = re.compile(
    r"(==|!=|<=|>=|<|>|\sand\s|\sor\s|\snot\s|\snot\sin\s|\sin\s|\s~\s)"
)


def _looks_like_expression(s: str) -> bool:
    """True if the string contains tokens that only the expression evaluator
    knows how to handle. Single bare refs (`params.x`, `{params.x}`, `SAMPLE`)
    intentionally don't trigger this — they keep going through the existing
    template-substitution path so back-compat is preserved exactly."""
    if not isinstance(s, str):
        return False
    # Pad with spaces so the word-boundary patterns above match leading /
    # trailing keywords.
    return bool(_EXPR_SIGNALS.search(f" {s} "))


def _resolve_cond_match(val: dict, resolved):
    """Pick the branch of a cond block whose key matches the resolved value.

    Lifted out of _resolve_cond so the new expression-evaluator path can
    share the matcher with the legacy ref-resolution path."""
    candidates = [resolved]
    if isinstance(resolved, bool):
        candidates.extend(['true' if resolved else 'false', True if resolved else False])
    elif isinstance(resolved, str) and resolved.lower() in ('true', 'false'):
        bool_val = resolved.lower() == 'true'
        candidates.extend([bool_val, resolved.lower()])
    else:
        candidates.append(str(resolved))

    for candidate in candidates:
        if candidate in val and candidate != 'cond':
            return val[candidate]
    # String comparison fallback
    for k, v in val.items():
        if k == 'cond':
            continue
        if str(k).lower() == str(resolved).lower():
            return v
    # Truthy/falsy fallback: if keys include true/false, match on truthiness
    keys = {k for k in val if k != 'cond'}
    if True in keys or 'true' in keys or False in keys or 'false' in keys:
        is_truthy = bool(resolved) and resolved not in ('', 'false', 'False', 'No', 'no', 'none', 'None', '0')
        for candidate in ([True, 'true'] if is_truthy else [False, 'false']):
            if candidate in val:
                return val[candidate]
    # `default` branch as final fallthrough — lets a workflow express
    # "use this value when nothing else matched".
    if 'default' in val:
        return val['default']
    raise ValueError(f"cond: no match for {resolved!r} in {list(k for k in val if k != 'cond')}")


def _typed_literal(val: str, true_kw: str = 'True', false_kw: str = 'False') -> str:
    """Convert a resolved string value to a typed literal for a programming language."""
    if val.lower() in ('true', 'false'):
        return true_kw if val.lower() == 'true' else false_kw
    try:
        int(val)
        return val
    except ValueError:
        pass
    try:
        float(val)
        return val
    except ValueError:
        pass
    escaped = val.replace("\\", "\\\\").replace("'", "\\'")
    return f"'{escaped}'"


# Language-specific literal formatters: {VAR} → typed literal
_LITERAL_FORMATTERS = {
    'python': lambda val: _typed_literal(val, 'True', 'False'),
    'r':      lambda val: _typed_literal(val, 'TRUE', 'FALSE'),
}


def _resolve_refs(val, params, itervar=None, extra_vars=None, literal_format=None, _depth=0):
    """Resolve {params.x}, {ITER_VAR}, and {var|filter} references in a string.

    Recurses on resolved values so that a param default like
    `"azure://.../{params.bioproject|name}/{params.depth}/"` gets fully
    expanded when the param is referenced. Capped at depth 8 to break
    accidental cycles between mutually-referencing defaults.
    """
    if not isinstance(val, str):
        return val
    if _depth > 8:
        return val
    def repl(m):
        expr = m.group(1)
        # Split expression and filters: "params.catalog|name" -> ("params.catalog", ["name"])
        parts = expr.split('|')
        ref = parts[0]
        filters = parts[1:]

        # Handle default value: {VAR:default}
        default = None
        if ':' in ref:
            ref, default = ref.split(':', 1)

        # Resolve the reference
        resolved = None
        if ref.startswith('params.'):
            attr = ref[7:]
            val = getattr(params, attr, None)
            if val is None:
                if default is not None:
                    return _resolve_refs(default, params, itervar, extra_vars, _depth=_depth + 1)
                return ''
            resolved = str(val)
        elif itervar and ref in itervar:
            resolved = str(itervar[ref])
        elif extra_vars and ref in extra_vars:
            resolved = str(extra_vars[ref])
        else:
            if default is not None:
                return _resolve_refs(default, params, itervar, extra_vars, _depth=_depth + 1)
            return m.group(0)

        # Recursively expand any {…} placeholders inside the resolved value
        # before applying filters, so filters see the fully-substituted text.
        if '{' in resolved:
            resolved = _resolve_refs(resolved, params, itervar, extra_vars, _depth=_depth + 1)

        # Apply filters
        for f in filters:
            resolved = _apply_filter(resolved, f.strip())
        if literal_format:
            resolved = literal_format(resolved)
        return resolved
    # Only match {NAME} not preceded by $ (shell variables ${VAR} are left for the shell)
    # Restrict capture to identifier-like chars to avoid matching Python/JSON dict literals
    return re.sub(r'(?<!\$)\{([^\s\'"{},]+)\}', repl, val)


def _resolve_cond(val, params, itervar=None, step_fields=None, extra_vars=None):
    """Resolve a cond: block to its selected value.
    val is a dict with 'cond' key (the condition reference) and value keys.
    step_fields: additional fields from the step definition (e.g. paired: true).
    """
    if not isinstance(val, dict) or 'cond' not in val:
        return val
    cond_ref = val['cond']
    # If the cond field carries operators / comparisons / boolean ops, treat
    # it as an expression first (F'). The expression evaluator sees
    # `params`, `<ITER_KEY>`, and `extra_vars` directly; for `step_fields`
    # entries (`paired:` style), they're merged into extra_vars below.
    if isinstance(cond_ref, str) and _looks_like_expression(cond_ref):
        eval_extras = dict(extra_vars) if extra_vars else {}
        if step_fields:
            for k, v in step_fields.items():
                if k not in eval_extras:
                    eval_extras[k] = v
        result = _eval_template_expression(cond_ref, params, itervar, eval_extras)
        # _eval_expression returns the original string if it couldn't
        # parse/evaluate the expression. Anything else — bool, int, str
        # value — is a real evaluation result we should use.
        if not (isinstance(result, str) and result == cond_ref.strip().lstrip('{').rstrip('}').strip()):
            resolved = result
            return _resolve_cond_match(val, resolved)
    # First try resolving as param ref (handles {params.x} syntax)
    resolved = _resolve_refs(cond_ref, params, itervar, extra_vars)
    # If still unresolved (bare name), check extra_vars then step-level fields
    if resolved == cond_ref:
        if extra_vars and cond_ref in extra_vars:
            resolved = str(extra_vars[cond_ref])
        elif step_fields and cond_ref in step_fields:
            resolved = _resolve_refs(str(step_fields[cond_ref]), params, itervar, extra_vars)
    return _resolve_cond_match(val, resolved)


def _resolve_task_spec(ts_def, params, itervar=None, extra_vars=None):
    """Resolve a `task_spec:` block, including an optional top-level
    `cond:` that selects one of several override sub-dicts.

    Unlike `_resolve_field`, which discards sibling keys when it sees a
    `cond:`, `task_spec` lets the workflow mix always-applied scalar
    fields (e.g. `prefetch: "100%"`) with cond-switched overrides:

        task_spec:
          prefetch: "100%"          # always set
          cond: "{NUMA}"            # branch on NUMA value
          true:
            numa: "{NUMA}"          # NUMA-bound branch
          false:
            cpu: 32                 # cpu/mem branch
            mem: 64

    Returns a flat dict ready to be passed as `**ts_def` to TaskSpec.
    All `{var}` references (params, iter vars, step vars) are resolved.
    """
    if not isinstance(ts_def, dict):
        return ts_def
    if 'cond' not in ts_def:
        # No cond: just resolve references in each value.
        return {k: _resolve_field(v, params, itervar, step_fields=ts_def, extra_vars=extra_vars)
                for k, v in ts_def.items()}
    # Split top-level keys: scalars/lists stay as always-applied
    # siblings; dict-valued keys are treated as branch overrides.
    siblings = {}
    branches = {'cond': ts_def['cond']}
    for k, v in ts_def.items():
        if k == 'cond':
            continue
        if isinstance(v, dict):
            branches[k] = v
        else:
            siblings[k] = v
    chosen = _resolve_cond(branches, params, itervar, extra_vars=extra_vars)
    merged = {**siblings, **(chosen if isinstance(chosen, dict) else {})}
    # Resolve `{NUMA}`, `{params.x}` etc. in every value.
    return {k: _resolve_field(v, params, itervar, step_fields=merged, extra_vars=extra_vars)
            for k, v in merged.items()}


def _resolve_field(val, params, itervar=None, step_fields=None, extra_vars=None, literal_format=None):
    """Resolve a field value: handles cond: blocks, param references, filters, and arithmetic.

    Recurses into lists so that `{params.x}` placeholders nested inside a
    YAML list (e.g. `inputs: ["{params.uri_a}", "{params.uri_b}"]`) get
    substituted — without this, list elements were passed unchanged to
    `_resolve_inputs`, which then tried to interpret `{params.uri_a}` as a
    step reference and raised."""
    # Loop on cond so a `cond:` whose chosen branch is itself a `cond:`
    # block (nested conditionals — e.g. "if param X is empty, dispatch
    # on param Y; otherwise use param X") resolves all the way through.
    while isinstance(val, dict) and 'cond' in val:
        val = _resolve_cond(val, params, itervar, step_fields, extra_vars)
    if isinstance(val, list):
        return [_resolve_field(v, params, itervar, step_fields, extra_vars, literal_format) for v in val]
    if isinstance(val, str):
        val = _resolve_refs(val, params, itervar, extra_vars, literal_format=literal_format)
    # If result looks like arithmetic (contains operators and only numbers/operators/builtins), evaluate
    if isinstance(val, str) and any(op in val for op in ('*', '/', '+', '-')) and not val.startswith('/'):
        val = _eval_arithmetic(val)
    return val


# ---------------------------------------------------------------------------
# Params class builder
# ---------------------------------------------------------------------------

def _build_params_class(params_def: Dict[str, dict]) -> type:
    """Build a ParamSpec class from YAML param definitions."""
    namespace = {}
    for name, spec in params_def.items():
        typ = spec.get('type', 'string')
        kwargs = {}
        if spec.get('required'):
            kwargs['required'] = True
        if 'default' in spec:
            kwargs['default'] = spec['default']
        if 'help' in spec:
            kwargs['help'] = spec['help']
        if 'requires' in spec:
            kwargs['requires'] = spec['requires']
        if typ == 'enum':
            kwargs['choices'] = spec.get('choices', [])
            namespace[name] = Param.enum(**kwargs)
        elif typ == 'path':
            namespace[name] = Param.path(**kwargs)
        elif typ == 'text':
            namespace[name] = Param.text(**kwargs)
        elif typ == 'provider_region':
            namespace[name] = Param.provider_region(**kwargs)
        elif typ == 'integer':
            namespace[name] = Param.integer(**kwargs)
        elif typ == 'boolean':
            namespace[name] = Param.boolean(**kwargs)
        else:
            namespace[name] = Param.string(**kwargs)
    return ParamSpec('YAMLParams', (), namespace)


# ---------------------------------------------------------------------------
# Iterators
# ---------------------------------------------------------------------------

def _build_iterations(iterate_def, params, workflow_vars: Optional[Dict] = None) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    """Build the list of iteration dicts from the iterate: block.
    Returns (iterations, primary_source_type).
    Each iteration dict maps variable names to values.
    For URI/ENA/SRA sources, each dict also has a '_sample' key with the sample object.
    workflow_vars: best-effort workflow-level vars (resolved with whatever
    is known before iterations exist — typically RESOURCE_ROOT and any
    `vars:` block that doesn't depend on iteration counts). Lets
    iter-level expressions like `fastq_pair_filtering: "{PAIRED|not}"`
    reference them.
    """
    if iterate_def is None:
        return [{}], None

    # Conditional iterator: cond selects which sub-block to use
    if 'cond' in iterate_def:
        resolved = _resolve_cond(iterate_def, params, extra_vars=workflow_vars)
        if isinstance(resolved, dict):
            # The selected branch becomes the iterator definition. Inherit
            # iterator-level attributes from the parent if the branch
            # didn't override them — this lets a workflow set e.g.
            # `fastq_pair_filtering` once at the top instead of repeating
            # it in every cond branch.
            for inherit in ('name', 'match', 'fastq_pair_filtering'):
                if inherit not in resolved and inherit in iterate_def:
                    resolved[inherit] = iterate_def[inherit]
            return _build_iterations(resolved, params, workflow_vars=workflow_vars)

    # Single iterator (potentially with a `product:` outer-product dimension).
    items, source_type = _build_single_iterator(iterate_def, params, workflow_vars=workflow_vars)

    # `product:` — outer-product dimension on the primary iterator. Each
    # primary iteration is crossed with every product iteration; the
    # iteration dict gets the merged variables, so a primary `name: sample`
    # with `product: { name: chrom, source: list, values: [...] }` yields
    # iterations like `{sample: S1, chrom: chr1}`, with tag composed by
    # the step builder as `S1.chr1`. The product dimension is itself an
    # iterator spec, recursively validated, so it can also carry its own
    # `product:` for higher-dimensional crosses (rarely needed in practice).
    product_def = iterate_def.get('product')
    if product_def is not None:
        if not isinstance(product_def, dict):
            raise ValueError("`product:` must be an iterator spec (mapping)")
        product_items, _ = _build_iterations(product_def, params, workflow_vars=workflow_vars)
        crossed = []
        for primary in items:
            for secondary in product_items:
                merged = dict(primary)
                # Secondary variables overlay; conflict on a name is an
                # author bug (same iterator name on both dimensions).
                for k, v in secondary.items():
                    if k in merged and not k.startswith('_'):
                        raise ValueError(
                            f"`product:` iterator '{product_def.get('name')}' "
                            f"shadows primary key '{k}' — rename one")
                    merged[k] = v
                crossed.append(merged)
        items = crossed

    return items, source_type


# ---------------------------------------------------------------------------
# Strict YAML schema validation
# ---------------------------------------------------------------------------
#
# Catches typos like `resources:` for `resource:` (silently ignored before
# this validator existed) by enumerating every key each parser actually
# reads at each YAML level and rejecting anything outside that set.
#
# Allowed-key sets are deliberately conservative — module steps
# (`import:` / `module:`) pass arbitrary kwargs through to user-defined
# Python, so they're exempted from step-level validation.

def _check_unknown_keys(d: dict, allowed: set, context: str) -> None:
    """Fail loudly if `d` contains keys not in `allowed`.
    Suggests near-matches via difflib so typos are easy to spot.
    Keys starting with `_` are reserved for internal markers and skipped.
    """
    if not isinstance(d, dict):
        return
    unknown = [k for k in d.keys()
               if k not in allowed and not (isinstance(k, str) and k.startswith('_'))]
    if not unknown:
        return
    lines = [f"❌ {context}: unknown key(s)"]
    for k in unknown:
        match = difflib.get_close_matches(str(k), [str(a) for a in allowed], n=1)
        if match:
            lines.append(f"   - {k!r}: did you mean {match[0]!r}?")
        else:
            lines.append(f"   - {k!r}")
    lines.append(f"   Allowed: {sorted(allowed)}")
    print("\n".join(lines), file=sys.stderr)
    sys.exit(1)


TOPLEVEL_KEYS = {
    'format', 'name', 'version', 'description', 'tag',
    'params', 'vars', 'iterate', 'steps',
    'worker_pool', 'workspace', 'language', 'retry',
    'opportunistic', 'untrusted', 'publish_root', 'container',
    'optimize', 'run_strategy', 'skip_if_exists', 'no_recruiters',
    'scores',
    'chain',
}

# Chain entry — see specs/workflow_chain.md. `template` is required;
# everything else has a sensible default at the server.
CHAIN_ENTRY_KEYS = {
    'template', 'version', 'params', 'when', 'on', 'always_new',
}

# Native step (inline command/container). Module steps (import: / module:)
# forward arbitrary kwargs and are intentionally exempt from this list.
STEP_NATIVE_KEYS = {
    'name', 'container', 'tag', 'inputs', 'resource', 'command',
    'outputs', 'publish', 'task_spec', 'vars', 'depends', 'when',
    'worker_pool', 'skip_if_exists', 'accept_failure', 'language',
    'quality', 'retry', 'grouped', 'grouped_by', 'per_sample',
    # adhoc container builders (conda / apt / binary / pip)
    'conda', 'apt', 'binary', 'pip',
    # `requires:` lists prerequisite modules that get auto-injected as
    # synthetic steps by _expand_requires (which runs before _build_step).
    # The field is dead state by the time the validator runs — already
    # consumed — but it's a legitimate authored field that both inline
    # steps and module-imported steps can carry.
    'requires',
}

WORKER_POOL_KEYS = {
    'provider', 'region', 'cpu', 'mem', 'disk', 'gpumem',
    'max_recruited', 'task_batches',
}

PARAM_ENTRY_KEYS = {
    'type', 'required', 'default', 'choices', 'help', 'requires',
}

# Per-source attribute schemas. Each entry lists the keys an iterator
# of that source recognises. _build_single_iterator validates iter_def
# against the matching schema — unknown keys raise instead of being
# silently ignored. To add a new iterator type, add an entry here and
# wire its handler in _build_single_iterator / _discover_samples.
#
# Sources listed in FILE_GROUP_SOURCES additionally accept extra string
# keys as named file groups (e.g. `fastqs: "*.f*q.gz"`); other sources
# reject anything outside the schema.
ITERATOR_SCHEMAS: Dict[str, set] = {
    'uri':   {'name', 'source', 'uri', 'group_by', 'filter',
              'match', 'fastq_pair_filtering', 'download_method', 'product'},
    'ena':   {'name', 'source', 'identifier', 'group_by', 'where', 'filter',
              'match', 'fastq_pair_filtering', 'download_method', 'product'},
    'sra':   {'name', 'source', 'identifier', 'group_by', 'where', 'filter',
              'match', 'fastq_pair_filtering', 'download_method', 'product'},
    'range': {'name', 'source', 'start', 'end', 'step', 'product'},
    'list':  {'name', 'source', 'values', 'product'},
    # `lines`: each non-empty line becomes one iteration.
    #   file:    path on the YAML-runner host (server-side use).
    #   content: in-memory multi-line string, typically from a
    #            `type: text` param (CLI ships the content via the
    #            `@file` shorthand, UI uploads + ships content, or
    #            operator pastes directly).
    #   item:    when set, each line is treated as a URI / glob and
    #            stored verbatim as the named file group <item> on the
    #            sample (so `inputs: sample.<item>` works the same as
    #            the URI iterator's `fastqs:` pattern). Globs are
    #            expanded at task-start by the worker's downloader, not
    #            at submission, so this is O(1) submission cost even
    #            for thousands of samples.
    #   tag:     where the iteration tag (= `{NAME}` substitution) is
    #            derived from. Accepted values:
    #              "folder" — parent folder of the matched URIs (default
    #                         when `item:` is set);
    #              omitted  — the raw line value (current behavior;
    #                         default when `item:` is not set).
    'lines': {'name', 'source', 'file', 'content', 'item', 'tag'},
    # `tsv` / `csv`: tabular iterator. Each row becomes one iteration; the
    # columns are exposed as `{<iter-name>.<column>}` substitutions and as
    # `<iter-name>` attributes inside `when:` / `cond:` / `task_spec` /
    # other expression-aware fields.
    #   uri:     local path on the runner host (server-side use). Remote
    #            URI fetch is reserved for a future revision — see
    #            `specs/addition_from_nextflow.md` (C, "Local vs remote
    #            TSV transport").
    #   content: in-memory string, typically from a `type: text` param
    #            (CLI ships the content via the `@file` shorthand, UI
    #            uploads + ships content, or operator pastes directly).
    #   key:     column name to use as the iteration key. Defaults to the
    #            first column. Must be unique across rows.
    #   sep:     field separator. If unset, auto-detected from the URI
    #            extension (`.tsv` → tab, `.csv` → comma), defaulting to
    #            tab. `content` always defaults to tab unless `sep:` is
    #            set explicitly.
    'tsv':   {'name', 'source', 'uri', 'content', 'key', 'sep', 'product'},
}
FILE_GROUP_SOURCES = {'uri', 'ena', 'sra'}
# Internal markers added by the runner that are valid on any iterator.
ITERATOR_INTERNAL_KEYS = {'_is_first_step'}


def _extract_named_file_groups(iter_def: dict) -> Dict[str, str]:
    """Extract named file groups from an iterator definition.
    Any string key not in the source's schema is treated as a named file
    group (glob pattern). Only meaningful for FILE_GROUP_SOURCES; other
    sources reject extras at validation time.
    """
    source = iter_def.get('source', 'list')
    schema = ITERATOR_SCHEMAS.get(source, set())
    groups = {}
    for k, v in iter_def.items():
        if k in schema or k in ITERATOR_INTERNAL_KEYS:
            continue
        if isinstance(v, str):
            groups[k] = v
    return groups


def _derive_lines_tag(line: str, uris: List[str], kind: Optional[str], fallback: str = "") -> str:
    """Derive the iteration tag for the `lines` iterator in file-group mode.

    `kind` is the value of the iter-level `tag:` field. Currently only
    `"folder"` is supported (the parent folder name of the matched URIs).
    Future kinds could add `"basename"`, `"stem"`, etc.

    When the glob didn't resolve to any URI, we fall back to the basename
    of the line itself — better than an empty tag, and stable across runs.
    """
    if kind in (None, "folder"):
        # If URI.find resolved something, fallback is the discovered
        # folder name; prefer it. Otherwise, derive from the line.
        if fallback:
            return fallback
        if uris:
            # Last-resort: parent folder of the first matched URI
            sample_uri = uris[0]
            return sample_uri.rstrip("/").rsplit("/", 2)[-2] if "/" in sample_uri else sample_uri
        # No URIs at all — derive from the line. Strip a trailing glob
        # part and take the leaf folder name.
        bare = line.rstrip("/")
        if "*" in bare or "?" in bare or "[" in bare:
            # `s3://.../SRR1234/*.fq.gz` → parent of last `/`
            head, _, _ = bare.rpartition("/")
            bare = head
        return bare.rsplit("/", 1)[-1] if "/" in bare else bare
    raise ValueError(f"Unsupported `tag:` value for iterator 'lines': {kind!r} (expected 'folder')")


def _build_single_iterator(iter_def: dict, params, workflow_vars: Optional[Dict] = None) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    """Build iterations for a single iterator definition.
    workflow_vars: workflow-level vars resolved before iterations were
    built — passed through so iter-level expressions like
    `fastq_pair_filtering: "{PAIRED|not}"` can reference them.
    """
    name = iter_def['name']
    source = iter_def.get('source', 'list')
    uname = name.upper()

    # Extract named file groups (v2: fastqs: "*.f*q.gz", csvs: "*.csv", etc.)
    named_groups = _extract_named_file_groups(iter_def)

    # Validate iter_def keys against the source's schema. Unknown keys
    # error — except for FILE_GROUP_SOURCES (uri/ena/sra) where extra
    # string keys are treated as named file groups (handled by
    # _extract_named_file_groups above). To add a new attribute supported
    # by a source, register it in ITERATOR_SCHEMAS.
    schema = ITERATOR_SCHEMAS.get(source)
    if schema is None:
        raise ValueError(f"Unknown iterator source: {source!r}")
    extras = set(iter_def.keys()) - schema - ITERATOR_INTERNAL_KEYS
    if source in FILE_GROUP_SOURCES:
        # Extras that are strings are named file groups (already
        # consumed by named_groups). Non-string extras are real errors.
        unsupported = [k for k in extras if not isinstance(iter_def[k], str)]
    else:
        unsupported = sorted(extras)
    if unsupported:
        raise ValueError(
            f"Iterator '{name}' (source={source!r}) doesn't recognise "
            f"{sorted(unsupported)!r}. Supported keys for this source: "
            f"{sorted(schema)!r}."
        )

    if source in ('uri', 'ena', 'sra'):
        samples = _discover_samples(iter_def, params, named_groups=named_groups, workflow_vars=workflow_vars)
        # Apply match: filter (sample name pattern)
        match_pattern = iter_def.get('match')
        if match_pattern:
            import fnmatch
            match_pattern = _resolve_refs(match_pattern, params, extra_vars=workflow_vars)
            samples = [s for s in samples if fnmatch.fnmatch(s.sample_accession, str(match_pattern))]
        items = []
        for sample in samples:
            d = {uname: sample.sample_accession, '_sample': sample, '_source': source}
            items.append(d)
        return items, source

    elif source == 'range':
        start = int(_resolve_refs(str(iter_def.get('start', 1)), params))
        end = int(_resolve_refs(str(iter_def.get('end', 10)), params))
        step = int(_resolve_refs(str(iter_def.get('step', 1)), params))
        return [{uname: str(i)} for i in range(start, end + 1, step)], None

    elif source == 'list':
        values = iter_def.get('values', [])
        return [{uname: str(v)} for v in values], None

    elif source == 'lines':
        # Two ways to feed the iterator, mutually exclusive:
        #   file:    a path on the runner host
        #   content: an in-memory string (typically from a text param)
        # See _derive_lines_tag below for tag derivation rules.
        has_file = 'file' in iter_def
        has_content = 'content' in iter_def
        if has_file == has_content:
            raise ValueError(
                "Iterator 'lines' requires exactly one of `file:` or `content:` "
                f"(got {'both' if has_file else 'neither'})"
            )
        if has_file:
            filepath = _resolve_refs(iter_def['file'], params)
            with open(filepath) as f:
                lines = [l.strip() for l in f if l.strip()]
        else:
            content = _resolve_refs(iter_def['content'], params)
            lines = [l.strip() for l in str(content).splitlines() if l.strip()]

        item_name = iter_def.get('item')
        tag_kind = iter_def.get('tag')
        if item_name and tag_kind is None:
            # Default tag derivation when each line is a URI/glob: the
            # parent folder name. Matches what URI iterator does with
            # `group_by: folder`.
            tag_kind = 'folder'

        # Simple mode: no item → each line is the iteration value.
        # Kept untouched so existing templates using `source: lines` with
        # only `file:` set behave exactly as before.
        if not item_name:
            return [{uname: line} for line in lines], None

        # File-group mode: pass each line through as the named file
        # group's literal value. The worker's downloader
        # (`fetch/fetch.go:RcloneBackend.List/Copy`) already expands
        # remote globs via rclone filters at task-start time, so doing
        # a submission-time URI.find here would be unnecessary work —
        # 3397 sequential S3 list calls during template-run for a
        # SCAPIS-sized list was 5+ minutes of dead air before any task
        # could exist. Skipping the lookup makes submission instant and
        # parallelizes the expansion across N workers naturally.
        #
        # Tradeoffs flagged on purpose:
        #   - A line whose glob matches nothing fails at the task's
        #     download phase (clean F status) instead of being skipped
        #     with a warning at submission. Arguably better — a typo'd
        #     sample shouldn't silently disappear.
        #   - `task.inputs` stores the glob string, not pre-expanded
        #     URIs. UI/debug views show the glob until the worker
        #     downloads. The actual files downloaded are visible in
        #     the worker's log.
        from scitq2.uri import URIObject
        items = []
        for line in lines:
            tag_value = _derive_lines_tag(line, [], tag_kind)
            sample_obj = URIObject({
                'sample_accession': tag_value,
                'fastqs': [line],
            })
            sample_obj.file_groups = {item_name: [line]}
            items.append({uname: tag_value, '_sample': sample_obj, '_source': source})
        return items, source

    elif source == 'tsv':
        # Tabular iterator. Exactly one of `uri:` (local path on the
        # runner host) or `content:` (in-memory text) must be set. Each
        # row materialises one iteration; columns are exposed as
        # `<iter-name>.<col>` substitutions (and as attributes inside
        # expression-aware fields, via the namespace synthesised by
        # _build_eval_locals from the same dotted keys).
        import csv as _csv
        import io as _io

        has_uri = 'uri' in iter_def
        has_content = 'content' in iter_def
        if has_uri == has_content:
            raise ValueError(
                "Iterator 'tsv' requires exactly one of `uri:` or `content:` "
                f"(got {'both' if has_uri else 'neither'})"
            )

        if has_uri:
            uri_str = _resolve_refs(iter_def['uri'], params, extra_vars=workflow_vars)
            if '://' in uri_str:
                # Remote URI — deferred to a future revision (the local-
                # vs-remote transport question in the spec). Be explicit
                # rather than silently failing in csv.DictReader.
                raise ValueError(
                    f"Iterator 'tsv' with remote URI {uri_str!r} is not yet "
                    "supported. Use `content:` (e.g. from a `type: text` param) "
                    "or a local path on the runner host."
                )
            with open(uri_str) as f:
                content = f.read()
        else:
            content = _resolve_refs(iter_def['content'], params, extra_vars=workflow_vars)

        # Separator: explicit > auto-detect from extension > tab.
        sep = iter_def.get('sep')
        if sep is None:
            if has_uri and uri_str.lower().endswith('.csv'):
                sep = ','
            else:
                sep = '\t'

        reader = _csv.DictReader(_io.StringIO(content), delimiter=sep)
        fieldnames = reader.fieldnames or []
        if not fieldnames:
            raise ValueError(
                f"Iterator '{name}' (source=tsv): no header row found. "
                "TSV inputs must have a header line with column names."
            )

        # Key column: explicit > first column. Must exist among columns.
        key_col = iter_def.get('key', fieldnames[0])
        if key_col not in fieldnames:
            raise ValueError(
                f"Iterator '{name}' (source=tsv): key column {key_col!r} not in "
                f"columns {fieldnames!r}."
            )

        rows = list(reader)
        if not rows:
            return [], None

        # Enforce key uniqueness so the iter-tag (= step.task_name suffix
        # in downstream tasks) is stable across rows.
        seen_keys = set()
        for r in rows:
            k = r.get(key_col, '')
            if k in seen_keys:
                raise ValueError(
                    f"Iterator '{name}' (source=tsv): duplicate value {k!r} in "
                    f"key column {key_col!r}."
                )
            seen_keys.add(k)

        items = []
        for r in rows:
            tag = r[key_col]
            d = {uname: tag}
            for col, val in r.items():
                if col is None:
                    continue  # extra fields beyond the header
                # Dotted form for `{<iter-name>.<col>}` substitution and
                # for the F' expression evaluator's attribute access via
                # the synthesised namespace.
                d[f"{name}.{col}"] = '' if val is None else str(val)
            d['_source'] = source
            items.append(d)
        return items, source

    else:
        raise ValueError(f"Unknown iterator source: {source}")


def _discover_samples(iter_def: dict, params, named_groups: Optional[Dict[str, str]] = None,
                      workflow_vars: Optional[Dict] = None) -> list:
    """Discover samples from URI/ENA/SRA.
    named_groups: v2 named file groups (e.g. {'fastqs': '*.f*q.gz'}).
    workflow_vars: workflow-level vars resolved before iterations were
    built — passed through so iter-level expressions like
    `fastq_pair_filtering: "{PAIRED|not}"` can reference them.
    """
    source = iter_def.get('source', 'uri')

    # Determine the file filter: v2 named groups take precedence over v1 filter:
    # For URI source, use the first named group's glob (or fall back to filter:)
    filter_glob = iter_def.get('filter')
    if named_groups:
        # Use the first named group as the primary filter for discovery
        first_group_glob = next(iter(named_groups.values()))
        filter_glob = first_group_glob

    # `fastq_pair_filtering: true|false` (default false) is an explicit
    # opt-in for "I want only R1 from a paired-end source." Workflow
    # author declares intent — the iterator never silently filters.
    #   false → pass through whatever the source returned
    #   true  → keep only R1 (URI: trim via find_sample_parity;
    #                         ENA/SRA: pass layout=SINGLE so the
    #                         underlying biology classes set the
    #                         `@only-read1` URI option and don't even
    #                         fetch R2 over the wire).
    # Use case: `fastq_pair_filtering: "{PAIRED|not}"` — turns on R1-only
    # exactly when the workflow runs in single-end mode.
    fpf_raw = _resolve_field(iter_def.get('fastq_pair_filtering', False), params, extra_vars=workflow_vars)
    if isinstance(fpf_raw, bool):
        fastq_pair_filtering = fpf_raw
    else:
        fastq_pair_filtering = str(fpf_raw).strip().lower() in ('true', '1', 'yes')

    # ENA/SRA accept a layout flag that triggers "@only-read1" on paired
    # samples (and is a no-op on single-end ones). For URI we apply the
    # equivalent logic ourselves on the raw URI list.
    biology_layout = 'SINGLE' if fastq_pair_filtering else 'AUTO'

    # Resolve download_method: explicit iter_def field wins, else fall back
    # to the workflow `download_method` param (the biomscope/hermes
    # convention — users declare the param and expect it to flow into the
    # iterator automatically). "any" means "no override, use scitq's
    # built-in transport default."
    download_method = _resolve_refs(str(iter_def.get('download_method', '')), params)
    if not download_method and hasattr(params, 'download_method'):
        download_method = str(getattr(params, 'download_method') or '')
    if download_method == 'any':
        download_method = ''

    if source == 'uri':
        from scitq2.uri import URI
        from scitq2.biology import find_sample_parity, PAIRED
        uri = _resolve_refs(iter_def.get('uri', ''), params)
        group_by = iter_def.get('group_by', 'folder')
        if filter_glob:
            filter_glob = _resolve_refs(str(filter_glob), params)
        samples = URI.find(uri, group_by=group_by, filter=filter_glob,
                        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})
        # When the workflow opts in via `fastq_pair_filtering: true`,
        # trim each sample's fastq list to R1 — but ONLY when the
        # sample's files actually look like an R1/R2 pair. Single-end
        # samples (or anything we can't classify) pass through as-is.
        # Never drop a sample.
        if fastq_pair_filtering:
            for sample in samples:
                parity = find_sample_parity(sample.fastqs)
                if parity['detected'] == PAIRED:
                    sample.fastqs = parity['R1']
        # Store named file groups on each sample (URI: generic 'files' default)
        for sample in samples:
            sample.file_groups = {k: sample.fastqs for k in named_groups} if named_groups else {'files': sample.fastqs}
        return samples
    elif source == 'ena':
        from scitq2.biology import ENA, SampleFilter, S
        identifier = _resolve_refs(iter_def.get('identifier', ''), params)
        group_by = iter_def.get('group_by', 'sample_accession')
        # v2: where: replaces dict-form filter:
        filter_def = iter_def.get('where') or iter_def.get('filter', {})
        sf = None
        if filter_def and isinstance(filter_def, dict):
            conditions = [getattr(S, k) == v for k, v in filter_def.items()]
            sf = SampleFilter(*conditions) if conditions else None
        samples = ENA(
            identifier=identifier, group_by=group_by, filter=sf, layout=biology_layout,
            use_ftp=(download_method == 'ena-ftp'),
            use_aspera=(download_method == 'ena-aspera'),
        )
        for sample in samples:
            if named_groups:
                sample.file_groups = {k: sample.fastqs for k in named_groups}
            else:
                sample.file_groups = {'fastqs': sample.fastqs, 'files': sample.fastqs}
        return samples
    elif source == 'sra':
        from scitq2.biology import SRA
        identifier = _resolve_refs(iter_def.get('identifier', ''), params)
        group_by = iter_def.get('group_by', 'sample_accession')
        # v2: where: for SRA too
        sra_method = download_method if download_method in ('sra-tools', 'sra-aws') else 'sra-aws'
        samples = SRA(
            identifier=identifier, group_by=group_by, layout=biology_layout,
            download_method=sra_method,
        )
        for sample in samples:
            if named_groups:
                sample.file_groups = {k: sample.fastqs for k in named_groups}
            else:
                sample.file_groups = {'fastqs': sample.fastqs, 'files': sample.fastqs}
        return samples
    raise ValueError(f"Unknown sample source: {source}")


# ---------------------------------------------------------------------------
# Worker pool / language
# ---------------------------------------------------------------------------

def _build_worker_pool(wp_def: dict, params, extra_vars: Optional[Dict] = None) -> WorkerPool:
    """Build a WorkerPool from YAML definition."""
    _check_unknown_keys(wp_def, WORKER_POOL_KEYS, "worker_pool")
    filters = []

    # Provider/region from explicit field. Accepts:
    #   - ProviderRegion (from a `type: provider_region` param) → tries
    #     prefix-match on provider so "azure" matches "azure.primary".
    #   - Plain string. May be "<provider>:<region>" or just "<provider>".
    #     Without this branch, a literal `provider: "local.local"` was
    #     silently ignored — the workflow's workspace then leaked into
    #     the recruiter's protofilter, recruiting an unintended worker.
    provider_ref = wp_def.get('provider')
    region_ref = wp_def.get('region')
    if provider_ref:
        resolved = _resolve_refs(provider_ref, params)
        if isinstance(resolved, ProviderRegion):
            filters.append(W.provider.like(f"{resolved.provider}%"))
            filters.append(W.region == resolved.region)
        elif isinstance(resolved, str) and resolved:
            if ':' in resolved:
                prov, reg = resolved.split(':', 1)
                filters.append(W.provider == prov)
                filters.append(W.region == reg)
            else:
                filters.append(W.provider == resolved)
                if region_ref:
                    region_resolved = _resolve_refs(region_ref, params)
                    if isinstance(region_resolved, str) and region_resolved:
                        filters.append(W.region == region_resolved)
    elif region_ref:
        region_resolved = _resolve_refs(region_ref, params)
        if isinstance(region_resolved, str) and region_resolved:
            filters.append(W.region == region_resolved)

    for field in ('cpu', 'mem', 'disk', 'gpumem'):
        if field in wp_def:
            val = wp_def[field]
            if isinstance(val, str):
                # Resolve refs and arithmetic first
                val = _resolve_field(val, params, extra_vars=extra_vars)
                m = re.match(r'(>=|<=|>|<|==)\s*(\d+)', str(val))
                if m:
                    op, num = m.group(1), int(m.group(2))
                    w_field = getattr(W, field)
                    if op == '>=': filters.append(w_field >= num)
                    elif op == '<=': filters.append(w_field <= num)
                    elif op == '>': filters.append(w_field > num)
                    elif op == '<': filters.append(w_field < num)
                    elif op == '==': filters.append(w_field == num)
            else:
                filters.append(getattr(W, field) >= val)

    kwargs = {}
    if 'max_recruited' in wp_def:
        val = _resolve_refs(str(wp_def['max_recruited']), params)
        kwargs['max_recruited'] = int(val)
    if 'task_batches' in wp_def:
        val = _resolve_refs(str(wp_def['task_batches']), params)
        kwargs['task_batches'] = int(val)
    return WorkerPool(*filters, **kwargs)


def _resolve_language(lang_str: Optional[str]):
    """Resolve language string to a Language object. Default: Shell('sh')."""
    if lang_str is None or lang_str == 'sh':
        return Shell('sh')
    elif lang_str == 'bash':
        return Shell('bash')
    elif lang_str == 'python':
        return Python()
    elif lang_str == 'r':
        return Shell('Rscript')
    elif lang_str == 'none':
        return Raw()
    else:
        return Shell(lang_str)


# ---------------------------------------------------------------------------
# Module import
# ---------------------------------------------------------------------------

def _import_python_module(module_name: str):
    """Import a Python module function from scitq2_modules or other package."""
    if '.' in module_name:
        mod_path, func_name = module_name.rsplit('.', 1)
    else:
        mod_path = module_name
        func_name = module_name
    try:
        mod = importlib.import_module(f'scitq2_modules.{mod_path}')
        return getattr(mod, func_name)
    except (ImportError, AttributeError):
        pass
    # Try as a fully qualified import (e.g. gmt_modules.hermes)
    try:
        parts = module_name.rsplit('.', 1)
        if len(parts) == 2:
            mod = importlib.import_module(parts[0])
            return getattr(mod, parts[1])
        mod = importlib.import_module(module_name)
        for name in dir(mod):
            if not name.startswith('_'):
                obj = getattr(mod, name)
                if callable(obj):
                    return obj
    except (ImportError, AttributeError):
        pass
    raise ImportError(f"Cannot import module '{module_name}'")


def _find_public_module_dir() -> str:
    """Find the scitq2_modules/yaml directory (shipped with scitq)."""
    import scitq2_modules
    base = os.path.dirname(scitq2_modules.__file__)
    return os.path.join(base, 'yaml')


def _split_module_ref(ref: str) -> Tuple[str, Optional[str]]:
    """Split 'genomics/fastp@1.2.0' → ('genomics/fastp', '1.2.0').
    'genomics/fastp' or 'genomics/fastp@latest' → ('genomics/fastp', None).
    Strips a trailing .yaml/.yml from the path part for tolerance.
    """
    version: Optional[str] = None
    path = ref
    if '@' in ref:
        path, version = ref.rsplit('@', 1)
        if version == 'latest':
            version = None
    for ext in ('.yaml', '.yml'):
        if path.endswith(ext):
            path = path[: -len(ext)]
            break
    return path, version


# Cached gRPC client for server-side module lookups. Reused across calls in
# the same runner process; lazily constructed so dry-runs without a server
# reachable fall back to package-only resolution cleanly.
_module_client = None
_module_client_failed = False


def _module_server_client():
    """Return a Scitq2Client configured for server-side module fetches, or
    None if no server is reachable / configured. Result is cached."""
    global _module_client, _module_client_failed
    if _module_client is not None:
        return _module_client
    if _module_client_failed:
        return None
    if not os.environ.get('SCITQ_SERVER') or not os.environ.get('SCITQ_TOKEN'):
        _module_client_failed = True
        return None
    try:
        from scitq2.grpc_client import Scitq2Client
        _module_client = Scitq2Client()
        return _module_client
    except Exception as e:
        print(f"⚠️ module library: cannot connect to server for module fetch ({e}); falling back to local package",
              file=sys.stderr)
        _module_client_failed = True
        return None


# Pins accumulator: the yaml_runner records every (ref, path, version) it
# resolves via the module library so a follow-up RPC can snapshot them into
# template_run.module_pins. Populated as _load_module* resolve references.
_resolved_module_pins: list = []


def _load_module_from_server(path: str, version: Optional[str]) -> Optional[dict]:
    """Try to fetch a module's YAML content from the server-side library.
    Returns parsed dict on success, None on any failure (missing module,
    no server, auth error, etc.) — the caller falls back to its local
    search strategy.
    """
    client = _module_server_client()
    if client is None:
        return None
    ref = path if not version else f"{path}@{version}"
    try:
        import taskqueue_pb2  # type: ignore  # noqa: F401 — just ensures the module is importable
    except ImportError:
        pass
    try:
        from scitq2.pb import taskqueue_pb2 as pb
        resp = client.stub.DownloadModule(pb.DownloadModuleRequest(filename=ref))
    except Exception:
        # Not-found / auth / network — fall through to local search. Keep
        # quiet here because missing-on-server is a legitimate outcome for
        # bare `import: genomics/fastp` when the server hasn't been seeded
        # with bundled modules yet (Phase 3).
        return None
    if not resp or not resp.content:
        return None
    resolved_name = resp.filename or ref
    resolved_version = version
    if '@' in resolved_name:
        _, resolved_version = resolved_name.rsplit('@', 1)
    _resolved_module_pins.append({
        'ref': ref,
        'path': path,
        'version': resolved_version,
        'source': 'server',
    })
    return yaml.safe_load(resp.content)


# --offline mode: bypass the server and read modules from a filesystem
# tree. Set by main() before any step compilation begins. See
# specs/module_library.md.
_offline_mode: bool = False
_offline_module_path: Optional[str] = None
# One-time-per-run flag for the `module:` deprecation warning.
_module_keyword_deprecation_warned: bool = False


def _offline_load(path: str, ref: str) -> dict:
    """Read a module from `_offline_module_path` (or the installed
    scitq2_modules/yaml/ by default). No server, no versioning. Only used
    when --offline is set."""
    base_dir = _offline_module_path or _find_public_module_dir()
    for ext in ('.yaml', '.yml', ''):
        full = os.path.join(base_dir, path + ext)
        if os.path.exists(full):
            with open(full) as f:
                data = yaml.safe_load(f)
            _resolved_module_pins.append({
                'ref': ref,
                'path': path,
                'version': (data or {}).get('version'),
                'source': 'offline',
            })
            return data
    raise FileNotFoundError(
        f"Module not found in offline tree: {ref} "
        f"(searched {base_dir} for {path}.yaml)"
    )


def _load_public_import(import_name: str) -> dict:
    """Load a module by reference from the server library. No fallback to
    the Python package or filesystem in online mode — the library is the
    single source of truth at runtime. In --offline mode, reads from
    `_offline_module_path` instead (default: installed scitq2_modules/yaml/).
    Accepts `path` or `path@version`.
    """
    path, version = _split_module_ref(import_name)

    if _offline_mode:
        return _offline_load(path, import_name)

    mod = _load_module_from_server(path, version)
    if mod is not None:
        return mod

    raise FileNotFoundError(
        f"Module '{import_name}' not found in library. "
        f"If this is a bundled module, the server may need seeding: run "
        f"`scitq module upgrade --apply` (admin). "
        f"If it is a private module, upload it first with "
        f"`scitq module upload --path X --as <namespace>/<name>`."
    )


def _load_private_module(module_path: str, pipeline_dir: Optional[str] = None,
                         script_root: Optional[str] = None) -> dict:
    """Loads a module by `path[.yaml]`. Internally a thin wrapper over
    `_load_public_import` that strips a trailing `.yaml`/`.yml` so a
    `module: foo.yaml` reference maps to the library path `foo`.

    Used in two places: the user-facing `module:` keyword (deprecated —
    the warning fires at the call site, not here, so internal callers
    using this as a fallback path don't print spurious warnings on
    every legitimate `import:` lookup miss) and `_load_module_by_ref`'s
    fallback chain. pipeline_dir / script_root are kept for signature
    compatibility but unused; see specs/module_library.md.
    """
    stripped = module_path
    for ext in ('.yaml', '.yml'):
        if stripped.endswith(ext):
            stripped = stripped[: -len(ext)]
            break
    return _load_public_import(stripped)


# ---------------------------------------------------------------------------
# Ad-hoc container handling
# ---------------------------------------------------------------------------

def _resolve_adhoc_container(step_def: dict, workflow, registry: str = "gmtscience") -> Tuple[Optional[str], Optional[Step]]:
    """If step has conda/apt/binary/pip, generate a preparation step.
    Returns (image_name, prep_step) or (None, None)."""
    for install_type in ('conda', 'apt', 'binary', 'pip'):
        if install_type not in step_def:
            continue
        spec_val = step_def[install_type]
        spec_key = f"{install_type}:{spec_val}"
        tag = hashlib.md5(spec_key.encode()).hexdigest()[:12]
        image_name = f"scitq_adhoc_{tag}"

        if install_type == 'conda':
            dockerfile = f"FROM {registry}/mamba\nRUN _conda install {spec_val}"
        elif install_type == 'apt':
            dockerfile = f"FROM ubuntu:latest\nRUN apt-get update && apt-get install -y {spec_val} && rm -rf /var/lib/apt/lists/*"
        elif install_type == 'binary':
            bin_name = spec_val.rsplit('/', 1)[-1]
            dockerfile = f"FROM alpine\nRUN wget -O /usr/local/bin/{bin_name} {spec_val} && chmod +x /usr/local/bin/{bin_name}"
        elif install_type == 'pip':
            dockerfile = f"FROM python:3.12-slim\nRUN pip install {spec_val}"

        build_cmd = (
            f'docker image inspect {image_name} >/dev/null 2>&1 || '
            f'docker build -t {image_name} -f - <<\'DOCKERFILE\'\n{dockerfile}\nDOCKERFILE'
        )
        prep_step = workflow.Step(
            name=f"_prepare_{tag}",
            container="docker:cli",
            command=build_cmd,
            language=Shell('sh'),
            task_spec=TaskSpec(concurrency=1, prefetch=0),
        )
        return image_name, prep_step

    return None, None


# ---------------------------------------------------------------------------
# Step input resolution
# ---------------------------------------------------------------------------

def _resolve_inputs(input_ref: str, step_map: Dict[str, Step], grouped: bool = False,
                    itervar: Optional[Dict] = None,
                    iterations: Optional[List[Dict]] = None):
    """Resolve 'step_name.output_name' or 'iterator.group' to inputs, or pass through raw URIs.

    For per-sample steps, `itervar` carries the current iteration. For grouped
    (fan-in) steps `itervar` is None but `iterations` carries the full list of
    iterations — an iterator-shaped reference (e.g. `sample.fastqs`) on a
    grouped step then resolves to the union of that file group across every
    sample, sparing the workflow author from inserting a no-op pass-through
    step purely to give the resolver an upstream Step to look up.

    For grouped_by steps, `itervar` carries `_group_iterations` (the subset of
    iterations sharing this group's key value); iterator-shaped refs collect
    file groups across that subset, and step.output refs return the list of
    per-task outputs from upstream tasks whose iteration was in the subset.
    """
    if not input_ref:
        return None
    # Raw URI — pass through as-is (e.g. "s3://bucket/path/", "azure://container/path/")
    if '://' in input_ref:
        return input_ref
    parts = input_ref.split('.')

    # Pick the iteration source: a single current itervar for per-sample
    # resolution, the filtered subset for grouped_by, or the full iterations
    # list when this is a (fully) grouped step.
    iter_pool: Optional[List[Dict]] = None
    grouped_by_pool: Optional[List[Dict]] = None
    if itervar is not None and '_group_iterations' in itervar:
        grouped_by_pool = itervar['_group_iterations']
        iter_pool = grouped_by_pool
    elif itervar is not None and '_sample' in itervar:
        iter_pool = [itervar]
    elif grouped and iterations:
        iter_pool = iterations

    if iter_pool:
        # The iterator name is the same across all iterations — read it once.
        iter_name = None
        for k in iter_pool[0]:
            if not k.startswith('_'):
                iter_name = k.lower()
                break
        if iter_name and parts[0].lower() == iter_name:
            if len(parts) == 2:
                # sample.fastqs — named file group, collected across iter_pool
                group_name = parts[1]
                collected = []
                for iv in iter_pool:
                    sample = iv.get('_sample')
                    if sample is None:
                        continue
                    if hasattr(sample, 'file_groups') and group_name in sample.file_groups:
                        collected.extend(sample.file_groups[group_name])
                    elif hasattr(sample, group_name):
                        val = getattr(sample, group_name)
                        if isinstance(val, (list, tuple)):
                            collected.extend(val)
                        else:
                            collected.append(val)
                if not collected:
                    sample0 = iter_pool[0].get('_sample')
                    available = (list(sample0.file_groups.keys())
                                 if sample0 is not None and hasattr(sample0, 'file_groups')
                                 else ['fastqs'])
                    raise ValueError(f"Iterator '{parts[0]}' has no file group '{group_name}' "
                                     f"(available: {available})")
                return collected
            elif len(parts) == 1:
                # sample — all files (unnamed group / v1 filter:)
                collected = []
                for iv in iter_pool:
                    sample = iv.get('_sample')
                    if sample is not None:
                        collected.extend(sample.fastqs)
                return collected

    # Step reference
    if len(parts) == 2:
        step_name, output_name = parts
        if step_name in step_map:
            upstream = step_map[step_name]
            # Keyed-grouping: return a list of per-task outputs for upstream
            # tasks that came from one of this group's iterations. Matched
            # by tag-component subset (an upstream task tagged "S1" matches
            # iteration {sample:S1, chrom:chr1} because {S1} ⊆ {S1, chr1};
            # an upstream "S1.chr1" matches that same iteration by
            # equality). Works whether the upstream is per-iter, fully
            # grouped, or itself grouped_by.
            if grouped_by_pool is not None:
                matching = _tasks_for_group(upstream, grouped_by_pool)
                if not matching:
                    raise ValueError(
                        f"grouped_by: no upstream tasks of step '{step_name}' match "
                        f"any iteration in this group (upstream tags: {[t.tag for t in upstream.tasks]})")
                return [upstream.output(output_name, task=t) for t in matching]
            return upstream.output(output_name, grouped=grouped)
    elif len(parts) == 1 and parts[0] in step_map:
        return step_map[parts[0]].output(grouped=grouped)
    available = ', '.join(sorted(step_map.keys())) if step_map else '(none)'
    raise ValueError(f"Cannot resolve input: {input_ref} (available steps: {available})")


def _tasks_for_group(upstream_step, group_iterations: List[Dict]):
    """Filter upstream_step.tasks to those whose iteration belonged to one
    of `group_iterations`. Matched by tag-component subset: an iteration's
    natural per-iter tag is the dot-join of its non-internal values, so a
    task tagged with any subset of those components belongs to the group.

    This handles three upstream shapes uniformly:
      - per-iter upstream (tag = "S1.chr1") matches iteration {sample:S1, chrom:chr1};
      - grouped_by upstream (tag = "S1") matches the same iteration since {S1} ⊆ {S1,chr1};
      - fully-grouped upstream (no tag) matches nothing — use a plain
        inputs ref against the grouped step instead.

    Returns tasks in upstream.tasks order (stable submission order).
    """
    if upstream_step is None or not upstream_step.tasks:
        return []
    iter_componentsets = []
    for iv in group_iterations:
        iter_componentsets.append({str(v) for k, v in iv.items() if not k.startswith('_')})
    matches = []
    for t in upstream_step.tasks:
        if not getattr(t, 'tag', None):
            continue
        task_components = set(t.tag.split('.'))
        for iset in iter_componentsets:
            if task_components.issubset(iset):
                matches.append(t)
                break
    return matches


def _load_module_by_ref(ref: str, pipeline_dir: Optional[str] = None,
                        script_root: Optional[str] = None) -> Optional[dict]:
    """Unified loader for a module reference of the form `path[@version]`.
    Tries the server library first (via `_load_public_import`, which also
    falls back to the installed scitq2_modules package), then the private
    search paths so inline / pipeline-local modules work in dry-run too.
    Returns None if nothing resolves — the caller decides whether that's
    worth surfacing."""
    try:
        return _load_public_import(ref)
    except FileNotFoundError:
        pass
    try:
        return _load_private_module(ref, pipeline_dir=pipeline_dir, script_root=script_root)
    except FileNotFoundError:
        return None


def _freeze_for_dedup(value):
    """Convert a YAML value to a hashable form for set membership.
    Used to dedup auto-injected `requires:` steps by (path, vars)."""
    if isinstance(value, dict):
        return tuple(sorted((k, _freeze_for_dedup(v)) for k, v in value.items()))
    if isinstance(value, list):
        return tuple(_freeze_for_dedup(v) for v in value)
    return value


def _expand_requires(data: dict, pipeline_dir: Optional[str] = None,
                     script_root: Optional[str] = None) -> None:
    """Pre-process steps to honour module-level `requires:` declarations.

    A module with `requires: [<other_path>, ...]` pulls its companion
    modules into the workflow even if the template didn't explicitly list
    them. The typical use is a one-off setup step (e.g. a reference
    catalog download) alongside a per-sample compute step: the module
    author declares the pairing once, and template writers don't have to
    remember both.

    Behaviour:
    - Every module referenced from a step's `requires:` (or the inherited
      `requires:` of the module that step imports) that isn't already in
      the template's explicit `import:` / `module:` list is prepended to
      `steps:` as a `- import: <path>` synthetic step.
    - Transitive requires are resolved.
    - For each requiring step, the required modules' `name:` fields are
      appended to that step's `depends:` list, merging with any explicit
      `depends:` the user set.
    - Modules the user already imported explicitly are left exactly where
      they were in the template (so the user keeps full control over
      placement and parameters).
    """
    steps = data.get('steps') or []
    if not steps:
        return

    # What paths are already imported by the user? Normalise refs by
    # dropping @version + extensions so duplicates are detected across
    # notation variants.
    def _normalise_ref(ref: str) -> str:
        if '@' in ref:
            ref = ref.split('@', 1)[0]
        for ext in ('.yaml', '.yml'):
            if ref.endswith(ext):
                ref = ref[: -len(ext)]
                break
        return ref

    explicitly_imported = set()
    for step_def in steps:
        ref = step_def.get('import') or step_def.get('module')
        if isinstance(ref, str):
            explicitly_imported.add(_normalise_ref(ref))

    # Cache loaded module bodies so we don't hit the server twice.
    module_cache: Dict[str, Optional[dict]] = {}

    def _load(ref: str) -> Optional[dict]:
        norm = _normalise_ref(ref)
        if norm in module_cache:
            return module_cache[norm]
        mod = _load_module_by_ref(ref, pipeline_dir=pipeline_dir, script_root=script_root)
        module_cache[norm] = mod
        return mod

    # For each original step, collect its effective `requires:` list (from
    # the imported module and/or the step_def itself) and recursively
    # expand transitive requires into a set of module paths to ensure.
    # Each entry is (path, vars_inherited_from_requirer) so that vars
    # set on the requiring step (e.g. METAPHLAN_INDEX="mpa_custom") flow
    # into the auto-injected setup step. Without this, the catalog step
    # would publish to its module-default cache slot while the consuming
    # step looks up a different (user-pinned) slot — cache miss with
    # nothing to consume.
    needed: List[Tuple[str, Dict, Any]] = []  # (path, inherited_vars, inherited_when), in discovery order
    seen: set = set()  # (normalised_path, frozen_vars) already decided on

    def _collect_transitive(ref: str, inherited_vars: Dict, inherited_when: Any) -> None:
        norm = _normalise_ref(ref)
        # Dedup on (path, inherited_vars). Two steps requiring the same
        # module with the same vars inject only once; with different
        # vars (e.g. different METAPHLAN_INDEX) each gets its own
        # injection so each cache slot gets populated. `when:` is not
        # part of the dedup key — if multiple requirers with different
        # `when` conditions point at the same module + vars, the first
        # discovered wins. The right answer is OR-of-whens, but that
        # would need cond-merging logic; in practice today, requirers
        # that need different conditional gating tend to have different
        # vars too, so this falls out naturally.
        vars_key = tuple(sorted((k, _freeze_for_dedup(v)) for k, v in inherited_vars.items()))
        if (norm, vars_key) in seen:
            return
        seen.add((norm, vars_key))
        mod = _load(ref)
        if not mod:
            return
        for sub_req in mod.get('requires', []) or []:
            _collect_transitive(sub_req, inherited_vars, inherited_when)
        if norm not in explicitly_imported:
            needed.append((norm, inherited_vars, inherited_when))

    # First pass: walk the original steps to discover every transitively-
    # required module. Each step's own vars AND `when:` are inherited
    # into its required modules so:
    #   - per-step customisation propagates (vars)
    #   - skipping the requirer skips its setup deps too (when), so an
    #     auto-injected catalog download doesn't run for a workflow that
    #     has the consuming step disabled by `when:`.
    for step_def in steps:
        step_vars = step_def.get('vars') or {}
        if not isinstance(step_vars, dict):
            step_vars = {}
        step_when = step_def.get('when')
        ref = step_def.get('import') or step_def.get('module')
        if isinstance(ref, str):
            mod = _load(ref)
            if mod:
                for sub_req in mod.get('requires', []) or []:
                    _collect_transitive(sub_req, step_vars, step_when)
        for inline_req in step_def.get('requires', []) or []:
            _collect_transitive(inline_req, step_vars, step_when)

    # Build the final step list: injections first (in discovery order so
    # transitive prerequisites precede their consumers), then the user's
    # original steps in their original order.
    if needed:
        injections = []
        for path, inherited_vars, inherited_when in needed:
            inj: Dict[str, Any] = {'import': path}
            if inherited_vars:
                inj['vars'] = dict(inherited_vars)
            if inherited_when is not None:
                inj['when'] = inherited_when
            injections.append(inj)
        final_steps = injections + steps
        data['steps'] = final_steps
    else:
        final_steps = steps

    # Second pass: auto-wire `depends:` for every step — both user-written
    # and injected — using the required modules' `name:` fields. Running
    # this over the full list means an injected prep step that *itself*
    # has `requires:` gets its own depends wired too.
    for step_def in final_steps:
        requires: List[str] = []
        ref = step_def.get('import') or step_def.get('module')
        if isinstance(ref, str):
            mod = _load(ref)
            if mod:
                requires.extend(mod.get('requires', []) or [])
        for inline_req in step_def.get('requires', []) or []:
            requires.append(inline_req)

        if not requires:
            continue

        auto_deps: List[str] = []
        for req_path in requires:
            req_mod = _load(req_path)
            if not req_mod:
                continue
            req_name = req_mod.get('name')
            if req_name:
                auto_deps.append(req_name)
        if not auto_deps:
            continue

        existing = step_def.get('depends')
        if existing is None:
            existing_list = []
        elif isinstance(existing, str):
            existing_list = [existing]
        elif isinstance(existing, list):
            existing_list = list(existing)
        else:
            existing_list = [str(existing)]
        merged = list(existing_list) + [d for d in auto_deps if d not in existing_list]
        step_def['depends'] = merged[0] if len(merged) == 1 else merged


_MODULE_METADATA_KEYS = {'version', 'description', 'format'}


def _merge_module_step(module_data: dict, step_def: dict, exclude_key: str) -> dict:
    """Merge module data with step definition. Step overrides module,
    except 'vars' which are merged (step vars override individual module vars).

    Module-level metadata (`version`, `description`, `format`) is stripped:
    those describe the module file, not the step, and would otherwise
    pollute the merged step dict — the strict step-key validator would
    then reject them as unknown.
    """
    merged = {k: v for k, v in module_data.items() if k not in _MODULE_METADATA_KEYS}
    for k, v in step_def.items():
        if k == exclude_key:
            continue
        if k == 'vars' and 'vars' in merged:
            # Merge vars dicts: module vars as base, step vars override
            merged_vars = dict(merged.get('vars', {}))
            merged_vars.update(v)
            merged['vars'] = merged_vars
        else:
            merged[k] = v
    return merged


# ---------------------------------------------------------------------------
# Step builder
# ---------------------------------------------------------------------------

def _build_step(workflow: Workflow, step_def: dict, step_map: Dict[str, Step],
                params, itervar: Optional[Dict] = None, is_fan_in: bool = False,
                default_language: str = 'sh', script_root: Optional[str] = None,
                pipeline_dir: Optional[str] = None, workflow_vars: Optional[Dict] = None,
                iterations: Optional[List[Dict]] = None,
                grouped_by_key: Optional[str] = None,
                verbose: bool = False) -> Step:
    """Build a single step from a YAML definition."""

    # when: conditional — skip step if falsy. The value can be a literal
    # bool, a single ref like `{params.oral}`, or a full expression like
    # `sample.read_type == 'long'` / `params.n_reads >= 1_000_000` /
    # `params.profile in ('full', 'extended')` / `sample.path ~ '\\.bam$'`.
    when = step_def.get('when')
    if when is not None:
        if isinstance(when, str):
            resolved = _eval_template_expression(when, params, itervar, workflow_vars)
        else:
            resolved = when
        step_label = step_def.get('name', step_def.get('module', step_def.get('import', '?')))
        if not resolved or resolved in ('false', 'False', 'No', 'no', 'none', 'None', ''):
            if verbose:
                print(f"⏭️ Step '{step_label}' skipped (when: {when!r} → {resolved!r})", file=sys.stderr)
            return None
        if verbose:
            print(f"✅ Step '{step_label}' when: {when!r} → {resolved!r}", file=sys.stderr)

    # If the step's `vars:` block is itself a cond (e.g. dispatching the
    # whole var-set on a param), resolve it now so the subsequent
    # module-merge sees a flat dict. This lets a workflow opt out of
    # overriding specific module defaults by simply omitting them in
    # the matching cond branch — module defaults then fall through
    # naturally, which is what users want when "I don't want to specify
    # this var for these cases".
    if (
        'vars' in step_def
        and isinstance(step_def['vars'], dict)
        and 'cond' in step_def['vars']
    ):
        resolved_vars = _resolve_field(step_def['vars'], params, itervar)
        if not isinstance(resolved_vars, dict):
            raise ValueError(
                f"Step '{step_def.get('name', '?')}': vars cond must resolve to a dict; got {type(resolved_vars).__name__}"
            )
        step_def = dict(step_def)
        step_def['vars'] = resolved_vars

    # Resolve imports: public (import:) or private (module:) YAML modules
    # Supports nesting: a module can import: another module (e.g. a private
    # wrapper that import:s a public bundled module).
    if 'import' in step_def:
        module_data = _load_public_import(step_def['import'])
        while 'import' in module_data:
            nested = _load_public_import(module_data['import'])
            module_data = _merge_module_step(nested, module_data, exclude_key='import')
        merged = _merge_module_step(module_data, step_def, exclude_key='import')
        step_def = merged

    if 'module' in step_def:
        # Deprecation warning fires here — at the user-facing call site —
        # not inside `_load_private_module` (which is also used as an
        # internal fallback by `_load_module_by_ref`, where the warning
        # would be spurious).
        global _module_keyword_deprecation_warned
        if not _module_keyword_deprecation_warned:
            print("⚠️ 'module:' is deprecated; use 'import:' with the library path "
                  "(e.g. 'import: private/X'). The `.yaml` suffix is stripped "
                  "automatically for backward compatibility.", file=sys.stderr)
            _module_keyword_deprecation_warned = True
        module_ref = step_def['module']
        if module_ref.endswith('.yaml') or module_ref.endswith('.yml'):
            module_data = _load_private_module(module_ref, pipeline_dir, script_root)
            # Nested import: if the private module itself imports a public module
            if 'import' in module_data:
                public_data = _load_public_import(module_data['import'])
                module_data = _merge_module_step(public_data, module_data, exclude_key='import')
            merged = _merge_module_step(module_data, step_def, exclude_key='module')
            step_def = merged
            # Fall through to custom step logic below
        else:
            # Python module: module: gmt_modules.hermes
            func = _import_python_module(module_ref)
            meta_keys = {'module', 'inputs', 'grouped', 'per_sample', 'name', 'worker_pool', 'vars'}
            kwargs = {}
            for key, val in step_def.items():
                if key not in meta_keys:
                    kwargs[key] = _resolve_field(val, params, itervar)
            input_ref = step_def.get('inputs')
            if input_ref:
                kwargs['inputs'] = _resolve_inputs(input_ref, step_map, grouped=is_fan_in, itervar=itervar, iterations=iterations)
            if 'worker_pool' in step_def and isinstance(step_def['worker_pool'], dict):
                kwargs['worker_pool'] = _build_worker_pool(step_def['worker_pool'], params)
            sample = itervar.get('_sample') if itervar else None
            if sample is not None:
                return func(workflow, sample, **kwargs)
            else:
                return func(workflow, **kwargs)

    # Strict: native steps have a fixed key surface. Module steps
    # (`import:` / `module:`) forward arbitrary kwargs to user-defined
    # Python and are exempt — they returned early above.
    _step_label = step_def.get('name', '<unnamed>')
    _check_unknown_keys(step_def, STEP_NATIVE_KEYS, f"step '{_step_label}'")

    # Resolve ad-hoc container (conda/apt/binary/pip)
    adhoc_image, prep_step = _resolve_adhoc_container(step_def, workflow)

    # Resolve vars: workflow-level → module-level → step-level (each can use cond:)
    extra_vars = dict(workflow_vars or {})
    vars_def = step_def.get('vars', {})
    if vars_def:
        # Resolve vars in order — later vars can reference earlier ones
        for var_name, var_expr in vars_def.items():
            extra_vars[var_name] = _resolve_field(var_expr, params, itervar, step_fields=extra_vars, extra_vars=extra_vars)

    # Custom / inline / YAML-module step
    name = step_def.get('name', 'unnamed')
    language_str = step_def.get('language', default_language)
    literal_fmt = _LITERAL_FORMATTERS.get(language_str)
    command = _resolve_field(step_def.get('command', ''), params, itervar, step_fields=step_def, extra_vars=extra_vars,
                             literal_format=literal_fmt)
    container = adhoc_image or _resolve_field(step_def.get('container'), params, itervar, step_fields=step_def, extra_vars=extra_vars)

    # Fail on unresolved YAML variables in shell commands (not shell ${VAR}).
    # Skip for typed languages (python, r) where {NAME} can be valid syntax.
    if command and not literal_fmt:
        unresolved = re.findall(r'(?<!\$)\{([A-Z_][A-Z0-9_]*)\}', command)
        if unresolved:
            raise ValueError(f"Step '{name}': unresolved YAML variables in command: {unresolved}")

    # Prepend only vars that are actually referenced as ${VAR} in the command
    if extra_vars and command:
        used_vars = {k: v for k, v in extra_vars.items() if f'${{{k}}}' in command}
        if used_vars:
            exports = "\n".join(f'export {k}="{v}"' for k, v in used_vars.items())
            command = exports + "\n" + command

    step_kwargs = dict(
        name=name,
        command=command,
        language=_resolve_language(language_str),
    )
    if container:
        step_kwargs['container'] = container

    # Tag from iterator
    sample = itervar.get('_sample') if itervar else None
    if itervar and grouped_by_key:
        # Keyed-grouping step: tag is just the group key value, not the
        # composite — one task per distinct value of the grouped_by var.
        step_kwargs['tag'] = str(itervar.get(grouped_by_key, ''))
    elif itervar and not is_fan_in:
        # Build tag from all iterator variables (excluding internal keys)
        tag_parts = [str(v) for k, v in itervar.items() if not k.startswith('_')]
        step_kwargs['tag'] = '.'.join(tag_parts) if tag_parts else None

    # Inputs — resolve cond: if present, then handle string or list
    input_ref = step_def.get('inputs')
    if input_ref:
        # Resolve cond: on inputs
        input_ref = _resolve_field(input_ref, params, itervar, step_fields=step_def, extra_vars=extra_vars)
        if isinstance(input_ref, list):
            # Multiple input references — resolve each, normalise to lists,
            # then concatenate. _resolve_inputs returns a list for step / iter
            # references but a bare string for raw URI passthrough; without
            # the normalise step, `+`-ing the latter does *string* concat
            # ("uri1uri2…") rather than list concat — which is what the
            # downloader-bug-with-glued-URIs symptom was.
            resolved = [_resolve_inputs(ref.strip(), step_map, grouped=is_fan_in, itervar=itervar, iterations=iterations) for ref in input_ref]
            combined: list = []
            for r in resolved:
                if r is None:
                    continue
                if isinstance(r, list):
                    combined.extend(r)
                else:
                    combined.append(r)
            step_kwargs['inputs'] = combined
        elif isinstance(input_ref, str):
            step_kwargs['inputs'] = _resolve_inputs(input_ref, step_map, grouped=is_fan_in, itervar=itervar, iterations=iterations)
    elif sample is not None and step_def.get('_is_first_step'):
        # v1 backward compat: implicit first step input from iterator
        step_kwargs['inputs'] = sample.fastqs

    # Depends: wire a task-level dependency on one or more earlier steps
    # by *name*, without any data flow. Useful when a setup step publishes
    # content that a later step consumes via `resource:` (e.g. a reference
    # catalog fetched once and read by all per-sample compute tasks) —
    # `resource:` alone wouldn't force ordering because it's just a URI.
    #
    # Form:
    #   depends: setup_step_name
    # or:
    #   depends: [step_a, step_b]
    #
    # Referenced steps must already be built (they're looked up in
    # step_map). Since the runner builds one-off steps (per_sample: false)
    # before per-iteration steps, a per-sample step can safely depend on a
    # one-off setup step declared above it in the template.
    depends_def = step_def.get('depends')
    if depends_def:
        if isinstance(depends_def, str):
            dep_names = [depends_def]
        elif isinstance(depends_def, list):
            dep_names = [str(d) for d in depends_def]
        else:
            raise ValueError(f"Step '{name}': 'depends' must be a string or list of strings, got {type(depends_def).__name__}")
        resolved_depends = []
        for dep_name in dep_names:
            if dep_name not in step_map:
                raise ValueError(
                    f"Step '{name}': depends references unknown step '{dep_name}' "
                    f"(available: {sorted(step_map.keys())})"
                )
            resolved_depends.append(step_map[dep_name])
        step_kwargs['depends'] = resolved_depends

    # Resource
    resource = step_def.get('resource')
    if resource:
        if isinstance(resource, list):
            step_kwargs['resources'] = [_resolve_field(r, params, itervar, extra_vars=extra_vars) for r in resource]
        else:
            step_kwargs['resources'] = [_resolve_field(resource, params, itervar, extra_vars=extra_vars)]

    # Outputs
    outputs_def = step_def.get('outputs', {})
    publish = step_def.get('publish')
    if outputs_def or publish:
        out_kwargs = dict(outputs_def) if isinstance(outputs_def, dict) else {}
        if publish:
            out_kwargs['publish'] = True if publish is True else _resolve_field(publish, params, itervar, extra_vars=extra_vars)
        step_kwargs['outputs'] = Outputs(**out_kwargs)

    # TaskSpec — resolve cond: and reference templates ({NUMA},
    # {params.x}, ...) before constructing. The cond resolver here is
    # task-spec-aware: scalar siblings (e.g. prefetch: "100%") survive
    # the cond switch.
    ts_def = _resolve_task_spec(step_def.get('task_spec', {}), params,
                                itervar=itervar, extra_vars=extra_vars)
    if ts_def:
        step_kwargs['task_spec'] = TaskSpec(**ts_def)

    # Worker pool override
    if 'worker_pool' in step_def and isinstance(step_def['worker_pool'], dict):
        step_kwargs['worker_pool'] = _build_worker_pool(step_def['worker_pool'], params, extra_vars=extra_vars)

    # skip_if_exists
    if 'skip_if_exists' in step_def:
        step_kwargs['skip_if_exists'] = step_def['skip_if_exists']

    # accept_failure: allow dependencies to be satisfied even if prerequisite failed
    if 'accept_failure' in step_def:
        step_kwargs['accept_failure'] = step_def['accept_failure']

    # Quality scoring
    if 'quality' in step_def and isinstance(step_def['quality'], dict):
        from scitq2.workflow import Quality
        q_def = step_def['quality']
        q_kwargs = {'variables': q_def.get('variables', {})}
        if 'objectives' in q_def:
            q_kwargs['objectives'] = q_def['objectives']
        else:
            q_kwargs['formula'] = q_def.get('score', '')
        step_kwargs['quality'] = Quality(**q_kwargs)

    # Depends on prep step for adhoc containers
    if prep_step:
        step_kwargs['depends'] = prep_step

    return workflow.Step(**step_kwargs)


# ---------------------------------------------------------------------------
# Main runner
# ---------------------------------------------------------------------------

def run_yaml(data: dict, params_values: Optional[dict] = None,
             dry_run: bool = False, standalone: bool = True,
             pipeline_dir: Optional[str] = None,
             no_recruiters: bool = False,
             verbose: bool = False,
             opportunistic: bool = False,
             untrusted: Optional[List[str]] = None,
             extend_workflow_id: Optional[int] = None,
             retry_failed_only: bool = False) -> Optional[int]:
    """Run a YAML pipeline definition."""
    # Validate
    if 'name' not in data:
        print("❌ Missing required field: name", file=sys.stderr)
        sys.exit(1)
    if 'steps' not in data or not data['steps']:
        print("❌ Missing or empty: steps", file=sys.stderr)
        sys.exit(1)

    # YAML format version (default: 1 for backward compat)
    yaml_format = int(data.get('format', 1))

    # Strict: unknown top-level keys are typos.
    _check_unknown_keys(data, TOPLEVEL_KEYS, "top-level YAML")

    # Build params
    params_def = data.get('params', {})
    for _pname, _pdef in (params_def or {}).items():
        _check_unknown_keys(_pdef, PARAM_ENTRY_KEYS, f"param '{_pname}'")
    ParamsClass = _build_params_class(params_def)
    params = ParamsClass.parse(params_values or {})

    # Build workflow
    default_language = data.get('language', 'sh')
    wp_def = data.get('worker_pool', {'cpu': '>= 4', 'mem': '>= 8'})
    worker_pool = _build_worker_pool(wp_def, params)

    wf_kwargs = dict(
        name=data['name'],
        version=data.get('version', '1.0.0'),
        description=data.get('description', ''),
        language=_resolve_language(default_language),
        worker_pool=worker_pool,
        skip_if_exists=data.get('skip_if_exists', False),
        retry=data.get('retry'),
    )
    if data.get('tag'):
        wf_kwargs['tag'] = _resolve_refs(data['tag'], params)
    if data.get('container'):
        wf_kwargs['container'] = data['container']
    if data.get('publish_root'):
        wf_kwargs['publish_root'] = _resolve_refs(data['publish_root'], params)

    # Provider/region from workspace
    workspace_ref = data.get('workspace')
    if workspace_ref:
        resolved = _resolve_refs(workspace_ref, params)
        if isinstance(resolved, ProviderRegion):
            wf_kwargs['provider'] = resolved.provider
            wf_kwargs['region'] = resolved.region
        elif isinstance(resolved, str) and ':' in resolved:
            # Resolved to string "provider:region" — parse it
            provider, region = resolved.split(':', 1)
            wf_kwargs['provider'] = provider
            wf_kwargs['region'] = region

    # Live mode for optimization workflows
    if data.get('optimize'):
        wf_kwargs['live'] = True

    # Workflow-level run strategy (B=Batch default, T=Thread/sticky, D=Debug,
    # Z=Suspended). Friendly words (batch|thread|debug|suspended) accepted.
    if data.get('run_strategy'):
        wf_kwargs['run_strategy'] = data['run_strategy']

    workflow = Workflow(**wf_kwargs)

    # Resolve RESOURCE_ROOT from server if provider/region are set.
    # Policy: `{RESOURCE_ROOT}` is a hard dependency — if the template
    # actually uses it and we can't resolve it, fail loudly rather than
    # silently expanding to an empty string (which produces malformed
    # URIs like `/meteor2/hs_10_4_gut/` that only surface as obscure
    # fetch errors at worker runtime). If the template does NOT use it,
    # resolution failure is a non-event.
    from scitq2.grpc_client import Scitq2Client
    client = Scitq2Client()

    def _template_uses_resource_root(d):
        """Recursive scan — catches `resource:`, `publish:`, `command:`,
        `vars:`, module `requires:` imports pulled in elsewhere, and any
        other field that ultimately gets string-substituted."""
        if isinstance(d, str):
            return '{RESOURCE_ROOT}' in d
        if isinstance(d, dict):
            return any(_template_uses_resource_root(v) for v in d.values())
        if isinstance(d, (list, tuple)):
            return any(_template_uses_resource_root(v) for v in d)
        return False

    uses_resource_root = _template_uses_resource_root(data)
    resource_root = ''
    resolve_error = None

    if 'provider' in wf_kwargs and 'region' in wf_kwargs:
        try:
            resource_root = client.get_resource_root(
                provider=wf_kwargs['provider'], region=wf_kwargs['region'])
        except Exception as e:
            err_str = str(e)
            if 'UNAUTHENTICATED' in err_str or 'invalid session' in err_str:
                resolve_error = (f"{{RESOURCE_ROOT}} lookup failed: authentication rejected by server. "
                                 f"SCITQ_TOKEN may be invalid or expired. (server error: {e})")
            elif 'NotFound' in err_str or 'not found' in err_str.lower():
                resolve_error = (f"{{RESOURCE_ROOT}} has no value: no local_resources configured for "
                                 f"{wf_kwargs['provider']}:{wf_kwargs['region']} "
                                 f"(check scitq.yaml). (server error: {e})")
            else:
                resolve_error = (f"{{RESOURCE_ROOT}} lookup failed for "
                                 f"{wf_kwargs['provider']}:{wf_kwargs['region']}. "
                                 f"(server error: {e})")
    elif 'workspace' in data:
        resolve_error = ("{RESOURCE_ROOT} has no value: workspace: did not resolve to a "
                         "provider:region pair.")
    else:
        resolve_error = ("{RESOURCE_ROOT} has no value: no 'workspace:' directive in the "
                         "template. Add workspace: \"{params.location}\" (or similar) to "
                         "enable {RESOURCE_ROOT}.")

    if uses_resource_root and (resolve_error is not None or not resource_root):
        # Template uses {RESOURCE_ROOT} but we couldn't produce a usable
        # value — refuse to run. Silent fallback to '' produced bugs
        # (malformed URIs, late worker-side errors).
        msg = resolve_error or ("{RESOURCE_ROOT} resolved to an empty string "
                                "(server returned no error but no value either).")
        print(f"❌ {msg}", file=sys.stderr)
        print("   The template references {RESOURCE_ROOT}; refusing to run with an unresolved value.",
              file=sys.stderr)
        sys.exit(1)
    # Template doesn't use it — stay silent.

    # Build iterations
    iterate_raw = data.get('iterate')
    if yaml_format >= 2 and iterate_raw:
        # Reject filter: in format 2
        def _check_filter_usage(d):
            if isinstance(d, dict):
                if 'filter' in d:
                    print("❌ format 2: 'filter:' is not allowed — use named file groups (e.g. fastqs: \"*.f*q.gz\") "
                          "and 'where:' for metadata filters", file=sys.stderr)
                    sys.exit(1)
                for v in d.values():
                    _check_filter_usage(v)
        _check_filter_usage(iterate_raw)
    # Resolve workflow-level vars (available to all steps).
    # RESOURCE_ROOT and {NAME}_COUNT/_S are auto-injected. Two-pass:
    #   pass 1 — resolve user vars with what's known pre-iteration
    #            (RESOURCE_ROOT + params). Lets the iterator block
    #            reference workflow vars (e.g. `fastq_pair_filtering:
    #            "{PAIRED|not}"`). Vars referencing _COUNT/_S that don't
    #            exist yet leave the placeholder unresolved.
    #   pass 2 — after iterations are built and _COUNT/_S are known,
    #            re-resolve user vars from their ORIGINAL expressions so
    #            any unresolved _COUNT placeholders get filled in.
    # Built-in vars available to every YAML workflow. TODAY/NOW are seeded at
    # workflow-build time so e.g. final_output paths can include today's date
    # without the user having to thread it through. User `vars:` can shadow these.
    workflow_vars = {
        'RESOURCE_ROOT': resource_root,
        'TODAY': date.today().isoformat(),
        'NOW': datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'),
    }
    user_vars_raw = data.get('vars', {})
    for var_name, var_expr in user_vars_raw.items():
        workflow_vars[var_name] = _resolve_field(var_expr, params, extra_vars=workflow_vars)

    iterations, source_type = _build_iterations(iterate_raw, params, workflow_vars=workflow_vars)

    iterate_def = data.get('iterate')
    if iterate_def:
        if 'name' in iterate_def:
            uname = iterate_def['name'].upper()
            workflow_vars[f"{uname}_COUNT"] = str(len(iterations))
            # Also inject comma-separated list of values (e.g. SAMPLES=sample1,sample2,...)
            vals = [str(it.get(uname, '')) for it in iterations]
            workflow_vars[f"{uname}S"] = ','.join(vals)
        elif 'over' in iterate_def:
            for sub in iterate_def['over']:
                if 'name' in sub:
                    workflow_vars[f"{sub['name'].upper()}_COUNT"] = str(len(iterations))
    # Pass 2: re-resolve user vars now that _COUNT/_S are known. Idempotent
    # for vars that fully resolved in pass 1.
    for var_name, var_expr in user_vars_raw.items():
        workflow_vars[var_name] = _resolve_field(var_expr, params, extra_vars=workflow_vars)

    # Expand `requires:` declarations. A module can list other modules its
    # steps need to run alongside (typically one-off setup steps like a
    # catalog download). Any required module not already explicitly
    # imported by the template is prepended as a synthetic `- import: ...`
    # step, and the requiring step's `depends:` is extended with the
    # required step's name so ordering is enforced automatically.
    _expand_requires(data, pipeline_dir, script_root=os.environ.get('SCITQ_SCRIPT_ROOT'))

    # Classify steps
    optimize_target = data.get('optimize', {}).get('step') if data.get('optimize') else None
    steps_def = data.get('steps', [])

    # For steps that import/module into another YAML file, flags like
    # `per_sample: false` or `grouped: true` may live inside the imported
    # module, not on the step_def itself. Peek into the module so the
    # classifier honours them.
    _classify_cache: Dict[str, Optional[dict]] = {}
    def _classify_flag(step_def: dict, flag: str):
        if flag in step_def:
            return step_def[flag]
        ref = step_def.get('import') or step_def.get('module')
        if not isinstance(ref, str) or ref.endswith('.yaml') or ref.endswith('.yml'):
            return None
        if ref not in _classify_cache:
            _classify_cache[ref] = _load_module_by_ref(
                ref, pipeline_dir=pipeline_dir,
                script_root=os.environ.get('SCITQ_SCRIPT_ROOT'))
        mod = _classify_cache[ref]
        return mod.get(flag) if mod else None

    per_iter_steps = []
    oneoff_steps = []
    fanin_steps = []
    grouped_by_steps = []
    for step_def in steps_def:
        if step_def.get('grouped_by') or _classify_flag(step_def, 'grouped_by') is not None:
            # `grouped_by: <iter-name>` collapses one dimension of a
            # multi-dimensional iteration into one task per distinct value
            # of that dimension. Distinct from `grouped: true` (one task
            # collecting ALL upstream outputs) — keyed instead of global.
            grouped_by_steps.append(step_def)
        elif _classify_flag(step_def, 'grouped'):
            fanin_steps.append(step_def)
        elif _classify_flag(step_def, 'per_sample') is False:
            oneoff_steps.append(step_def)
        else:
            per_iter_steps.append(step_def)

    step_map: Dict[str, Step] = {}
    script_root = os.environ.get('SCITQ_SCRIPT_ROOT')

    # One-off steps
    for step_def in oneoff_steps:
        step = _build_step(workflow, step_def, step_map, params,
                           default_language=default_language, script_root=script_root,
                           pipeline_dir=pipeline_dir, workflow_vars=workflow_vars,
                           verbose=verbose)
        if step is not None:
            step_map[step.name] = step

    # Mark first per-iteration step (format 1 only: implicit input from iterator)
    if per_iter_steps and yaml_format < 2:
        per_iter_steps[0]['_is_first_step'] = True
    # format 2: check all steps for /input/ without inputs:
    if yaml_format >= 2:
        for sd in per_iter_steps + oneoff_steps + fanin_steps:
            if not sd.get('inputs'):
                cmd = sd.get('command', '')
                if isinstance(cmd, str) and ('/input/' in cmd or '/input ' in cmd):
                    step_label = sd.get('name', sd.get('import', sd.get('module', '?')))
                    print(f"❌ format 2: step '{step_label}' references /input/ but has no inputs:",
                          file=sys.stderr)
                    sys.exit(1)

    # Iteration loop
    if per_iter_steps and not iterations:
        print("❌ No iterations found — the iterator produced 0 samples. Check your params (bioproject, filter, etc.).", file=sys.stderr)
        sys.exit(1)
    for i, itervar in enumerate(iterations):
        for step_def in per_iter_steps:
            # Skip the optimize target step during iteration — tasks will be submitted by the optimization loop
            if optimize_target and step_def.get('name') == optimize_target:
                # Build once (first iteration) to create the step in step_map with quality/recruiter
                if step_def.get('name') not in step_map:
                    step = _build_step(workflow, step_def, step_map, params, itervar=itervar,
                                       default_language=default_language, script_root=script_root,
                                       pipeline_dir=pipeline_dir, workflow_vars=workflow_vars,
                                       verbose=verbose)
                    if step is not None:
                        step_map[step.name] = step
                continue
            step = _build_step(workflow, step_def, step_map, params, itervar=itervar,
                               default_language=default_language, script_root=script_root,
                               pipeline_dir=pipeline_dir, workflow_vars=workflow_vars,
                               verbose=verbose)
            if step is not None:
                step_map[step.name] = step

    # Fan-in steps
    if verbose:
        print(f"📊 step_map before fan-in: {sorted(step_map.keys())}", file=sys.stderr)
    for step_def in fanin_steps:
        step = _build_step(workflow, step_def, step_map, params, is_fan_in=True,
                           default_language=default_language, script_root=script_root,
                           pipeline_dir=pipeline_dir, workflow_vars=workflow_vars,
                           iterations=iterations,
                           verbose=verbose)
        if step is not None:
            step_map[step.name] = step

    # Keyed-grouping steps — one task per distinct value of the named
    # iteration variable, collecting per-task outputs of upstream steps
    # whose iteration shared that value.
    for step_def in grouped_by_steps:
        key_raw = step_def.get('grouped_by')
        if not isinstance(key_raw, str) or not key_raw:
            print(f"❌ Step '{step_def.get('name','?')}': grouped_by must be a non-empty string",
                  file=sys.stderr)
            sys.exit(1)
        # User writes lowercase to match the iterator's `name:`; iter dicts
        # store the value under the UPPERCASE name (matching the {KEY}
        # substitution convention elsewhere in YAML).
        key = key_raw.upper()
        # Group iterations by the named iter variable. Preserves first-seen
        # order so the step's tasks come out in a stable order.
        groups: Dict[str, list] = {}
        order: List[str] = []
        for iv in (iterations or []):
            if key not in iv:
                print(f"❌ Step '{step_def.get('name','?')}': grouped_by '{key_raw}' not found "
                      f"in iteration vars (available: {sorted(k for k in iv if not k.startswith('_'))})",
                      file=sys.stderr)
                sys.exit(1)
            gv = str(iv[key])
            if gv not in groups:
                groups[gv] = []
                order.append(gv)
            groups[gv].append(iv)
        if not groups:
            print(f"❌ Step '{step_def.get('name','?')}': grouped_by '{key_raw}' produced 0 groups "
                  f"(no iterations to group over)", file=sys.stderr)
            sys.exit(1)
        for gv in order:
            # Synthesise an itervar that carries only the group key so
            # `{<KEY>}` substitution + tag construction work as expected.
            # Keeps every original iteration's `_sample` accessible via
            # `_group_iterations` for input resolution.
            synthetic_itervar = {key: gv, '_group_iterations': groups[gv]}
            step = _build_step(workflow, step_def, step_map, params,
                               itervar=synthetic_itervar,
                               grouped_by_key=key,
                               default_language=default_language, script_root=script_root,
                               pipeline_dir=pipeline_dir, workflow_vars=workflow_vars,
                               iterations=iterations,
                               verbose=verbose)
            if step is not None:
                step_map[step.name] = step

    # Strip recruiters if requested — either by the CLI/API flag, or
    # declared in the YAML itself (typically wired to a param so the
    # template exposes a user-facing auto-vs-manual toggle). The CLI/API
    # flag still wins when set; the YAML field opts in without needing
    # the caller to remember it each time.
    yaml_no_recruiters = data.get('no_recruiters', False)
    if isinstance(yaml_no_recruiters, str):
        yaml_no_recruiters = str(_resolve_refs(yaml_no_recruiters, params)).lower() in ('true', '1', 'yes')
    if no_recruiters or yaml_no_recruiters:
        workflow.worker_pool = None
        for step in workflow.steps:
            step.worker_pool = None

    # Pre-flight: validate that all resources exist.
    # Resources produced by a publish: step in this workflow are exempt — they
    # may not exist yet on first run (the publishing step creates them) and
    # skip_if_exists will pick them up on subsequent runs. A resource is also
    # exempt when it is a sub-path of a published namespace (e.g. the catalog
    # publishes `.../metaphlan/{INDEX}/` and the per-sample step reads from
    # `.../metaphlan/{INDEX}/bowtie2/`).
    produced = set()
    for step in workflow.steps:
        for task in step.tasks:
            if task.publish:
                produced.add(task.publish.rstrip('/'))
    seen_resources = set()
    missing = []
    for step in workflow.steps:
        for task in step.tasks:
            for res in task.resources:
                uri = str(res).split('|')[0]  # strip |untar, |gunzip, etc.
                if uri in seen_resources:
                    continue
                seen_resources.add(uri)
                uri_clean = uri.rstrip('/')
                if any(uri_clean == p or uri_clean.startswith(p + '/') for p in produced):
                    continue
                try:
                    info = client.fetch_info(uri)
                    if not info.is_file and not info.is_dir:
                        missing.append(uri)
                except Exception:
                    missing.append(uri)
    if missing:
        print("❌ Missing resources (pre-flight check failed):", file=sys.stderr)
        for m in missing:
            print(f"   - {m}", file=sys.stderr)
        sys.exit(1)

    # Resolve opportunistic reuse settings from YAML data
    yaml_opportunistic = data.get('opportunistic', False)
    if isinstance(yaml_opportunistic, str):
        yaml_opportunistic = str(_resolve_refs(yaml_opportunistic, params)).lower() in ('true', '1', 'yes')
    yaml_untrusted_raw = data.get('untrusted', '')
    if isinstance(yaml_untrusted_raw, str):
        yaml_untrusted_raw = str(_resolve_refs(yaml_untrusted_raw, params))
        yaml_untrusted = [s.strip() for s in yaml_untrusted_raw.split(',') if s.strip()]
    else:
        yaml_untrusted = []
    effective_opportunistic = opportunistic or yaml_opportunistic
    effective_untrusted = untrusted or yaml_untrusted

    # Compile (client already created above for RESOURCE_ROOT)
    activate = standalone and not dry_run
    workflow.compile(client, activate_leading_tasks=activate,
                     opportunistic=effective_opportunistic, untrusted=effective_untrusted,
                     extend_workflow_id=extend_workflow_id, retry_failed_only=retry_failed_only)

    if dry_run:
        client.delete_workflow(workflow.workflow_id)
        print(f"✅ Dry run successful: workflow '{workflow.full_name}' created and deleted.")
        return None

    print(f"✅ Workflow '{workflow.full_name}' created (id={workflow.workflow_id})")

    # Workflow chain — top-level `chain:` block declares one or more child
    # templates to fire when this workflow reaches a terminal status. See
    # specs/workflow_chain.md. Materialising the entries here (after the
    # workflow row is created, before optimize) means a dry-run never arms
    # a chain and an extend re-run does not duplicate entries (the runner
    # creates the workflow only once per run).
    chain_def = data.get('chain')
    if chain_def:
        if not isinstance(chain_def, list):
            print("❌ chain: must be a list of entry objects", file=sys.stderr)
            sys.exit(1)
        prepared = []
        for i, entry in enumerate(chain_def):
            if not isinstance(entry, dict):
                print(f"❌ chain entry {i}: must be a mapping", file=sys.stderr)
                sys.exit(1)
            # YAML 1.1 (PyYAML default) booleanises `on`/`off`/`yes`/`no` as
            # bare keys: `on: failed` parses to `{True: 'failed'}`. Normalise
            # back so users can write `on: failed` unquoted. This is the
            # classic YAML 1.1 "Norway problem" applied to chain entries.
            if True in entry:
                entry['on'] = entry.pop(True)
            if False in entry:
                entry['off'] = entry.pop(False)
            _check_unknown_keys(entry, CHAIN_ENTRY_KEYS, f"chain entry {i}")
            tmpl = entry.get('template')
            if not tmpl or not isinstance(tmpl, str):
                print(f"❌ chain entry {i}: 'template' is required (string)", file=sys.stderr)
                sys.exit(1)
            # Resolve `template: name@version` shorthand or split fields.
            tmpl_name, tmpl_version = tmpl, entry.get('version')
            if '@' in tmpl:
                tmpl_name, tmpl_version = tmpl.split('@', 1)
            # Resolve {params.X} / {vars.X} substitutions in mapping values
            # *now*, against the parent's submitted params. Cross-workflow
            # parent.* references are resolved server-side at fire time and
            # are passed through verbatim.
            mapping_in = entry.get('params', {}) or {}
            if not isinstance(mapping_in, dict):
                print(f"❌ chain entry {i}: 'params' must be a mapping", file=sys.stderr)
                sys.exit(1)
            mapping_resolved = {}
            for k, v in mapping_in.items():
                if isinstance(v, str):
                    # Leave {parent.X} alone; only resolve {params.X} / {VARS}
                    # whose values are knowable now.
                    mapping_resolved[k] = _resolve_refs(v, params, extra_vars=workflow_vars)
                else:
                    mapping_resolved[k] = v
            prepared.append({
                'template_name': tmpl_name,
                'template_version': tmpl_version,
                'params_template': mapping_resolved,
                'when': str(entry.get('when', 'true')),
                'on': str(entry.get('on', 'succeeded')),
                'always_new': bool(entry.get('always_new', False)),
            })
        created = client.create_chain_entries(workflow.workflow_id, prepared)
        print(f"🔗 Chain armed: {len(created)} entr(ies) for workflow {workflow.workflow_id}")

    # Flush module pins into template_run.module_pins so replays can use the
    # exact same module content. Only meaningful when the runner is invoked
    # as a server subprocess (SCITQ_TEMPLATE_RUN_ID set by scriptRunner).
    _flush_module_pins(client)

    # Optimization loop (if optimize: block is present)
    optimize_def = data.get('optimize')
    if optimize_def and not dry_run:
        return _run_optimize_loop(client, workflow, optimize_def, step_map,
                                  per_iter_steps, iterations, params,
                                  default_language, workflow_vars, pipeline_dir, script_root)

    return workflow.workflow_id


def _flush_module_pins(client) -> None:
    """Persist `_resolved_module_pins` into template_run.module_pins. No-op
    when not running as a template subprocess (no template_run_id env var)
    or when nothing was resolved via the module library."""
    run_id_str = os.environ.get('SCITQ_TEMPLATE_RUN_ID')
    if not run_id_str:
        return
    if not _resolved_module_pins:
        return
    try:
        template_run_id = int(run_id_str)
    except ValueError:
        return
    try:
        from scitq2.pb import taskqueue_pb2 as pb
        client.stub.UpdateTemplateRun(pb.UpdateTemplateRunRequest(
            template_run_id=template_run_id,
            module_pins=json.dumps(_resolved_module_pins),
        ))
    except Exception as e:
        # Non-fatal: pin recording is best-effort. Log and continue.
        print(f"⚠️ module pins: failed to record {len(_resolved_module_pins)} pin(s): {e}",
              file=sys.stderr)


def _extract_task_scores(ctx, task_id: int, n_objectives: int):
    """Extract per-objective scores from a task's quality_vars JSON.
    Returns list of floats (one per objective), or None if unavailable."""
    t = ctx._get_task(task_id)
    if t is None or t.status != "S":
        return None
    qv = getattr(t, "quality_vars", None)
    if not qv:
        return None
    try:
        import json
        data = json.loads(qv)
        scores = data.get("scores")
        if scores and len(scores) >= n_objectives:
            return scores[:n_objectives]
    except (json.JSONDecodeError, TypeError, AttributeError):
        pass
    # Fallback: single quality_score as sole objective
    qs = getattr(t, "quality_score", None)
    if qs is not None and n_objectives == 1:
        return [qs]
    return None


def _run_optimize_loop(client, workflow: Workflow, optimize_def: dict,
                       step_map: Dict[str, Step], per_iter_steps: list,
                       iterations: list, params, default_language: str,
                       workflow_vars: dict, pipeline_dir, script_root) -> int:
    """Run an Optuna optimization loop driven by the YAML optimize: block."""
    try:
        import optuna
    except ImportError:
        print("❌ optuna is required for optimize: blocks. Install with: pip install scitq2[optuna]", file=sys.stderr)
        sys.exit(1)

    from scitq2.live import LiveContext

    # Parse optimize config
    directions = optimize_def.get('directions')  # multi-objective: list of "maximize"/"minimize"
    direction = optimize_def.get('direction', 'maximize')  # single-objective
    multi_objective = directions is not None
    n_trials = int(_resolve_field(optimize_def.get('n_trials', 100), params, extra_vars=workflow_vars))
    n_parallel = int(_resolve_field(optimize_def.get('n_parallel', 1), params, extra_vars=workflow_vars))
    aggregation = optimize_def.get('aggregation', 'mean')
    target_step_name = optimize_def.get('step')
    search_space = optimize_def.get('search_space', {})
    storage = optimize_def.get('storage')
    study_name = optimize_def.get('study_name', f"scitq_{workflow.name}")
    seed = optimize_def.get('seed')

    if not target_step_name:
        print("❌ optimize.step is required (which step to optimize)", file=sys.stderr)
        sys.exit(1)

    # Find the target step definition
    target_step_def = None
    for sd in per_iter_steps:
        if sd.get('name') == target_step_name:
            target_step_def = sd
            break
    if target_step_def is None:
        print(f"❌ optimize.step '{target_step_name}' not found in steps", file=sys.stderr)
        sys.exit(1)

    # Resolve storage path
    if storage:
        storage = str(_resolve_field(storage, params, extra_vars=workflow_vars))
    else:
        storage = f"sqlite:///optuna_{workflow.name}.db"

    # Create Optuna study with configurable sampler
    sampler_name = optimize_def.get('sampler', 'tpe')
    sampler_opts = optimize_def.get('sampler_options', {})
    if seed is not None:
        seed = int(_resolve_field(seed, params, extra_vars=workflow_vars))
        sampler_opts.setdefault('seed', seed)

    SAMPLERS = {
        'tpe': optuna.samplers.TPESampler,
        'cmaes': optuna.samplers.CmaEsSampler,
        'random': optuna.samplers.RandomSampler,
        'qmc': optuna.samplers.QMCSampler,
        'nsgaii': optuna.samplers.NSGAIISampler,
        'nsgaiii': getattr(optuna.samplers, 'NSGAIIISampler', None),
    }
    sampler_cls = SAMPLERS.get(sampler_name.lower())
    if sampler_cls is None:
        print(f"❌ Unknown sampler: {sampler_name}. Available: {', '.join(k for k, v in SAMPLERS.items() if v)}", file=sys.stderr)
        sys.exit(1)

    # Multi-objective defaults
    if multi_objective and sampler_name.lower() == 'tpe':
        sampler_cls = optuna.samplers.NSGAIISampler  # TPE doesn't support multi-objective

    sampler = sampler_cls(**sampler_opts)

    study_kwargs = dict(
        storage=storage,
        study_name=study_name,
        load_if_exists=True,
        sampler=sampler,
    )
    if multi_objective:
        study_kwargs['directions'] = directions
    else:
        study_kwargs['direction'] = direction
    study = optuna.create_study(**study_kwargs)

    ctx = LiveContext(client, poll_interval=2.0)

    # Get the step's quality definition from the compiled step
    target_step = step_map.get(target_step_name)
    if target_step is None or target_step.step_id is None:
        print(f"❌ Step '{target_step_name}' was not compiled (skipped by when:?)", file=sys.stderr)
        sys.exit(1)
    step_id = target_step.step_id

    # Aggregation function
    agg_funcs = {
        'mean': lambda scores: sum(scores) / len(scores),
        'median': lambda scores: sorted(scores)[len(scores) // 2],
        'min': min,
        'max': max,
    }
    agg_fn = agg_funcs.get(aggregation, agg_funcs['mean'])

    # Pruning config
    pruning_def = optimize_def.get('pruning', {})
    pruning_enabled = pruning_def.get('enabled', False)
    grace_period = pruning_def.get('grace_period', 10)

    dir_label = str(directions) if multi_objective else direction
    print(f"🔬 Starting optimization: {n_trials} trials, {n_parallel} parallel, {dir_label}", file=sys.stderr)
    print(f"   Target step: {target_step_name}, aggregation: {aggregation}", file=sys.stderr)
    if multi_objective:
        print(f"   Multi-objective: {len(directions)} objectives", file=sys.stderr)
    print(f"   Storage: {storage}", file=sys.stderr)

    # Get the command template from the step definition
    cmd_template = target_step_def.get('command', '')
    container = target_step_def.get('container', workflow.container or 'alpine')
    shell = target_step_def.get('language', default_language)

    completed_trials = 0
    trial_offset = 0

    while completed_trials < n_trials:
        batch_size = min(n_parallel, n_trials - completed_trials)
        trials = [study.ask() for _ in range(batch_size)]
        trial_tasks = {}  # trial -> [task_ids]

        for trial in trials:
            # Suggest parameters from search space
            suggested = {}
            for param_name, param_def in search_space.items():
                ptype = param_def.get('type', 'float')
                if ptype == 'float':
                    suggested[param_name] = trial.suggest_float(
                        param_name,
                        float(param_def['low']),
                        float(param_def['high']),
                        log=param_def.get('log', False),
                    )
                elif ptype == 'int':
                    suggested[param_name] = trial.suggest_int(
                        param_name,
                        int(param_def['low']),
                        int(param_def['high']),
                    )
                elif ptype == 'categorical':
                    suggested[param_name] = trial.suggest_categorical(
                        param_name,
                        param_def['choices'],
                    )

            # Submit one task per iteration (sample) with suggested params
            task_ids = []
            for itervar in (iterations or [{}]):
                # Build vars with suggested params + iteration vars + workflow vars
                trial_vars = dict(workflow_vars)
                trial_vars.update({k.upper(): str(v) for k, v in suggested.items()})
                trial_vars.update({k.upper(): str(v) for k, v in itervar.items()})
                # Also make lowercase versions available
                trial_vars.update({k: str(v) for k, v in suggested.items()})

                # Resolve command with trial vars
                resolved_cmd = _resolve_field(cmd_template, params, itervar, extra_vars=trial_vars)

                task_id = client.submit_task(
                    step_id=step_id,
                    command=str(resolved_cmd),
                    container=str(_resolve_field(container, params, itervar, extra_vars=trial_vars)),
                    shell=shell if shell != 'none' else None,
                )
                task_ids.append(task_id)

            trial_tasks[trial.number] = (trial, task_ids)
            print(f"  Trial {trial.number}: {suggested} → {len(task_ids)} task(s)", file=sys.stderr)

        # Wait for all tasks and report results
        for trial_num, (trial, task_ids) in trial_tasks.items():
            results = ctx.wait_all(task_ids)

            if multi_objective:
                # Collect per-objective score lists from quality_vars JSON
                n_obj = len(directions)
                obj_scores = [[] for _ in range(n_obj)]  # obj_scores[i] = scores across samples
                n_valid = 0
                for tid, _ in results:
                    task_scores = _extract_task_scores(ctx, tid, n_obj)
                    if task_scores:
                        n_valid += 1
                        for i, s in enumerate(task_scores):
                            obj_scores[i].append(s)

                if n_valid > 0:
                    aggregated = [agg_fn(obj_scores[i]) for i in range(n_obj)]
                    study.tell(trial, aggregated)
                    label = ", ".join(f"{v:.4f}" for v in aggregated)
                    print(f"  Trial {trial.number}: scores=[{label}] ({n_valid}/{len(task_ids)} samples)", file=sys.stderr)
                else:
                    study.tell(trial, state=optuna.trial.TrialState.FAIL)
                    print(f"  Trial {trial.number}: FAILED (no quality scores)", file=sys.stderr)
            else:
                # Single-objective: use quality_score directly
                scores = [score for _, score in results if score is not None]
                if scores:
                    trial_score = agg_fn(scores)
                    study.tell(trial, trial_score)
                    print(f"  Trial {trial.number}: score={trial_score:.4f} ({len(scores)}/{len(task_ids)} samples)", file=sys.stderr)
                else:
                    study.tell(trial, state=optuna.trial.TrialState.FAIL)
                    print(f"  Trial {trial.number}: FAILED (no quality scores)", file=sys.stderr)

            completed_trials += 1

    # Report results
    print(f"\n🏆 Optimization complete: {len(study.trials)} trials", file=sys.stderr)
    completed = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if completed:
        if multi_objective:
            print(f"   Pareto front: {len(study.best_trials)} trials", file=sys.stderr)
            for bt in study.best_trials[:5]:  # show top 5
                label = ", ".join(f"{v:.4f}" for v in bt.values)
                print(f"     {bt.params} → [{label}]", file=sys.stderr)
        else:
            print(f"   Best params: {study.best_params}", file=sys.stderr)
            print(f"   Best score: {study.best_value:.4f}", file=sys.stderr)
    else:
        print("   No trials completed successfully", file=sys.stderr)

    # Close the live workflow
    client.update_workflow_status(workflow_id=workflow.workflow_id, status="S")
    print(f"✅ Workflow '{workflow.full_name}' completed", file=sys.stderr)

    return workflow.workflow_id


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _list_bundled_modules():
    """Walk the installed scitq2_modules/yaml tree and print one JSON object
    per module to stdout. Each object carries enough for the server to seed
    the module_library table: path, version, description, content_sha256,
    content_base64. Used by `scitq module upgrade` (the server shells out
    to this to locate and read the bundled modules inside its Python venv,
    so it doesn't have to guess the site-packages path)."""
    import base64
    import hashlib
    base_dir = _find_public_module_dir()
    if not os.path.isdir(base_dir):
        return
    for root, _dirs, files in os.walk(base_dir):
        for fn in files:
            if not (fn.endswith('.yaml') or fn.endswith('.yml')):
                continue
            full = os.path.join(root, fn)
            rel = os.path.relpath(full, base_dir)
            # Strip extension for the logical path
            path = rel
            for ext in ('.yaml', '.yml'):
                if path.endswith(ext):
                    path = path[: -len(ext)]
                    break
            # Normalise path separators to forward slashes so Windows-built
            # venvs still produce canonical module paths.
            path = path.replace(os.sep, '/')
            with open(full, 'rb') as f:
                content = f.read()
            # Parse version and description
            try:
                data = yaml.safe_load(content) or {}
            except Exception:
                data = {}
            version = data.get('version')
            description = data.get('description')
            if isinstance(description, str) and ('\n' in description):
                description = description.split('\n', 1)[0]
            print(json.dumps({
                'path': path,
                'version': version,
                'description': description,
                'content_sha256': hashlib.sha256(content).hexdigest(),
                'content_base64': base64.b64encode(content).decode('ascii'),
            }))


def main():
    parser = argparse.ArgumentParser(description="Run a YAML scitq pipeline")
    parser.add_argument("input", nargs='?', help="YAML pipeline file")
    parser.add_argument("--values", type=str, help="JSON parameter values")
    parser.add_argument("--values-file", type=str, dest="values_file",
                        help="Path to a JSON file containing parameter values. "
                             "Used by the server for payloads too large for argv "
                             "(e.g. a text param holding thousands of URIs). "
                             "Mutually exclusive with --values.")
    parser.add_argument("--params", action="store_true", help="Print parameter schema as JSON")
    parser.add_argument("--dry-run", action="store_true", dest="dry_run")
    parser.add_argument("--no-recruiters", action="store_true", dest="no_recruiters", help="Create workflow without recruiters")
    parser.add_argument("--extend-workflow", type=int, default=None, dest="extend_workflow",
                        help="Extend an existing workflow (by id) instead of creating a new one. Steps are found-or-created by name; tasks found-or-referenced by (step, tag); drifted-command tasks are edit-and-retried (with cascade by default). See specs/workflow_extend.md.")
    parser.add_argument("--retry-failed-only", action="store_true", dest="retry_failed_only",
                        help="With --extend-workflow: among existing tasks only re-run those currently failed (no cascade); leave succeeded/running/pending untouched. New tags are still added.")
    parser.add_argument("--standalone", action="store_true", default=True)
    parser.add_argument("--verbose", action="store_true", help="Print step decisions to stderr")
    parser.add_argument("--list-bundled", action="store_true", dest="list_bundled",
                        help="Emit JSON lines describing bundled modules (for server-side module upgrade)")
    parser.add_argument("--offline", action="store_true",
                        help="Resolve modules from a local filesystem tree instead of the server library. "
                             "For development and dry-run only; production YAML runs always go online.")
    parser.add_argument("--yaml-module-path", dest="yaml_module_path", default=None,
                        help="In --offline mode, root of the module tree (e.g. a git checkout). "
                             "Defaults to the installed scitq2_modules/yaml/ directory.")
    args = parser.parse_args()

    if args.list_bundled:
        _list_bundled_modules()
        return

    if not args.input:
        parser.error("input YAML file is required unless --list-bundled is given")

    if args.yaml_module_path and not args.offline:
        parser.error("--yaml-module-path requires --offline")

    # Wire offline flags into the module loader before any resolution happens.
    global _offline_mode, _offline_module_path
    _offline_mode = args.offline
    _offline_module_path = args.yaml_module_path

    with open(args.input) as f:
        data = yaml.safe_load(f)

    if args.params:
        params_def = data.get('params', {})
        ParamsClass = _build_params_class(params_def)
        print(json.dumps(ParamsClass.schema(), indent=2))
        return

    if args.values and args.values_file:
        parser.error("--values and --values-file are mutually exclusive")
    if args.values_file:
        with open(args.values_file) as _vf:
            values = json.loads(_vf.read())
    else:
        values = json.loads(args.values) if args.values else {}
    standalone = args.standalone or not os.environ.get("SCITQ_TEMPLATE_RUN_ID")
    pipeline_dir = os.path.dirname(os.path.abspath(args.input))
    if args.retry_failed_only and args.extend_workflow is None:
        print("⚠️ --retry-failed-only has no effect without --extend-workflow; ignoring.", file=sys.stderr)
    run_yaml(data, params_values=values, dry_run=args.dry_run, standalone=standalone,
             pipeline_dir=pipeline_dir, no_recruiters=args.no_recruiters,
             verbose=args.verbose,
             extend_workflow_id=args.extend_workflow, retry_failed_only=args.retry_failed_only)


if __name__ == "__main__":
    main()
