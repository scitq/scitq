"""Nextflow DSL2 to scitq Python DSL converter."""
import re
import sys
import textwrap
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple


# ---------------------------------------------------------------------------
# Intermediate representation
# ---------------------------------------------------------------------------

@dataclass
class NfInput:
    """One input declaration inside a process."""
    qualifiers: List[str]   # e.g. ["val(sample_id)", "path(reads)"]
    raw: str                # full line

@dataclass
class NfOutput:
    """One output declaration inside a process."""
    qualifiers: List[str]
    emit: Optional[str] = None
    raw: str = ""

@dataclass
class NfProcess:
    name: str
    container: Optional[str] = None
    conda: Optional[str] = None
    cpus: Optional[int] = None
    memory_gb: Optional[float] = None
    inputs: List[NfInput] = field(default_factory=list)
    outputs: List[NfOutput] = field(default_factory=list)
    script: str = ""
    publish_dir: Optional[str] = None
    label: Optional[str] = None
    when: Optional[str] = None

    @property
    def is_per_sample(self) -> bool:
        """True if the first input is a tuple with val(id)."""
        if not self.inputs:
            return False
        first = self.inputs[0]
        return any(q.startswith('val(') for q in first.qualifiers) and len(first.qualifiers) > 1

@dataclass
class NfWorkflowCall:
    """One process invocation inside the workflow{} block."""
    process: str
    args: str           # raw argument string
    is_collect: bool = False   # .collect() on input

@dataclass
class NfWorkflow:
    """Parsed workflow{} block."""
    channel_defs: List[str] = field(default_factory=list)  # raw channel creation lines
    calls: List[NfWorkflowCall] = field(default_factory=list)

@dataclass
class NfPipeline:
    params: Dict[str, str] = field(default_factory=dict)
    processes: Dict[str, NfProcess] = field(default_factory=dict)
    workflow: Optional[NfWorkflow] = None


# ---------------------------------------------------------------------------
# Parser
# ---------------------------------------------------------------------------

def _strip_comments(text: str) -> str:
    """Remove // line comments (but not inside strings)."""
    lines = []
    for line in text.splitlines():
        # naive: strip from first // that's not inside quotes
        in_single = in_double = False
        for i, ch in enumerate(line):
            if ch == "'" and not in_double:
                in_single = not in_single
            elif ch == '"' and not in_single:
                in_double = not in_double
            elif ch == '/' and i + 1 < len(line) and line[i + 1] == '/' and not in_single and not in_double:
                line = line[:i]
                break
        lines.append(line)
    return "\n".join(lines)


def _find_block(text: str, keyword: str, start: int = 0) -> Optional[Tuple[int, int, str]]:
    """Find a brace-delimited block starting with keyword.
    Returns (block_start, block_end, block_body) or None.
    """
    pat = re.compile(rf'\b{keyword}\b\s*\{{', re.MULTILINE)
    m = pat.search(text, start)
    if not m:
        return None
    brace_start = m.end() - 1
    depth = 0
    for i in range(brace_start, len(text)):
        if text[i] == '{':
            depth += 1
        elif text[i] == '}':
            depth -= 1
            if depth == 0:
                body = text[brace_start + 1:i]
                return (m.start(), i + 1, body)
    return None


def _find_named_block(text: str, keyword: str, start: int = 0) -> Optional[Tuple[str, int, int, str]]:
    """Find  keyword NAME { ... }  — returns (name, start, end, body)."""
    pat = re.compile(rf'\b{keyword}\s+(\w+)\s*\{{', re.MULTILINE)
    m = pat.search(text, start)
    if not m:
        return None
    name = m.group(1)
    brace_start = m.end() - 1
    depth = 0
    for i in range(brace_start, len(text)):
        if text[i] == '{':
            depth += 1
        elif text[i] == '}':
            depth -= 1
            if depth == 0:
                body = text[brace_start + 1:i]
                return (name, m.start(), i + 1, body)
    return None


