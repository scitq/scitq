"""Snakemake to scitq Python DSL converter."""
import re
import sys
import textwrap
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Set, Tuple


# ---------------------------------------------------------------------------
# Intermediate representation
# ---------------------------------------------------------------------------

@dataclass
class SmkRule:
    name: str
    inputs: Dict[str, str] = field(default_factory=dict)    # name -> pattern (or positional list)
    input_raw: List[str] = field(default_factory=list)       # raw positional inputs
    outputs: Dict[str, str] = field(default_factory=dict)
    output_raw: List[str] = field(default_factory=list)
    shell: str = ""
    threads: Optional[int] = None
    mem_mb: Optional[int] = None
    conda: Optional[str] = None
    container: Optional[str] = None
    params: Dict[str, str] = field(default_factory=dict)
    log: Optional[str] = None
    wildcards: Set[str] = field(default_factory=set)         # {sample}, {n}, etc.

    @property
    def is_per_sample(self) -> bool:
        """True if this rule uses wildcards (not an aggregation rule)."""
        return len(self.wildcards) > 0

    @property
    def all_output_patterns(self) -> List[str]:
        return list(self.outputs.values()) + self.output_raw

    @property
    def all_input_patterns(self) -> List[str]:
        return list(self.inputs.values()) + self.input_raw


@dataclass
class SmkPipeline:
    config_file: Optional[str] = None
    config_refs: Set[str] = field(default_factory=set)       # config["key"] references
    glob_wildcards_expr: Optional[str] = None                # glob_wildcards(...) expression
    glob_wildcards_var: Optional[str] = None                 # e.g. "SAMPLES"
    rules: Dict[str, SmkRule] = field(default_factory=dict)
    rule_all_inputs: List[str] = field(default_factory=list)
    expand_exprs: List[str] = field(default_factory=list)    # raw expand() calls in rule all


# ---------------------------------------------------------------------------
# Parser
# ---------------------------------------------------------------------------

_WILDCARD_RE = re.compile(r'\{(\w+)\}')


def _extract_wildcards(pattern: str) -> Set[str]:
    """Extract {wildcard} names from a pattern, excluding known Snakemake builtins."""
    wcs = set(_WILDCARD_RE.findall(pattern))
    wcs -= {'input', 'output', 'threads', 'params', 'wildcards', 'resources', 'log'}
    return wcs


def _strip_snakemake_wrappers(val: str) -> str:
    """Strip Snakemake wrappers like directory(), temp(), protected(), ancient()."""
    for wrapper in ('directory', 'temp', 'protected', 'ancient', 'touch', 'pipe'):
        m = re.match(rf'{wrapper}\((.+)\)', val)
        if m:
            return m.group(1).strip().strip('"\'')
    return val


def _parse_key_value_block(lines: List[str]) -> Tuple[Dict[str, str], List[str]]:
    """Parse a block of key=value or positional entries.
    Returns (named_dict, positional_list)."""
    named = {}
    positional = []
    joined = " ".join(l.strip().rstrip(",") for l in lines).strip()
    if not joined:
        return named, positional
    # Split by commas, respecting parentheses
    parts = _split_commas(joined)
    for part in parts:
        part = part.strip()
        m = re.match(r'(\w+)\s*=\s*(.*)', part)
        if m:
            val = _strip_snakemake_wrappers(m.group(2).strip().strip('"\''))
            named[m.group(1)] = val
        else:
            val = _strip_snakemake_wrappers(part.strip('"\''))
            positional.append(val)
    return named, positional


def _split_commas(s: str) -> List[str]:
    """Split by commas, respecting parentheses and brackets."""
    parts = []
    depth = 0
    current = []
    for ch in s:
        if ch in ('(', '[', '{'):
            depth += 1
        elif ch in (')', ']', '}'):
            depth -= 1
        elif ch == ',' and depth == 0:
            parts.append(''.join(current))
            current = []
            continue
        current.append(ch)
    if current:
        parts.append(''.join(current))
    return parts


