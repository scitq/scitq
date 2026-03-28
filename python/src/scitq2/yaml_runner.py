"""YAML pipeline runner for scitq.

Reads a declarative YAML pipeline definition and compiles it into
a scitq Workflow using the scitq2_modules system.

Usage:
    python -m scitq2.yaml_runner pipeline.yaml --values '{"key": "val"}'
    python -m scitq2.yaml_runner pipeline.yaml --params
    python -m scitq2.yaml_runner pipeline.yaml --values '...' --dry-run
"""
import argparse
import hashlib
import importlib
import json
import os
import re
import sys
from itertools import product as itertools_product
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
    elif filter_name == 'lower':
        return value.lower()
    elif filter_name == 'upper':
        return value.upper()
    elif filter_name == 'int':
        return str(int(float(value)))
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


def _resolve_refs(val, params, itervar=None, extra_vars=None):
    """Resolve {params.x}, {ITER_VAR}, and {var|filter} references in a string."""
    if not isinstance(val, str):
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
                    return default
                return ''
            resolved = str(val)
        elif itervar and ref in itervar:
            resolved = str(itervar[ref])
        elif extra_vars and ref in extra_vars:
            resolved = str(extra_vars[ref])
        else:
            if default is not None:
                return default
            return m.group(0)

        # Apply filters
        for f in filters:
            resolved = _apply_filter(resolved, f.strip())
        return resolved
    # Only match {NAME} not preceded by $ (shell variables ${VAR} are left for the shell)
    return re.sub(r'(?<!\$)\{([^}]+)\}', repl, val)


def _resolve_cond(val, params, itervar=None, step_fields=None, extra_vars=None):
    """Resolve a cond: block to its selected value.
    val is a dict with 'cond' key (the condition reference) and value keys.
    step_fields: additional fields from the step definition (e.g. paired: true).
    """
    if not isinstance(val, dict) or 'cond' not in val:
        return val
    cond_ref = val['cond']
    # First try resolving as param ref (handles {params.x} syntax)
    resolved = _resolve_refs(cond_ref, params, itervar, extra_vars)
    # If still unresolved (bare name), check extra_vars then step-level fields
    if resolved == cond_ref:
        if extra_vars and cond_ref in extra_vars:
            resolved = str(extra_vars[cond_ref])
        elif step_fields and cond_ref in step_fields:
            resolved = _resolve_refs(str(step_fields[cond_ref]), params, itervar, extra_vars)
    # Normalize to find the matching key
    # YAML parses true/false as Python booleans, so we need to handle both
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
    raise ValueError(f"cond: no match for '{resolved}' in {list(k for k in val if k != 'cond')}")


def _resolve_field(val, params, itervar=None, step_fields=None, extra_vars=None):
    """Resolve a field value: handles cond: blocks, param references, filters, and arithmetic."""
    if isinstance(val, dict) and 'cond' in val:
        val = _resolve_cond(val, params, itervar, step_fields, extra_vars)
    if isinstance(val, str):
        val = _resolve_refs(val, params, itervar, extra_vars)
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
        if typ == 'enum':
            kwargs['choices'] = spec.get('choices', [])
            namespace[name] = Param.enum(**kwargs)
        elif typ == 'path':
            namespace[name] = Param.path(**kwargs)
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

def _build_iterations(iterate_def, params) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    """Build the list of iteration dicts from the iterate: block.
    Returns (iterations, primary_source_type).
    Each iteration dict maps variable names to values.
    For URI/ENA/SRA sources, each dict also has a '_sample' key with the sample object.
    """
    if iterate_def is None:
        return [{}], None

    # Conditional iterator: cond selects which sub-block to use
    if 'cond' in iterate_def:
        resolved = _resolve_cond(iterate_def, params)
        if isinstance(resolved, dict):
            # The selected branch is the iterator definition
            # Inherit 'name' from parent if not in branch
            if 'name' not in resolved and 'name' in iterate_def:
                resolved['name'] = iterate_def['name']
            return _build_iterations(resolved, params)

    # Product mode
    if 'mode' in iterate_def and iterate_def['mode'] == 'product':
        sub_iters = [_build_single_iterator(sub, params) for sub in iterate_def['over']]
        names = [sub['name'] for sub in iterate_def['over']]
        result = []
        for combo in itertools_product(*[s[0] for s in sub_iters]):
            merged = {}
            for name, items in zip(names, combo):
                merged.update(items)
            result.append(merged)
        return result, None

    # Single iterator
    items, source_type = _build_single_iterator(iterate_def, params)
    return items, source_type