def _resolve_container_ternary(val: str) -> str:
    """Extract the Docker image from a Nextflow container ternary expression.
    e.g. "${ workflow.containerEngine == 'singularity' ? 'singularity_url' : 'docker_url' }"
    """
    # Try to extract the Docker branch (after the ':' in the ternary)
    m = re.search(r":\s*'([^']+)'", val)
    if m:
        return m.group(1)
    m = re.search(r':\s*"([^"]+)"', val)
    if m:
        return m.group(1)
    # Fallback: strip Groovy interpolation markers
    val = val.strip("${ }")
    return val


def _parse_directive(line: str) -> Optional[Tuple[str, str]]:
    """Parse a process directive like  cpus 4  or  container 'img'."""
    line = line.strip()
    for directive in ('container', 'conda', 'cpus', 'memory', 'publishDir', 'when', 'label'):
        if line.startswith(directive):
            val = line[len(directive):].strip()
            # strip quotes
            val = val.strip("'\"")
            # Handle container ternary expressions
            if directive == 'container' and ('?' in val or 'workflow.containerEngine' in val):
                val = _resolve_container_ternary(val)
            return (directive, val)
    return None


def _parse_memory(val: str) -> Optional[float]:
    """Parse '8 GB' or '500 MB' into GB."""
    m = re.match(r'([\d.]+)\s*(GB|MB|TB)', val, re.IGNORECASE)
    if not m:
        return None
    num = float(m.group(1))
    unit = m.group(2).upper()
    if unit == 'MB':
        return num / 1024
    if unit == 'TB':
        return num * 1024
    return num


def _parse_outputs(block_body: str) -> List[NfOutput]:
    """Parse the output: block lines."""
    outputs = []
    for line in block_body.strip().splitlines():
        line = line.strip()
        if not line:
            continue
        emit = None
        emit_match = re.search(r',\s*emit:\s*(\w+)', line)
        if emit_match:
            emit = emit_match.group(1)
            line_no_emit = line[:emit_match.start()].strip()
        else:
            line_no_emit = line
        # extract qualifiers
        quals = re.findall(r'(val\([^)]+\)|path\([^)]+\)|file\([^)]+\))', line_no_emit)
        outputs.append(NfOutput(qualifiers=quals, emit=emit, raw=line))
    return outputs


def _parse_inputs(block_body: str) -> List[NfInput]:
    """Parse the input: block lines."""
    inputs = []
    for line in block_body.strip().splitlines():
        line = line.strip()
        if not line:
            continue
        quals = re.findall(r'(val\([^)]+\)|path\([^)]+\)|file\([^)]+\))', line)
        inputs.append(NfInput(qualifiers=quals, raw=line))
    return inputs


def _extract_script(body: str) -> str:
    """Extract the script: triple-quoted block.
    Handles Groovy if/else with multiple triple-quoted blocks by taking the last one
    (usually the paired-end / main branch). Stops before stub: section.
    """
    # Find the script: section, stopping at stub: if present
    script_section = re.search(r'script:\s*\n(.*?)(?=\n\s*stub:|$)', body, re.DOTALL)
    if script_section:
        script_body = script_section.group(1)
        # Find all """...""" blocks within the script section only
        blocks = re.findall(r'"""(.*?)"""', script_body, re.DOTALL)
        if blocks:
            # Take the last block (usually the main/paired-end branch)
            return textwrap.dedent(blocks[-1]).strip()

    m = re.search(r'script:\s*\n\s*"""(.*?)"""', body, re.DOTALL)
    if m:
        return textwrap.dedent(m.group(1)).strip()
    # try shell:
    m = re.search(r'shell:\s*\n\s*"""(.*?)"""', body, re.DOTALL)
    if m:
        return textwrap.dedent(m.group(1)).strip()
    # single-line script
    m = re.search(r'script:\s*\n\s*["\'](.+?)["\']', body)
    if m:
        return m.group(1).strip()
    return ""