def _parse_rule_block(name: str, body_lines: List[str]) -> SmkRule:
    """Parse the indented body of a rule."""
    rule = SmkRule(name=name)

    # Split into directives
    current_directive = None
    directive_lines: Dict[str, List[str]] = {}

    for line in body_lines:
        stripped = line.strip()
        if not stripped or stripped.startswith('#'):
            continue
        # Check if this is a new directive
        m = re.match(r'(\w+)\s*:\s*(.*)', stripped)
        if m and m.group(1) in ('input', 'output', 'shell', 'run', 'script', 'threads',
                                 'resources', 'conda', 'container', 'singularity',
                                 'params', 'log', 'benchmark', 'wrapper', 'message'):
            current_directive = m.group(1)
            remainder = m.group(2).strip()
            directive_lines[current_directive] = [remainder] if remainder else []
        elif current_directive is not None:
            directive_lines[current_directive].append(stripped)

    # Parse directives
    if 'input' in directive_lines:
        rule.inputs, rule.input_raw = _parse_key_value_block(directive_lines['input'])
    if 'output' in directive_lines:
        rule.outputs, rule.output_raw = _parse_key_value_block(directive_lines['output'])

    # Shell
    if 'shell' in directive_lines:
        shell_text = "\n".join(directive_lines['shell'])
        # Strip triple and single quotes
        shell_text = re.sub(r'^\s*"""', '', shell_text)
        shell_text = re.sub(r'"""\s*$', '', shell_text)
        shell_text = re.sub(r"^\s*'''", '', shell_text)
        shell_text = re.sub(r"'''\s*$", '', shell_text)
        # Also strip single-line quoting: "command"
        shell_text = shell_text.strip()
        if (shell_text.startswith('"') and shell_text.endswith('"')) or \
           (shell_text.startswith("'") and shell_text.endswith("'")):
            shell_text = shell_text[1:-1]
        rule.shell = textwrap.dedent(shell_text).strip()

    # Threads
    if 'threads' in directive_lines:
        try:
            rule.threads = int(directive_lines['threads'][0])
        except (ValueError, IndexError):
            pass

    # Resources
    if 'resources' in directive_lines:
        res_named, _ = _parse_key_value_block(directive_lines['resources'])
        if 'mem_mb' in res_named:
            try:
                rule.mem_mb = int(res_named['mem_mb'])
            except ValueError:
                pass

    # Conda / container
    if 'conda' in directive_lines:
        rule.conda = directive_lines['conda'][0].strip().strip('"\'')
    if 'container' in directive_lines:
        rule.container = directive_lines['container'][0].strip().strip('"\'')
    if 'singularity' in directive_lines:
        rule.container = directive_lines['singularity'][0].strip().strip('"\'')

    # Params
    if 'params' in directive_lines:
        rule.params, _ = _parse_key_value_block(directive_lines['params'])

    # Log
    if 'log' in directive_lines:
        rule.log = directive_lines['log'][0].strip().strip('"\'')

    # Extract wildcards from input/output patterns
    for pattern in rule.all_input_patterns + rule.all_output_patterns:
        rule.wildcards.update(_extract_wildcards(pattern))

    return rule


def parse(text: str) -> SmkPipeline:
    """Parse a Snakefile into SmkPipeline."""
    pipeline = SmkPipeline()

    # configfile
    m = re.search(r'configfile:\s*["\'](.+?)["\']', text)
    if m:
        pipeline.config_file = m.group(1)

    # config["key"] references
    pipeline.config_refs = set(re.findall(r'config\["(\w+)"\]', text))

    # glob_wildcards
    m = re.search(r'(\w+)\s*=\s*glob_wildcards\(["\'](.+?)["\']\)\.\w+', text)
    if m:
        pipeline.glob_wildcards_var = m.group(1)
        pipeline.glob_wildcards_expr = m.group(2)

    # Parse rules
    lines = text.splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        m = re.match(r'^rule\s+(\w+)\s*:', line)
        if m:
            rule_name = m.group(1)
            # Collect indented body
            body_lines = []
            i += 1
            while i < len(lines) and (lines[i].startswith(' ') or lines[i].startswith('\t') or lines[i].strip() == ''):
                body_lines.append(lines[i])
                i += 1

            if rule_name == 'all':
                # Parse rule all specially
                joined = " ".join(l.strip().rstrip(",") for l in body_lines if l.strip() and not l.strip().startswith('#'))
                pipeline.expand_exprs = re.findall(r'expand\(([^)]+)\)', joined)
                # Also capture raw input patterns
                input_m = re.search(r'input:\s*(.*)', joined)
                if input_m:
                    pipeline.rule_all_inputs = _split_commas(input_m.group(1))
            else:
                pipeline.rules[rule_name] = _parse_rule_block(rule_name, body_lines)
            continue
        i += 1

    return pipeline