def _build_single_iterator(iter_def: dict, params) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    """Build iterations for a single iterator definition."""
    name = iter_def['name']
    source = iter_def.get('source', 'list')
    uname = name.upper()

    if source in ('uri', 'ena', 'sra'):
        samples = _discover_samples(iter_def, params)
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
        filepath = _resolve_refs(iter_def['file'], params)
        with open(filepath) as f:
            lines = [l.strip() for l in f if l.strip()]
        return [{uname: line} for line in lines], None

    else:
        raise ValueError(f"Unknown iterator source: {source}")


def _discover_samples(iter_def: dict, params) -> list:
    """Discover samples from URI/ENA/SRA."""
    source = iter_def.get('source', 'uri')

    if source == 'uri':
        from scitq2.uri import URI
        uri = _resolve_refs(iter_def.get('uri', ''), params)
        group_by = iter_def.get('group_by', 'folder')
        filter_glob = iter_def.get('filter')
        return URI.find(uri, group_by=group_by, filter=filter_glob,
                        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})
    elif source == 'ena':
        from scitq2.biology import ENA, SampleFilter, S
        identifier = _resolve_refs(iter_def.get('identifier', ''), params)
        group_by = iter_def.get('group_by', 'sample_accession')
        filter_def = iter_def.get('filter', {})
        sf = None
        if filter_def and isinstance(filter_def, dict):
            conditions = [getattr(S, k) == v for k, v in filter_def.items()]
            sf = SampleFilter(*conditions) if conditions else None
        return ENA(identifier=identifier, group_by=group_by, filter=sf)
    elif source == 'sra':
        from scitq2.biology import SRA
        identifier = _resolve_refs(iter_def.get('identifier', ''), params)
        group_by = iter_def.get('group_by', 'sample_accession')
        return SRA(identifier=identifier, group_by=group_by)
    raise ValueError(f"Unknown sample source: {source}")


# ---------------------------------------------------------------------------
# Worker pool / language
# ---------------------------------------------------------------------------

def _build_worker_pool(wp_def: dict, params, extra_vars: Optional[Dict] = None) -> WorkerPool:
    """Build a WorkerPool from YAML definition."""
    filters = []

    # Provider/region from explicit field
    provider_ref = wp_def.get('provider')
    if provider_ref:
        resolved = _resolve_refs(provider_ref, params)
        if isinstance(resolved, ProviderRegion):
            filters.append(W.provider.like(f"{resolved.provider}%"))
            filters.append(W.region == resolved.region)

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


def _load_public_import(import_name: str) -> dict:
    """Load a public module by name. Searches scitq2_modules/yaml/ with auto-extension.
    e.g. 'genetic/fastp' -> scitq2_modules/yaml/genetic/fastp.yaml
    """
    base_dir = _find_public_module_dir()
    # Try with .yaml extension, then without (in case already has extension)
    for ext in ('.yaml', '.yml', ''):
        path = os.path.join(base_dir, import_name + ext)
        if os.path.exists(path):
            with open(path) as f:
                return yaml.safe_load(f)
    raise FileNotFoundError(f"Public module not found: {import_name} (searched in {base_dir})")


def _load_private_module(module_path: str, pipeline_dir: Optional[str] = None,
                         script_root: Optional[str] = None) -> dict:
    """Load a private module by path. Searches: relative to pipeline, server modules, env path.
    Auto-detects .yaml extension.
    """
    candidates = []
    for path_base in [module_path]:
        if pipeline_dir:
            candidates.append(os.path.join(pipeline_dir, path_base))
            candidates.append(os.path.join(pipeline_dir, 'modules', path_base))
        candidates.append(path_base)
        if script_root:
            candidates.append(os.path.join(script_root, 'modules', path_base))
        module_path_env = os.environ.get('SCITQ_YAML_MODULE_PATH')
        if module_path_env:
            candidates.append(os.path.join(module_path_env, path_base))

    for path in candidates:
        if os.path.exists(path):
            with open(path) as f:
                return yaml.safe_load(f)

    raise FileNotFoundError(f"Private module not found: {module_path} (searched: {candidates})")


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

def _resolve_inputs(input_ref: str, step_map: Dict[str, Step], grouped: bool = False):
    """Resolve 'step_name.output_name' to an Output object."""
    if not input_ref:
        return None
    parts = input_ref.split('.')
    if len(parts) == 2:
        step_name, output_name = parts
        if step_name in step_map:
            return step_map[step_name].output(output_name, grouped=grouped)
    elif len(parts) == 1 and parts[0] in step_map:
        return step_map[parts[0]].output(grouped=grouped)
    raise ValueError(f"Cannot resolve input: {input_ref}")