def _parse_process(name: str, body: str) -> NfProcess:
    """Parse a process body into NfProcess."""
    proc = NfProcess(name=name)

    # Extract sub-blocks
    input_block = _find_block(body, 'input:' if 'input:' in body else 'input')
    output_block = _find_block(body, 'output:' if 'output:' in body else 'output')

    # Parse input/output using line-scanning within their section
    sections = {}
    current_section = "directives"
    section_lines: Dict[str, List[str]] = {"directives": []}
    for line in body.splitlines():
        stripped = line.strip()
        if stripped in ('input:', 'output:', 'script:', 'shell:', 'exec:', 'when:'):
            current_section = stripped.rstrip(':')
            section_lines[current_section] = []
            continue
        if current_section not in section_lines:
            section_lines[current_section] = []
        section_lines[current_section].append(line)

    # Directives — join multi-line values (e.g. container ternary spanning 3 lines)
    raw_directives = section_lines.get("directives", [])
    joined_directives = []
    for line in raw_directives:
        stripped = line.strip()
        if not stripped:
            continue
        if joined_directives and (joined_directives[-1].count('"') % 2 == 1 or
                                   joined_directives[-1].count("'") % 2 == 1 or
                                   joined_directives[-1].rstrip().endswith(('?', ':',  '\\'))):
            joined_directives[-1] += " " + stripped
        else:
            joined_directives.append(stripped)

    for line in joined_directives:
        d = _parse_directive(line)
        if d:
            key, val = d
            if key == 'container':
                proc.container = val
            elif key == 'cpus':
                try:
                    proc.cpus = int(val)
                except ValueError:
                    pass
            elif key == 'memory':
                proc.memory_gb = _parse_memory(val)
            elif key == 'conda':
                proc.conda = val
            elif key == 'publishDir':
                proc.publish_dir = val
            elif key == 'label':
                proc.label = val
                # Infer resources from nf-core labels
                if 'high' in val and proc.cpus is None:
                    proc.cpus = 12
                elif 'medium' in val and proc.cpus is None:
                    proc.cpus = 6

    # Input
    if "input" in section_lines:
        proc.inputs = _parse_inputs("\n".join(section_lines["input"]))

    # Output
    if "output" in section_lines:
        proc.outputs = _parse_outputs("\n".join(section_lines["output"]))

    # Script
    proc.script = _extract_script(body)

    return proc


def _parse_workflow_block(body: str) -> NfWorkflow:
    """Parse the workflow{} block."""
    wf = NfWorkflow()
    for line in body.strip().splitlines():
        line = line.strip()
        if not line:
            continue
        # Channel definitions: reads = Channel.fromFilePairs(...)
        if '=' in line and 'Channel.' in line:
            wf.channel_defs.append(line)
            continue
        # Process calls: PROCESS_NAME(args)
        m = re.match(r'(\w+)\((.+)\)', line)
        if m:
            proc_name = m.group(1)
            args = m.group(2).strip()
            is_collect = '.collect()' in args
            args_clean = args.replace('.collect()', '')
            wf.calls.append(NfWorkflowCall(process=proc_name, args=args_clean, is_collect=is_collect))
    return wf


def parse(text: str) -> NfPipeline:
    """Parse a Nextflow DSL2 file into NfPipeline."""
    text = _strip_comments(text)
    pipeline = NfPipeline()

    # Parse params
    for m in re.finditer(r'params\.(\w+)\s*=\s*["\']?([^"\'\n]+)["\']?', text):
        pipeline.params[m.group(1)] = m.group(2).strip()

    # Parse processes
    pos = 0
    while True:
        result = _find_named_block(text, 'process', pos)
        if result is None:
            break
        name, start, end, body = result
        pipeline.processes[name] = _parse_process(name, body)
        pos = end

    # Parse workflow block
    wf_result = _find_block(text, 'workflow')
    if wf_result:
        _, _, wf_body = wf_result
        pipeline.workflow = _parse_workflow_block(wf_body)

    return pipeline