# ---------------------------------------------------------------------------
# DAG resolution
# ---------------------------------------------------------------------------

def _pattern_to_regex(pattern: str) -> str:
    """Convert a Snakemake output pattern to a regex for matching inputs."""
    escaped = re.escape(pattern)
    return escaped.replace(r'\{', '(?P<').replace(r'\}', '>\\w+)')


def _match_input_to_rule(input_pattern: str, rules: Dict[str, SmkRule]) -> Optional[str]:
    """Find which rule produces files matching this input pattern."""
    for rule_name, rule in rules.items():
        for out_pattern in rule.all_output_patterns:
            # Direct match (patterns with same wildcards)
            out_base = re.sub(r'\{(\w+)\}', '{*}', out_pattern)
            in_base = re.sub(r'\{(\w+)\}', '{*}', input_pattern)
            if out_base == in_base:
                return rule_name
            # Check if input is a prefix/suffix match of output
            out_fixed = out_pattern.replace('{', '').replace('}', '')
            in_fixed = input_pattern.replace('{', '').replace('}', '')
            if out_fixed == in_fixed:
                return rule_name
    return None


def resolve_dag(pipeline: SmkPipeline) -> List[Tuple[str, List[str]]]:
    """Resolve the DAG: returns topologically sorted (rule_name, [dependency_rule_names])."""
    deps: Dict[str, Set[str]] = {name: set() for name in pipeline.rules}

    for rule_name, rule in pipeline.rules.items():
        for pattern in rule.all_input_patterns:
            # Skip config references and expand() calls
            if 'config[' in pattern or pattern.startswith('expand('):
                continue
            source = _match_input_to_rule(pattern, pipeline.rules)
            if source and source != rule_name:
                deps[rule_name].add(source)

    # Topological sort (Kahn's algorithm)
    in_degree = {name: len(d) for name, d in deps.items()}
    queue = [name for name, deg in in_degree.items() if deg == 0]
    result = []
    while queue:
        node = queue.pop(0)
        result.append((node, sorted(deps[node])))
        for name, d in deps.items():
            if node in d:
                in_degree[name] -= 1
                if in_degree[name] == 0:
                    queue.append(name)

    return result


# ---------------------------------------------------------------------------
# Code generator
# ---------------------------------------------------------------------------

def _conda_to_container(conda_spec: str, registry: str) -> Tuple[str, Optional[str]]:
    """Convert conda env file or spec to a container name + mkdocker Dockerfile."""
    spec = conda_spec.strip().strip("'\"")
    if spec.endswith('.yml') or spec.endswith('.yaml'):
        # Can't parse the yaml here, just use the filename as hint
        name = spec.rsplit('/', 1)[-1].rsplit('.', 1)[0]
        container = f"{registry}/{name}:latest"
        dockerfile = f"FROM {registry}/mamba\n# TODO: parse {spec} and add _conda install commands\n#registry {registry}\n"
        return container, dockerfile
    # Direct spec like "bioconda::salmon=1.10.3"
    if '::' in spec:
        spec = spec.split('::', 1)[1]
    if '=' in spec:
        pkg, version = spec.split('=', 1)
    else:
        pkg, version = spec, "latest"
    container = f"{registry}/{pkg}:{version}"
    dockerfile = f"FROM {registry}/mamba\nRUN _conda install {spec}\n#tag {version}\n#registry {registry}\n"
    return container, dockerfile


