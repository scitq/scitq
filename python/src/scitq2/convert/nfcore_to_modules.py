"""Bulk-convert nf-core process definitions into scitq2_modules.

Usage:
    python nfcore_to_modules.py /path/to/nf_files/ /path/to/output_modules/
"""
import os
import re
import sys
import textwrap
from pathlib import Path
from typing import Optional

# Import our Nextflow parser
from scitq2.convert.nextflow import parse, _resolve_container_ternary, _translate_script


HEADER = '''"""
{description}

Converted from nf-core/modules ({module_name}) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional
'''


def _extract_container(proc) -> str:
    """Get the Docker container image from a parsed process."""
    if proc.container:
        c = proc.container
        # Handle ternary
        if '?' in c or 'workflow.containerEngine' in c:
            c = _resolve_container_ternary(c)
        return c
    if proc.conda:
        # Best-effort: extract package name
        spec = proc.conda.strip("'\"")
        if '::' in spec:
            spec = spec.split('::', 1)[1]
        if '=' in spec:
            pkg, ver = spec.split('=', 1)
            return f"gmtscience/{pkg}:{ver}"
        return f"gmtscience/{spec}:latest"
    return "TODO"


def _extract_outputs(proc) -> dict:
    """Extract output emit names and glob patterns."""
    globs = {}
    for out in proc.outputs:
        if out.emit:
            for q in out.qualifiers:
                m = re.match(r"path\(['\"](.+?)['\"]\)", q)
                if m:
                    pattern = m.group(1)
                    # Clean up nf-core patterns like *.{bam,cram}
                    pattern = pattern.replace('{', '').replace('}', '')
                    globs[out.emit] = pattern
    return globs


def _has_meta_input(proc) -> bool:
    """Check if process takes tuple val(meta), path(reads) style input."""
    if proc.inputs:
        first = proc.inputs[0]
        return any('val(' in q and ('meta' in q or 'id' in q) for q in first.qualifiers)
    return False


def _has_extra_path_inputs(proc) -> int:
    """Count additional path-only inputs (like database, index, reference)."""
    count = 0
    for inp in proc.inputs[1:] if proc.inputs else []:
        if any('path(' in q for q in inp.qualifiers) and not any('val(' in q for q in inp.qualifiers):
            count += 1
    return count


def _extract_extra_input_names(proc) -> list:
    """Get names of extra path inputs (db, index, fasta, etc.)."""
    names = []
    for inp in proc.inputs[1:] if proc.inputs else []:
        for q in inp.qualifiers:
            m = re.match(r'path\((\w+)\)', q)
            if m and 'val(' not in str(inp.qualifiers):
                names.append(m.group(1))
    return names


def convert_module(nf_text: str, module_name: str) -> Optional[str]:
    """Convert a single nf-core module file to a scitq2_module function."""
    pipeline = parse(nf_text)
    if not pipeline.processes:
        return None

    proc = list(pipeline.processes.values())[0]
    func_name = proc.name.lower()
    container = _extract_container(proc)
    outputs = _extract_outputs(proc)
    is_per_sample = _has_meta_input(proc)
    extra_inputs = _extract_extra_input_names(proc)

    # Translate the script
    script = proc.script
    if not script:
        script = f"# TODO: no script extracted from {module_name}"

    sample_var = "sample.sample_accession" if is_per_sample else "sample"
    translated = _translate_script(script, sample_var, proc)

    # Build the TaskSpec
    task_parts = []
    if proc.cpus:
        task_parts.append(f"cpu={proc.cpus}")
    if proc.memory_gb:
        task_parts.append(f"mem={proc.memory_gb}")
    task_spec_str = f"TaskSpec({', '.join(task_parts)})" if task_parts else "None"

    # Build outputs
    if outputs:
        out_args = ", ".join(f'{k}="{v}"' for k, v in outputs.items())
        outputs_str = f"Outputs({out_args})"
    else:
        outputs_str = "None"

    # Build extra kwargs for resources
    extra_params = ""
    extra_resources = ""
    if extra_inputs:
        for name in extra_inputs:
            extra_params += f",\n          {name}_resource=None"
        resource_items = [f"{name}_resource" for name in extra_inputs]
        extra_resources = f"\n    resources = [r for r in [{', '.join(resource_items)}] if r is not None]"

    # Generate the module
    lines = []
    lines.append(HEADER.format(
        description=f"{proc.name} — {module_name}",
        module_name=module_name,
    ))

    if is_per_sample:
        lines.append(f"""
def {func_name}(workflow, sample, *, inputs=None, paired=True, worker_pool=None{extra_params},
          container="{container}"):
    \"\"\"{proc.name} step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: {', '.join(f'{k}="{v}"' for k,v in outputs.items()) if outputs else 'none defined'}
    \"\"\"{extra_resources}
    return workflow.Step(
        name="{func_name}",
        tag=sample.sample_accession,
        container=container,
        command=fr\"\"\"
{textwrap.indent(translated, '        ')}
        \"\"\",
        inputs=inputs or sample.fastqs,{f'''
        resources=resources,''' if extra_resources else ''}
        outputs={outputs_str},
        task_spec={task_spec_str},
        worker_pool=worker_pool,
    )
""")
    else:
        lines.append(f"""
def {func_name}(workflow, *, inputs=None, worker_pool=None{extra_params},
          container="{container}"):
    \"\"\"{proc.name} step (non-per-sample).

    Args:
        workflow: the Workflow object
        inputs: input files
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: {', '.join(f'{k}="{v}"' for k,v in outputs.items()) if outputs else 'none defined'}
    \"\"\"{extra_resources}
    return workflow.Step(
        name="{func_name}",
        container=container,
        command=fr\"\"\"
{textwrap.indent(translated, '        ')}
        \"\"\",
        inputs=inputs,{f'''
        resources=resources,''' if extra_resources else ''}
        outputs={outputs_str},
        task_spec={task_spec_str},
        worker_pool=worker_pool,
    )
""")

    return "".join(lines)


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <input_dir> <output_dir>")
        sys.exit(1)

    input_dir = Path(sys.argv[1])
    output_dir = Path(sys.argv[2])
    output_dir.mkdir(parents=True, exist_ok=True)

    successes = 0
    failures = 0

    for nf_file in sorted(input_dir.glob("*.nf")):
        module_name = nf_file.stem
        try:
            text = nf_file.read_text()
            result = convert_module(text, module_name)
            if result:
                out_file = output_dir / f"{module_name}.py"
                out_file.write_text(result)
                # Verify syntax
                import ast
                ast.parse(result)
                print(f"  ✅ {module_name}")
                successes += 1
            else:
                print(f"  ⚠️ {module_name} (no process found)")
                failures += 1
        except Exception as e:
            print(f"  ❌ {module_name}: {e}")
            failures += 1

    print(f"\nDone: {successes} converted, {failures} failed")


if __name__ == "__main__":
    main()