# ---------------------------------------------------------------------------
# Code generator
# ---------------------------------------------------------------------------

def _conda_to_container(conda_spec: str, registry: str = "gmtscience") -> Tuple[str, Optional[str]]:
    """Convert a conda spec like 'bioconda::salmon=1.10.3' to a container name and mkdocker Dockerfile content.
    Returns (container_name, dockerfile_content)."""
    # Parse "bioconda::package=version" or "package=version"
    spec = conda_spec.strip().strip("'\"")
    # Handle environment.yml references
    if spec.endswith('.yml') or spec.endswith('.yaml'):
        return (f"{registry}/TODO-conda-env", None)
    # Remove channel prefix
    if '::' in spec:
        spec = spec.split('::', 1)[1]
    # Split name=version
    if '=' in spec:
        pkg, version = spec.split('=', 1)
    else:
        pkg, version = spec, "latest"
    container = f"{registry}/{pkg}:{version}"
    dockerfile = f"FROM {registry}/mamba\nRUN _conda install {spec}\n#tag {version}\n#registry {registry}\n"
    return (container, dockerfile)


def _translate_script(script: str, sample_var: str = "sample", proc: Optional[NfProcess] = None) -> str:
    """Convert Nextflow script variables to scitq fr-string conventions."""
    s = script
    # Collect input path variable names for substitution
    input_path_vars = set()
    input_val_vars = set()
    if proc:
        for inp in proc.inputs:
            for q in inp.qualifiers:
                m = re.match(r'path\((\w+)\)', q)
                if m:
                    input_path_vars.add(m.group(1))
                m = re.match(r'val\((\w+)\)', q)
                if m:
                    input_val_vars.add(m.group(1))

    # $task.cpus / ${task.cpus} -> ${CPU}
    s = re.sub(r'\$\{?task\.cpus\}?', '${{CPU}}', s)
    s = re.sub(r'\$\{?task\.memory[^}]*\}?', '${{MEM}}', s)
    # ${params.xxx} -> {params.xxx}  (python f-string)
    s = re.sub(r'\$\{params\.(\w+)\}', r'{params.\1}', s)

    # val() variables -> python f-string interpolation
    for var in input_val_vars:
        # ${id} or ${meta.id} -> {sample_var}
        s = re.sub(rf'\$\{{?{var}\}}?', f'{{{sample_var}}}', s)
    s = re.sub(r'\$\{meta\.id\}', f'{{{sample_var}}}', s)

    # path() variables: ${reads[0]}, ${reads[1]} -> /input/* patterns
    s = re.sub(r'\$\{?\w+\[0\]\}?', '/input/*_1.f*q.gz', s)
    s = re.sub(r'\$\{?\w+\[1\]\}?', '/input/*_2.f*q.gz', s)
    # Single path variables like ${transcriptome}, ${index}, ${fastq_1} -> /input/ or /resource/
    for var in input_path_vars:
        s = re.sub(rf'\$\{{?{var}\}}?', f'/input/{var}', s)

    # Remaining ${prefix} (common nf-core pattern) -> sample var
    s = re.sub(r'\$\{prefix\}', f'{{{sample_var}}}', s)

    # Double-escape shell variables that survived: $VAR -> ${{VAR}} in fr-strings
    # Only for simple $VAR patterns (uppercase or known shell vars)
    def escape_shell_var(m):
        var = m.group(1)
        if var in ('CPU', 'MEM', 'THREADS') or var.startswith('{'):
            return m.group(0)  # already handled
        return f'${{{{{var}}}}}'
    s = re.sub(r'\$([A-Z_][A-Z0-9_]*)', escape_shell_var, s)

    return s