def _translate_shell(shell: str, sample_var: str = "sample.sample_accession",
                     rule_wildcards: Optional[Set[str]] = None) -> str:
    """Translate Snakemake shell variables to scitq fr-string conventions."""
    s = shell
    reserved = {'input', 'output', 'threads', 'params', 'resources', 'log', 'wildcards'}
    wildcards = rule_wildcards or set()

    # Step 1: Replace known Snakemake variables with placeholders to avoid double-replacement
    # {threads} -> ${{THREADS}}
    s = re.sub(r'\{threads\}', '__THREADS__', s)
    # {resources.mem_mb} -> ${{MEM}}
    s = re.sub(r'\{resources\.\w+\}', '__MEM__', s)
    # {input.name} -> /input/name
    s = re.sub(r'\{input\.(\w+)\}', r'__INPUT_\1__', s)
    # {input} -> /input/*
    s = re.sub(r'\{input\}', '__INPUT__', s)
    # {input[0]}, {input[1]}
    s = re.sub(r'\{input\[0\]\}', '__INPUT0__', s)
    s = re.sub(r'\{input\[1\]\}', '__INPUT1__', s)
    # {output.name} -> /output/name
    s = re.sub(r'\{output\.(\w+)\}', r'__OUTPUT_\1__', s)
    # {output} -> /output/
    s = re.sub(r'\{output\}', '__OUTPUT__', s)
    # {wildcards.name} -> sample var
    s = re.sub(r'\{wildcards\.(\w+)\}', '__SAMPLE__', s)
    # {params.name} -> keep as f-string
    s = re.sub(r'\{params\.(\w+)\}', r'__PARAM_\1__', s)

    # Step 2: Replace remaining {wildcard} patterns (actual wildcards like {sample})
    def replace_remaining(m):
        name = m.group(1)
        if name in reserved:
            return m.group(0)
        if name in wildcards:
            return '__SAMPLE__'
        return m.group(0)
    s = re.sub(r'\{(\w+)\}', replace_remaining, s)

    # Step 3: Expand placeholders to final fr-string values
    s = s.replace('__THREADS__', '${{THREADS}}')
    s = s.replace('__MEM__', '${{MEM}}')
    s = s.replace('__INPUT__', '/input/*')
    s = s.replace('__INPUT0__', '/input/*_R1.*')
    s = s.replace('__INPUT1__', '/input/*_R2.*')
    s = re.sub(r'__INPUT_(\w+)__', r'/input/\1', s)
    s = s.replace('__OUTPUT__', '/output/')
    s = re.sub(r'__OUTPUT_(\w+)__', r'/output/\1', s)
    s = s.replace('__SAMPLE__', f'{{{sample_var}}}')
    s = re.sub(r'__PARAM_(\w+)__', r'{params.\1}', s)

    return s


def _output_globs(rule: SmkRule) -> Dict[str, str]:
    """Generate output glob patterns from rule outputs."""
    globs = {}
    for name, pattern in rule.outputs.items():
        # Convert pattern to glob: "trimmed/{sample}_fastp.json" -> "*_fastp.json"
        glob = re.sub(r'\{[^}]+\}', '*', pattern)
        glob = glob.rsplit('/', 1)[-1]  # just the filename part
        if glob:
            globs[name] = glob
    for i, pattern in enumerate(rule.output_raw):
        glob = re.sub(r'\{[^}]+\}', '*', pattern)
        glob = glob.rsplit('/', 1)[-1]
        if glob:
            key = f"out{i}" if len(rule.output_raw) > 1 else "output"
            globs[key] = glob
    return globs