def _merge_module_step(module_data: dict, step_def: dict, exclude_key: str) -> dict:
    """Merge module data with step definition. Step overrides module,
    except 'vars' which are merged (step vars override individual module vars)."""
    merged = dict(module_data)
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
                pipeline_dir: Optional[str] = None, workflow_vars: Optional[Dict] = None) -> Step:
    """Build a single step from a YAML definition."""

    # when: conditional — skip step if falsy
    when = step_def.get('when')
    if when is not None:
        resolved = _resolve_field(when, params, itervar, extra_vars=workflow_vars)
        if not resolved or resolved in ('false', 'False', 'No', 'no', 'none', 'None', ''):
            return None

    # Resolve imports: public (import:) or private (module:) YAML modules
    # Supports nesting: a private module can import: a public module
    if 'import' in step_def:
        module_data = _load_public_import(step_def['import'])
        merged = _merge_module_step(module_data, step_def, exclude_key='import')
        step_def = merged

    if 'module' in step_def:
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
                kwargs['inputs'] = _resolve_inputs(input_ref, step_map, grouped=is_fan_in)
            if 'worker_pool' in step_def and isinstance(step_def['worker_pool'], dict):
                kwargs['worker_pool'] = _build_worker_pool(step_def['worker_pool'], params)
            sample = itervar.get('_sample') if itervar else None
            if sample is not None:
                return func(workflow, sample, **kwargs)
            else:
                return func(workflow, **kwargs)

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
    command = _resolve_field(step_def.get('command', ''), params, itervar, step_fields=step_def, extra_vars=extra_vars)
    container = adhoc_image or _resolve_field(step_def.get('container'), params, itervar, step_fields=step_def, extra_vars=extra_vars)
    language_str = step_def.get('language', default_language)

    # Fail on unresolved YAML variables in command (not shell ${VAR})
    if command:
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
    if itervar and not is_fan_in:
        # Build tag from all iterator variables (excluding internal keys)
        tag_parts = [str(v) for k, v in itervar.items() if not k.startswith('_')]
        step_kwargs['tag'] = '.'.join(tag_parts) if tag_parts else None

    # Inputs — resolve cond: if present, then handle string or list
    input_ref = step_def.get('inputs')
    if input_ref:
        # Resolve cond: on inputs
        input_ref = _resolve_field(input_ref, params, itervar, step_fields=step_def, extra_vars=extra_vars)
        if isinstance(input_ref, list):
            # Multiple input references — resolve each and combine with +
            resolved = [_resolve_inputs(ref.strip(), step_map, grouped=is_fan_in) for ref in input_ref]
            combined = resolved[0]
            for r in resolved[1:]:
                combined = combined + r
            step_kwargs['inputs'] = combined
        elif isinstance(input_ref, str):
            step_kwargs['inputs'] = _resolve_inputs(input_ref, step_map, grouped=is_fan_in)
    elif sample is not None and step_def.get('_is_first_step'):
        step_kwargs['inputs'] = sample.fastqs

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
            out_kwargs['publish'] = True if publish is True else _resolve_field(publish, params, itervar)
        step_kwargs['outputs'] = Outputs(**out_kwargs)

    # TaskSpec
    ts_def = step_def.get('task_spec', {})
    if ts_def:
        step_kwargs['task_spec'] = TaskSpec(**ts_def)

    # Worker pool override
    if 'worker_pool' in step_def and isinstance(step_def['worker_pool'], dict):
        step_kwargs['worker_pool'] = _build_worker_pool(step_def['worker_pool'], params, extra_vars=extra_vars)

    # skip_if_exists
    if 'skip_if_exists' in step_def:
        step_kwargs['skip_if_exists'] = step_def['skip_if_exists']

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
             no_recruiters: bool = False) -> Optional[int]:
    """Run a YAML pipeline definition."""
    # Validate
    if 'name' not in data:
        print("❌ Missing required field: name", file=sys.stderr)
        sys.exit(1)
    if 'steps' not in data or not data['steps']:
        print("❌ Missing or empty: steps", file=sys.stderr)
        sys.exit(1)

    # Build params
    params_def = data.get('params', {})
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

    workflow = Workflow(**wf_kwargs)

    # Resolve RESOURCE_ROOT from server if provider/region are set
    from scitq2.grpc_client import Scitq2Client
    client = Scitq2Client()
    resource_root = ''
    if 'provider' in wf_kwargs and 'region' in wf_kwargs:
        try:
            resource_root = client.get_resource_root(
                provider=wf_kwargs['provider'], region=wf_kwargs['region'])
        except Exception:
            pass  # no resource root configured — RESOURCE_ROOT will be empty

    # Build iterations
    iterations, source_type = _build_iterations(data.get('iterate'), params)

    # Resolve workflow-level vars (available to all steps)
    # RESOURCE_ROOT and {NAME}_COUNT are auto-injected
    workflow_vars = {'RESOURCE_ROOT': resource_root}
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
    for var_name, var_expr in data.get('vars', {}).items():
        workflow_vars[var_name] = _resolve_field(var_expr, params, extra_vars=workflow_vars)

    # Classify steps
    steps_def = data.get('steps', [])
    per_iter_steps = []
    oneoff_steps = []
    fanin_steps = []
    for step_def in steps_def:
        if step_def.get('grouped'):
            fanin_steps.append(step_def)
        elif step_def.get('per_sample') is False:
            oneoff_steps.append(step_def)
        else:
            per_iter_steps.append(step_def)

    step_map: Dict[str, Step] = {}
    script_root = os.environ.get('SCITQ_SCRIPT_ROOT')

    # One-off steps
    for step_def in oneoff_steps:
        step = _build_step(workflow, step_def, step_map, params,
                           default_language=default_language, script_root=script_root,
                           pipeline_dir=pipeline_dir, workflow_vars=workflow_vars)
        if step is not None:
            step_map[step.name] = step

    # Mark first per-iteration step
    if per_iter_steps:
        per_iter_steps[0]['_is_first_step'] = True

    # Iteration loop
    for i, itervar in enumerate(iterations):
        for step_def in per_iter_steps:
            step = _build_step(workflow, step_def, step_map, params, itervar=itervar,
                               default_language=default_language, script_root=script_root,
                               pipeline_dir=pipeline_dir, workflow_vars=workflow_vars)
            if step is not None:
                step_map[step.name] = step
            elif i == 0:
                n = step_def.get('name', step_def.get('module', step_def.get('import', '?')))
                print(f"SKIPPED on first iter: {n}", file=sys.stderr)

    # Fan-in steps
    for step_def in fanin_steps:
        step = _build_step(workflow, step_def, step_map, params, is_fan_in=True,
                           default_language=default_language, script_root=script_root,
                           pipeline_dir=pipeline_dir, workflow_vars=workflow_vars)
        if step is not None:
            step_map[step.name] = step

    # Strip recruiters if requested
    if no_recruiters:
        for step in workflow.steps:
            step.worker_pool = None

    # Compile (client already created above for RESOURCE_ROOT)
    activate = standalone and not dry_run
    workflow.compile(client, activate_leading_tasks=activate)

    if dry_run:
        client.delete_workflow(workflow.workflow_id)
        print(f"✅ Dry run successful: workflow '{workflow.full_name}' created and deleted.")
        return None

    print(f"✅ Workflow '{workflow.full_name}' created (id={workflow.workflow_id})")
    return workflow.workflow_id


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Run a YAML scitq pipeline")
    parser.add_argument("input", help="YAML pipeline file")
    parser.add_argument("--values", type=str, help="JSON parameter values")
    parser.add_argument("--params", action="store_true", help="Print parameter schema as JSON")
    parser.add_argument("--dry-run", action="store_true", dest="dry_run")
    parser.add_argument("--no-recruiters", action="store_true", dest="no_recruiters", help="Create workflow without recruiters")
    parser.add_argument("--standalone", action="store_true", default=True)
    args = parser.parse_args()

    with open(args.input) as f:
        data = yaml.safe_load(f)

    if args.params:
        params_def = data.get('params', {})
        ParamsClass = _build_params_class(params_def)
        print(json.dumps(ParamsClass.schema(), indent=2))
        return

    values = json.loads(args.values) if args.values else {}
    standalone = args.standalone or not os.environ.get("SCITQ_TEMPLATE_RUN_ID")
    pipeline_dir = os.path.dirname(os.path.abspath(args.input))
    run_yaml(data, params_values=values, dry_run=args.dry_run, standalone=standalone,
             pipeline_dir=pipeline_dir, no_recruiters=args.no_recruiters)


if __name__ == "__main__":
    main()