def _output_globs(proc: NfProcess) -> Dict[str, str]:
    """Extract named output globs from process outputs."""
    globs = {}
    for out in proc.outputs:
        if out.emit:
            # Extract path pattern from qualifiers
            for q in out.qualifiers:
                m = re.match(r'path\(["\'](.+?)["\']\)', q)
                if m:
                    globs[out.emit] = m.group(1)
    return globs


def _build_task_spec(proc: NfProcess) -> Optional[str]:
    """Build TaskSpec string from process directives."""
    parts = []
    if proc.cpus:
        parts.append(f"cpu={proc.cpus}")
    if proc.memory_gb:
        parts.append(f"mem={proc.memory_gb}")
    if not parts:
        return None
    return f"TaskSpec({', '.join(parts)})"


def _resolve_single_arg(arg: str, is_collect: bool = False) -> str:
    """Resolve a single argument to an input expression."""
    arg = arg.strip()
    # Handle .collect() or .mix().collect() etc
    collect = is_collect or '.collect()' in arg
    arg_clean = re.sub(r'\.(collect|mix|flatten)\([^)]*\)', '', arg).strip()

    grouped = ', grouped=True' if collect else ''

    # PROCESS.out.name -> step.output("name")
    m = re.match(r'(\w+)\.out\.(\w+)', arg_clean)
    if m:
        proc_name = m.group(1).lower()
        emit_name = m.group(2)
        return f'{proc_name}.output("{emit_name}"{grouped})'
    # PROCESS.out -> step.output()
    m = re.match(r'(\w+)\.out', arg_clean)
    if m:
        proc_name = m.group(1).lower()
        return f'{proc_name}.output({grouped})'
    # params.xxx -> resource
    if arg_clean.startswith('params.'):
        param_name = arg_clean[7:]
        return f'params.{param_name}  # TODO: may need Resource() wrapper'
    # channel variable
    return f'sample.fastqs  # TODO: resolve channel "{arg_clean}"'


def _resolve_call_inputs(call: NfWorkflowCall, processes: Dict[str, NfProcess]) -> List[str]:
    """Resolve all arguments of a process call into input expressions."""
    args_str = call.args.strip()
    # Split by comma, but respect parentheses
    args = []
    depth = 0
    current = []
    for ch in args_str:
        if ch in ('(', '['):
            depth += 1
        elif ch in (')', ']'):
            depth -= 1
        elif ch == ',' and depth == 0:
            args.append(''.join(current).strip())
            current = []
            continue
        current.append(ch)
    if current:
        args.append(''.join(current).strip())

    return [_resolve_single_arg(a, call.is_collect) for a in args if a]