def generate(pipeline: SmkPipeline, config: Optional[Dict] = None) -> str:
    """Generate scitq Python DSL from parsed Snakemake pipeline."""
    config = config or {}
    registry = config.get('registry', 'gmtscience')
    lines = []
    dockerfiles: Dict[str, str] = {}

    # Resolve containers
    resolved_containers: Dict[str, str] = {}
    for rname, rule in pipeline.rules.items():
        if rule.container:
            resolved_containers[rname] = rule.container
        elif rule.conda:
            container, dockerfile = _conda_to_container(rule.conda, registry)
            resolved_containers[rname] = container
            if dockerfile:
                dockerfiles[rname] = dockerfile

    # Resolve DAG
    dag = resolve_dag(pipeline)

    # Header
    lines.append("from scitq2 import *")
    lines.append("")

    # Params
    lines.append("class Params(metaclass=ParamSpec):")
    if pipeline.glob_wildcards_expr:
        lines.append(f'    input_dir = Param.string(required=True, help="Input data directory")')
    for key in sorted(pipeline.config_refs):
        lines.append(f'    {key} = Param.string(required=True, help="{key}")')
    lines.append('    location = Param.provider_region(required=True, help="Provider and region")')
    lines.append("")

    # Workflow function
    lines.append("def converted_workflow(params: Params):")
    lines.append("")

    # Workflow object
    containers = list(resolved_containers.values())
    default_container = containers[0] if containers and len(set(containers)) == 1 else None

    max_threads = max((r.threads or 1 for r in pipeline.rules.values()), default=4)
    max_mem = max(((r.mem_mb or 8000) / 1000 for r in pipeline.rules.values()), default=8)

    lines.append("    workflow = Workflow(")
    lines.append(f'        name="converted-pipeline",')
    lines.append(f'        description="Converted from Snakemake",')
    lines.append(f'        version="1.0.0",')
    lines.append(f'        language=Shell("bash"),')
    lines.append(f'        skip_if_exists=True,  # Snakemake DAG semantics: only run if output missing')
    if default_container:
        lines.append(f'        container="{default_container}",')
    lines.append(f'        worker_pool=WorkerPool(')
    lines.append(f'            W.cpu >= {max_threads},')
    lines.append(f'            W.mem >= {int(max_mem)},')
    lines.append(f'            max_recruited=10,')
    lines.append(f'        ),')
    lines.append(f'        provider=params.location.provider,')
    lines.append(f'        region=params.location.region,')
    lines.append("    )")
    lines.append("")

    # Sample discovery
    lines.append("    # TODO: adjust sample discovery to your data source")
    if pipeline.glob_wildcards_expr:
        lines.append(f"    # Original: {pipeline.glob_wildcards_var} = glob_wildcards(\"{pipeline.glob_wildcards_expr}\").*")
    lines.append('    samples = URI.find(params.input_dir, group_by="folder", filter="*.f*q.gz",')
    lines.append('        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})')
    lines.append("")

    # Classify rules: one-off (no wildcards) vs per-sample
    oneoff_rules = [name for name, rule in pipeline.rules.items() if not rule.is_per_sample]
    per_sample_rules = [name for name, rule in pipeline.rules.items() if rule.is_per_sample]

    # Emit one-off steps first (before the loop)
    for rule_name, dep_names in dag:
        if rule_name not in oneoff_rules:
            continue
        rule = pipeline.rules[rule_name]
        _emit_rule_step(lines, "    ", rule, dep_names, resolved_containers,
                        default_container, is_first=True, is_fan_in=False, pipeline=pipeline)

    # Per-sample loop
    lines.append("    for sample in samples:")
    lines.append("")

    first_in_loop = True
    for rule_name, dep_names in dag:
        if rule_name not in per_sample_rules:
            continue
        rule = pipeline.rules[rule_name]
        # Check if this is a fan-in rule (input uses expand() collecting from all samples)
        is_fan_in = any('expand(' in inp for inp in rule.input_raw)
        if is_fan_in:
            continue  # handle after the loop
        _emit_rule_step(lines, "        ", rule, dep_names, resolved_containers,
                        default_container, is_first=first_in_loop, is_fan_in=False, pipeline=pipeline)
        first_in_loop = False

    # Fan-in steps (after the loop) — rules whose input uses expand() or collects all samples
    for rule_name in pipeline.rules:
        rule = pipeline.rules[rule_name]
        is_fan_in = any('expand(' in inp for inp in rule.input_raw)
        if not is_fan_in:
            continue
        # Find which rule's output feeds into this rule
        dep_names = []
        for rn, deps in dag:
            if rn == rule_name:
                dep_names = deps
                break
        lines.append("")
        _emit_rule_step(lines, "    ", rule, dep_names, resolved_containers,
                        default_container, is_first=False, is_fan_in=True, pipeline=pipeline)

    # Runner
    lines.append("run(converted_workflow)")
    lines.append("")

    # Print mkdocker instructions
    if dockerfiles:
        print("\n📦 mkdocker Dockerfiles to build:", file=sys.stderr)
        for name, content in dockerfiles.items():
            print(f"\n--- dockers/{name} ---", file=sys.stderr)
            print(content, file=sys.stderr)

    return "\n".join(lines)