def generate(pipeline: NfPipeline, config: Optional[Dict] = None) -> str:
    """Generate scitq Python DSL from parsed pipeline."""
    config = config or {}
    lines = []

    # Header
    lines.append("from scitq2 import *")
    if any(c.is_collect for c in (pipeline.workflow.calls if pipeline.workflow else [])):
        pass  # grouped is handled inline
    lines.append("")

    # Params
    if pipeline.params:
        lines.append("class Params(metaclass=ParamSpec):")
        for name, default in pipeline.params.items():
            if default.lower() in ('true', 'false'):
                lines.append(f'    {name} = Param.boolean(default={default.capitalize()}, help="{name}")')
            elif default.isdigit():
                lines.append(f'    {name} = Param.integer(default={default}, help="{name}")')
            else:
                lines.append(f'    {name} = Param.string(default="{default}", help="{name}")')
        lines.append('    location = Param.provider_region(required=True, help="Provider and region")')
        lines.append("")

    # Workflow function
    func_name = "converted_workflow"
    if pipeline.params:
        lines.append(f"def {func_name}(params: Params):")
    else:
        lines.append(f"def {func_name}():")
    lines.append("")

    # Resolve containers: use explicit container, or generate from conda
    registry = config.get('registry', 'gmtscience')
    dockerfiles: Dict[str, str] = {}  # name -> dockerfile content
    resolved_containers: Dict[str, str] = {}  # process name -> container image
    for pname, proc in pipeline.processes.items():
        if proc.container:
            resolved_containers[pname] = proc.container
        elif proc.conda:
            container, dockerfile = _conda_to_container(proc.conda, registry)
            resolved_containers[pname] = container
            if dockerfile:
                dockerfiles[pname.lower()] = dockerfile

    # Workflow object
    containers = list(resolved_containers.values())
    default_container = containers[0] if containers and len(set(containers)) == 1 else None

    lines.append("    workflow = Workflow(")
    lines.append(f'        name="converted-pipeline",')
    lines.append(f'        description="Converted from Nextflow",')
    lines.append(f'        version="1.0.0",')
    lines.append(f'        language=Shell("bash"),')
    if default_container:
        lines.append(f'        container="{default_container}",')
    lines.append(f'        worker_pool=WorkerPool(')
    max_cpu = max((p.cpus or 1 for p in pipeline.processes.values()), default=4)
    max_mem = max((p.memory_gb or 8 for p in pipeline.processes.values()), default=16)
    lines.append(f'            W.cpu >= {max_cpu},')
    lines.append(f'            W.mem >= {int(max_mem)},')
    lines.append(f'            max_recruited=10,')
    lines.append(f'        ),')
    if pipeline.params:
        lines.append(f'        provider=params.location.provider,')
        lines.append(f'        region=params.location.region,')
    lines.append("    )")
    lines.append("")

    # Classify processes: per-sample vs one-off
    per_sample_procs = set()
    oneoff_procs = set()
    for pname, proc in pipeline.processes.items():
        if proc.is_per_sample:
            per_sample_procs.add(pname)
        else:
            oneoff_procs.add(pname)

    # Sample discovery
    lines.append("    # TODO: adjust sample discovery to your data source")
    if pipeline.workflow and pipeline.workflow.channel_defs:
        for ch_def in pipeline.workflow.channel_defs:
            lines.append(f"    # Original: {ch_def}")
    lines.append('    samples = URI.find(params.input_dir, group_by="folder", filter="*.f*q.gz",')
    lines.append('        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})')
    lines.append("")

    # Generate one-off steps (before the loop)
    if pipeline.workflow:
        for call in pipeline.workflow.calls:
            if call.process not in oneoff_procs or call.is_collect:
                continue
            proc = pipeline.processes.get(call.process)
            if not proc:
                continue
            var_name = call.process.lower()
            _emit_step(lines, "    ", var_name, proc, call,
                       resolved_containers, default_container, is_first=True, is_fan_in=False, config=config)

    # Per-sample loop
    lines.append("    for sample in samples:")
    lines.append("")

    # Generate per-sample steps
    if pipeline.workflow:
        first_in_loop = True
        for call in pipeline.workflow.calls:
            if call.process not in per_sample_procs:
                continue
            proc = pipeline.processes.get(call.process)
            if not proc:
                lines.append(f"        # TODO: process {call.process} not found")
                continue
            var_name = call.process.lower()
            _emit_step(lines, "        ", var_name, proc, call,
                       resolved_containers, default_container, is_first=first_in_loop, is_fan_in=False, config=config)
            first_in_loop = False

    # Generate fan-in steps (after the loop)
    if pipeline.workflow:
        for call in pipeline.workflow.calls:
            if not call.is_collect:
                continue
            proc = pipeline.processes.get(call.process)
            if not proc:
                continue
            var_name = call.process.lower()
            lines.append("")
            _emit_step(lines, "    ", var_name, proc, call,
                       resolved_containers, default_container, is_first=False, is_fan_in=True, config=config)

    # Runner
    lines.append(f"run({func_name})")
    lines.append("")

    # Print mkdocker instructions if any
    if dockerfiles:
        print("\n📦 mkdocker Dockerfiles to build:", file=sys.stderr)
        for name, content in dockerfiles.items():
            print(f"\n--- dockers/{name} ---", file=sys.stderr)
            print(content, file=sys.stderr)

    return "\n".join(lines)


def _emit_step(lines: List[str], indent: str, var_name: str, proc: NfProcess,
               call: NfWorkflowCall, resolved_containers: Dict, default_container: Optional[str],
               is_first: bool, is_fan_in: bool, config: Dict) -> None:
    """Emit a single workflow.Step() call."""
    container = resolved_containers.get(proc.name)

    lines.append(f'{indent}{var_name} = workflow.Step(')
    lines.append(f'{indent}    name="{var_name}",')

    if not is_fan_in and proc.is_per_sample:
        lines.append(f'{indent}    tag=sample.sample_accession,')

    # Container (only if different from workflow default)
    if container and container != default_container:
        lines.append(f'{indent}    container="{container}",')
    elif not default_container and container:
        lines.append(f'{indent}    container="{container}",')

    # Command
    sample_var = "sample.sample_accession" if proc.is_per_sample else "sample"
    translated = _translate_script(proc.script, sample_var, proc)
    lines.append(f'{indent}    command=fr"""')
    for script_line in translated.splitlines():
        lines.append(f'{indent}    {script_line}')
    lines.append(f'{indent}    """,')

    # Inputs
    if is_first and proc.is_per_sample:
        lines.append(f'{indent}    inputs=sample.fastqs,')
    else:
        input_refs = _resolve_call_inputs(call, {})
        if len(input_refs) == 1:
            ref = input_refs[0]
            if '#' in ref:
                comment = ref.split('#', 1)[1].strip()
                ref_clean = ref.split('#', 1)[0].strip()
                lines.append(f'{indent}    inputs={ref_clean},  # {comment}')
            else:
                lines.append(f'{indent}    inputs={ref},')
        else:
            # Extract TODO comments to place after the list
            todos = [ref.split('#')[1].strip() for ref in input_refs if '#' in ref]
            clean_refs = [ref.split('#')[0].strip().rstrip(',') for ref in input_refs]
            lines.append(f'{indent}    inputs=[{", ".join(clean_refs)}],{"  # " + "; ".join(todos) if todos else ""}')

    # Outputs
    globs = _output_globs(proc)
    if globs:
        glob_args = ", ".join(f'{k}="{v}"' for k, v in globs.items())
        if proc.publish_dir:
            lines.append(f'{indent}    outputs=Outputs({glob_args}, publish=True),  # was publishDir "{proc.publish_dir}"')
        else:
            lines.append(f'{indent}    outputs=Outputs({glob_args}),')
    elif proc.publish_dir:
        lines.append(f'{indent}    outputs=Outputs(publish=True),  # was publishDir "{proc.publish_dir}"')

    # TaskSpec
    task_spec = _build_task_spec(proc)
    if task_spec:
        lines.append(f'{indent}    task_spec={task_spec},')

    lines.append(f'{indent})')
    lines.append("")


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------

def convert_file(input_path: str, output_path: Optional[str] = None, config: Optional[Dict] = None) -> str:
    """Convert a Nextflow .nf file to scitq Python DSL."""
    with open(input_path, "r") as f:
        text = f.read()

    pipeline = parse(text)
    code = generate(pipeline, config)

    if output_path:
        with open(output_path, "w") as f:
            f.write(code)
        print(f"✅ Converted {input_path} → {output_path}", file=sys.stderr)
    else:
        print(code)

    return code


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Convert Nextflow DSL2 to scitq Python DSL")
    parser.add_argument("input", help="Input .nf file")
    parser.add_argument("-o", "--output", help="Output .py file (default: stdout)")
    args = parser.parse_args()
    convert_file(args.input, args.output)


if __name__ == "__main__":
    main()