def _emit_rule_step(lines: List[str], indent: str, rule: SmkRule,
                    dep_names: List[str], resolved_containers: Dict,
                    default_container: Optional[str], is_first: bool,
                    is_fan_in: bool, pipeline: SmkPipeline) -> None:
    """Emit a workflow.Step() call for a Snakemake rule."""
    container = resolved_containers.get(rule.name)

    lines.append(f'{indent}{rule.name} = workflow.Step(')
    lines.append(f'{indent}    name="{rule.name}",')

    if rule.is_per_sample and not is_fan_in:
        lines.append(f'{indent}    tag=sample.sample_accession,')

    # Container
    if container and container != default_container:
        lines.append(f'{indent}    container="{container}",')
    elif not default_container and container:
        lines.append(f'{indent}    container="{container}",')

    # Command
    if rule.shell:
        translated = _translate_shell(rule.shell, rule_wildcards=rule.wildcards)
        lines.append(f'{indent}    command=fr"""')
        for script_line in translated.splitlines():
            lines.append(f'{indent}    {script_line}')
        lines.append(f'{indent}    """,')

    # Inputs
    if is_first and rule.is_per_sample:
        lines.append(f'{indent}    inputs=sample.fastqs,')
    elif is_fan_in and dep_names:
        dep = dep_names[0]
        globs = _output_globs(pipeline.rules.get(dep, SmkRule(name=dep)))
        first_glob_name = next(iter(globs), None)
        if first_glob_name:
            lines.append(f'{indent}    inputs={dep}.output("{first_glob_name}", grouped=True),')
        else:
            lines.append(f'{indent}    inputs={dep}.output(grouped=True),')
    elif dep_names:
        if len(dep_names) == 1:
            dep = dep_names[0]
            globs = _output_globs(pipeline.rules.get(dep, SmkRule(name=dep)))
            first_glob_name = next(iter(globs), None)
            if first_glob_name:
                lines.append(f'{indent}    inputs={dep}.output("{first_glob_name}"),')
            else:
                lines.append(f'{indent}    inputs={dep}.output(),')
        else:
            refs = []
            for dep in dep_names:
                globs = _output_globs(pipeline.rules.get(dep, SmkRule(name=dep)))
                first_glob_name = next(iter(globs), None)
                if first_glob_name:
                    refs.append(f'{dep}.output("{first_glob_name}")')
                else:
                    refs.append(f'{dep}.output()')
            lines.append(f'{indent}    inputs=[{", ".join(refs)}],')

    # Outputs
    globs = _output_globs(rule)
    if globs:
        glob_args = ", ".join(f'{k}="{v}"' for k, v in globs.items())
        lines.append(f'{indent}    outputs=Outputs({glob_args}),')

    # TaskSpec
    parts = []
    if rule.threads:
        parts.append(f"cpu={rule.threads}")
    if rule.mem_mb:
        parts.append(f"mem={rule.mem_mb / 1000}")
    if parts:
        lines.append(f'{indent}    task_spec=TaskSpec({", ".join(parts)}),')

    lines.append(f'{indent})')
    lines.append("")


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------

def convert_file(input_path: str, output_path: Optional[str] = None, config: Optional[Dict] = None) -> str:
    """Convert a Snakefile to scitq Python DSL."""
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
    parser = argparse.ArgumentParser(description="Convert Snakemake to scitq Python DSL")
    parser.add_argument("input", help="Input Snakefile")
    parser.add_argument("-o", "--output", help="Output .py file (default: stdout)")
    parser.add_argument("--registry", default="gmtscience", help="Docker registry (default: gmtscience)")
    args = parser.parse_args()
    convert_file(args.input, args.output, config={"registry": args.registry})


if __name__ == "__main__":
    main()
