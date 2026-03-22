# Snakemake to scitq Converter — Specification

## Goal

Build a tool that converts Snakemake workflow files (`Snakefile`, `.smk`) into scitq Python DSL templates. Snakemake conversion is harder than Nextflow due to its implicit file-based DAG construction, but the common bioinformatics patterns are tractable.

## Fundamental difference

Snakemake uses a **pull model** inspired by GNU Make: rules declare input/output file patterns with wildcards, and the engine works *backwards* from the desired target (`rule all`) to infer what must run. The DAG is emergent from file pattern matching.

scitq uses a **push model**: you explicitly loop over samples, create tasks, and wire dependencies. The DAG is in the code.

The converter must **resolve** Snakemake's implicit DAG into explicit scitq loops and dependencies.

## Concept mapping

| Snakemake | scitq DSL | Difficulty | Notes |
|---|---|---|---|
| `rule` | `workflow.Step()` | Easy | Each rule becomes a Step |
| `wildcards` (`{sample}`) | Loop variable | Medium | Need to infer loop structure from `rule all` |
| `expand("{s}.bam", s=SAMPLES)` | `for sample in samples:` | Medium | Drives the main loop |
| `glob_wildcards()` | `URI.find()` or `FASTQ()` | Easy | Sample discovery |
| `input:` file patterns | `inputs=` / `.output()` | Medium | Resolve which rule produces each input |
| `output:` file patterns | `outputs=Outputs()` | Easy | Map globs to named connectors |
| `threads:` | `TaskSpec(cpu=)` | Easy | |
| `resources: mem_mb=` | `TaskSpec(mem=)` | Easy | Convert MB to GB |
| `resources: disk_mb=` | `TaskSpec(disk=)` | Easy | Convert MB to GB |
| `container:` / `singularity:` | `container=` | Easy | Singularity → Docker equivalent |
| `conda:` | `container=` | Easy | Auto-generate Dockerfile via mkdocker (`othersources/mkdocker/`) |
| `shell:` | `command=fr"..."` | Easy | Variable translation needed |
| `run:` (Python block) | `command=` with `language=Python()` | Medium | Or refactor into shell |
| `script:` | Resource + command | Medium | Upload script as resource |
| `configfile:` / `config["x"]` | `Params` class | Easy | |
| `params:` (rule params) | Python variables in `fr""` | Easy | |
| `temp()` | No action needed | Easy | scitq workspace is temporary by default |
| `protected()` | `Outputs(publish=...)` | Easy | Published outputs are persistent |
| `ancient()` | No equivalent needed | Easy | scitq always re-runs |
| `log:` | Captured in stdout/stderr | Easy | scitq captures all output |
| `benchmark:` | No direct equivalent | Skip | Use `step stats` instead |
| `checkpoint` | Python `if` + conditional steps | **Very hard** | Dynamic DAG, rare in practice |
| `lambda wildcards:` in input | Python logic in loop | Hard | |
| `scatter`/`gather` | Loop + `.grouped()` | Medium | |
| `ruleorder:` | Step ordering in code | Easy | Implicit from code order |
| `localrules:` | No equivalent needed | Skip | All scitq tasks run on workers |
| `include:` | Python `import` | Easy | |
| `module:` | Python `import` | Easy | |
| Multi-wildcard `expand()` | `itertools.product()` | Easy | See gpredomics example below |

## Variable translation

Snakemake `shell:` blocks use a mix of Python f-string-like syntax and shell variables:

| Snakemake | scitq `fr""` string | Notes |
|---|---|---|
| `{input}` | `/input/...` (explicit path) | Snakemake expands to file path |
| `{input.reads}` | `/input/...` (from named input) | Named input |
| `{output}` | `/output/...` (explicit path) | |
| `{wildcards.sample}` | `{sample}` (Python f-string) | Loop variable |
| `{threads}` | `${{CPU}}` | Shell variable set by scitq |
| `{resources.mem_mb}` | `${{MEM}}` (in MB) | Shell variable |
| `{params.x}` | `{x}` (Python f-string) | Rule param becomes Python var |
| `{log}` | Redirect to `/output/` | scitq captures stdout/stderr |
| Shell `$VAR` | `${{VAR}}` | Double braces in fr-string |

## Conversion patterns

### Pattern 1: Simple per-sample pipeline

**Snakemake:**
```python
SAMPLES = glob_wildcards("data/{sample}.fastq.gz").sample

rule all:
    input: expand("results/{sample}.bam", sample=SAMPLES)

rule trim:
    input: "data/{sample}.fastq.gz"
    output: "trimmed/{sample}.fastq.gz"
    threads: 4
    container: "staphb/fastp:1.0.1"
    shell: "fastp -i {input} -o {output} --thread {threads}"

rule align:
    input: "trimmed/{sample}.fastq.gz"
    output: "results/{sample}.bam"
    threads: 8
    container: "biocontainers/bwa:0.7.17"
    shell: "bwa mem -t {threads} ref.fa {input} | samtools sort -o {output}"
```

**scitq:**
```python
from scitq2 import *

class Params(metaclass=ParamSpec):
    input_dir = Param.string(required=True)
    location = Param.provider_region(required=True)

def pipeline(params: Params):
    workflow = Workflow(
        name="trim-align",
        description="Simple trim + align pipeline",
        version="1.0.0",
        tag=params.input_dir.split("/")[-1],
        language=Shell("bash"),
        worker_pool=WorkerPool(W.cpu >= 8, W.mem >= 16),
        provider=params.location.provider,
        region=params.location.region,
    )

    samples = URI.find(params.input_dir, filter="*.fastq.gz")

    for sample in samples:
        trim = workflow.Step(
            name="trim",
            tag=sample.sample_accession,
            container="staphb/fastp:1.0.1",
            command=fr"fastp -i /input/*.fastq.gz -o /output/{sample.sample_accession}.fastq.gz --thread ${{CPU}}",
            inputs=sample.uris,
            outputs=Outputs(fastqs="*.fastq.gz"),
            task_spec=TaskSpec(cpu=4),
        )

        align = workflow.Step(
            name="align",
            tag=sample.sample_accession,
            container="biocontainers/bwa:0.7.17",
            command=fr"bwa mem -t ${{CPU}} /resource/ref.fa /input/*.fastq.gz | samtools sort -o /output/{sample.sample_accession}.bam",
            inputs=trim.output("fastqs"),
            outputs=Outputs(bams="*.bam"),
            task_spec=TaskSpec(cpu=8),
        )

run(pipeline)
```

### Pattern 2: Collect / aggregate

**Snakemake:**
```python
rule merge:
    input: expand("results/{sample}.txt", sample=SAMPLES)
    output: "results/merged.txt"
    shell: "cat {input} > {output}"
```

**scitq:**
```python
    merge = workflow.Step(
        name="merge",
        command=fr"cat /input/*.txt > /output/merged.txt",
        container="alpine",
        inputs=per_sample_step.output("results", grouped=True),
        outputs=Outputs(merged="merged.txt", publish="azure://results/project/"),
    )
```

### Pattern 3: Multi-wildcard expand (Cartesian product)

This is the Snakemake equivalent of a parameter sweep. scitq handles this naturally with `itertools.product`, as demonstrated in the real-world `workflow/gpredomics.py` template.

**Snakemake:**
```python
SEEDS = range(1, 6)
METHODS = ["ga", "beam"]

rule all:
    input: expand("results/seed{seed}_{method}.txt", seed=SEEDS, method=METHODS)

rule run:
    output: "results/seed{seed}_{method}.txt"
    params:
        seed=lambda w: w.seed,
        method=lambda w: w.method
    shell: "mytool --seed {params.seed} --method {params.method} -o {output}"
```

**scitq:**
```python
from itertools import product

# ...

    seeds = range(1, 6)
    methods = ["ga", "beam"]

    for seed, method in product(seeds, methods):
        workflow.Step(
            name="run",
            tag=f"seed{seed}_{method}",
            command=fr"mytool --seed {seed} --method {method} -o /output/result.txt",
            container="myimage",
            outputs=Outputs(results="*.txt"),
        )
```

The `workflow/gpredomics.py` template shows a 6-dimensional product sweep in production — `itertools.product` is the idiomatic scitq way and requires no special DSL support.

### Pattern 4: Conditional rules

**Snakemake:**
```python
if config.get("run_qc", True):
    rule qc:
        input: "data/{sample}.fastq.gz"
        output: "qc/{sample}_report.html"
        shell: "fastqc {input} -o qc/"
```

**scitq:**
```python
    if params.run_qc:
        qc = workflow.Step(
            name="qc",
            tag=sample.sample_accession,
            command=fr"fastqc /input/*.fastq.gz -o /output/",
            container="biocontainers/fastqc:0.11.9",
            inputs=sample.fastqs,
            outputs=Outputs(reports="*.html"),
        )
```

### Pattern 5: Scatter / gather (fan-out then fan-in)

**Snakemake:**
```python
rule split:
    input: "data/{sample}.fastq.gz"
    output: expand("split/{{sample}}_chunk{n}.fastq.gz", n=range(4))
    shell: "split_fastq.sh {input} split/{wildcards.sample}_chunk 4"

rule process_chunk:
    input: "split/{sample}_chunk{n}.fastq.gz"
    output: "processed/{sample}_chunk{n}.txt"
    shell: "process.sh {input} > {output}"

rule gather:
    input: expand("processed/{{sample}}_chunk{n}.txt", n=range(4))
    output: "results/{sample}.txt"
    shell: "cat {input} > {output}"
```

**scitq:**
```python
    for sample in samples:
        chunks = []
        for n in range(4):
            chunk = workflow.Step(
                name="process_chunk",
                tag=f"{sample.sample_accession}_chunk{n}",
                command=fr"process.sh /input/*.fastq.gz > /output/result.txt",
                container="myimage",
                inputs=...,  # chunk input
                outputs=Outputs(results="*.txt"),
            )
            chunks.append(chunk)

        gather = workflow.Step(
            name="gather",
            tag=sample.sample_accession,
            command=fr"cat /input/*.txt > /output/{sample.sample_accession}.txt",
            container="alpine",
            inputs=chunks[-1].output("results", grouped=True),  # depends on all chunks
            outputs=Outputs(merged="*.txt"),
        )
```

## DAG resolution algorithm

The core challenge of Snakemake conversion. The converter must:

1. **Parse `rule all`** to find target files and extract wildcard dimensions (the `expand()` calls).
2. **Build a rule graph**: for each rule, match its `input:` patterns against other rules' `output:` patterns to determine dependencies.
3. **Identify the loop variable**: the wildcard that drives the main iteration (usually `{sample}`). Multi-wildcard expansions become nested loops or `product()`.
4. **Resolve the DAG into linear code**: topologically sort the rules and emit `workflow.Step()` calls in order, wiring `.output()` connectors.

### Steps:
```
Snakefile
  → parse rules (input/output patterns, wildcards, directives)
  → parse rule all (target expand, extract dimensions)
  → match input patterns to output patterns (build rule DAG)
  → topological sort
  → identify loop dimensions from expand()
  → emit Python DSL code
```

### Edge cases:
- **Ambiguous rules**: multiple rules could produce the same output pattern → `ruleorder:` resolves this
- **Dynamic wildcards**: `glob_wildcards()` in rule bodies → flag for manual review
- **Checkpoints**: fundamentally change the DAG at runtime → cannot be statically converted, emit TODO

## What the converter should handle (scope)

### Automated (Phase 1)
- Parse `Snakefile` / `.smk` files
- Extract `rule` definitions with `input:`, `output:`, `shell:`, `threads:`, `resources:`, `container:`, `params:`
- Parse `configfile:` and `config[]` access into `Params` class
- Parse `rule all` and `expand()` to determine loop structure
- Build rule dependency graph from input/output pattern matching
- Generate `for sample in samples:` loop with `workflow.Step()` calls
- Handle `expand(..., zip)` and multi-wildcard `expand()` via `product()`
- Convert `shell:` blocks with variable translation
- Map `temp()` → workspace (no action), `protected()` → `Outputs(publish=...)`

### Semi-automated (Phase 2 — generates TODO comments)
- `checkpoint` rules — flag as dynamic DAG, emit TODO
- `lambda wildcards:` in input — generate skeleton with TODO
- Complex `run:` blocks with Python logic — emit as commented Python
- `scatter`/`gather` patterns — generate skeleton
- `conda:` environments — auto-generate mkdocker Dockerfile from conda YAML

### Out of scope
- Snakemake profiles and cluster configs
- Snakemake wrappers (`wrapper:` directive) — too varied
- Report generation (`report:`)
- Singularity-specific features beyond container name

## Comparison with Nextflow converter

| Aspect | Nextflow | Snakemake |
|---|---|---|
| DAG construction | Explicit (channel wiring) | Implicit (file patterns) |
| Parsing difficulty | Medium (structured DSL2) | Hard (Python + custom DSL) |
| Process/rule → Step | Direct | Direct |
| Parallelism model | Channel-driven | Wildcard-driven |
| Fan-in | `.collect()` → `.grouped()` | `expand()` in input → `.grouped()` |
| Cartesian product | `.combine()` | `expand(a, b)` → `product()` |
| Converter complexity | Lower | Higher (DAG resolution needed) |
| Coverage estimate | ~80% of pipelines | ~65% of pipelines |

## Tool design

Integrated into the scitq CLI alongside the Nextflow converter:

```sh
scitq template convert --from-snakemake Snakefile
```

### Converter configuration

Same config as the Nextflow converter (shared `convert.yaml`):

```yaml
registry: gmtscience                  # Docker registry for generated images
base_image: gmtscience/mamba:1.5.8    # Base image for mkdocker-generated Dockerfiles
output_dir: ./converted               # Where to write the generated scitq template
docker_dir: ./dockers                  # Where to write generated mkdocker Dockerfiles
```

The `registry` and `base_image` are especially important for Snakemake since many rules use `conda:` instead of `container:`. When the converter encounters a conda env:

1. Parses the conda YAML (e.g. `envs/fastp.yaml`) to extract packages and versions
2. Generates a mkdocker Dockerfile in `docker_dir/`:
   ```docker
   FROM {base_image}
   RUN _conda install fastp=0.23.4
   #tag 0.23.4
   #registry {registry}
   ```
3. Emits `container="{registry}/fastp:0.23.4"` in the scitq DSL
4. Prints build instructions: `mkdocker dockers/fastp`

See [mkdocker](https://github.com/gmtsciencedev/mkdocker) for the Docker image generation tool.

### Shared intermediate representation

The converter shares the same IR as the Nextflow converter:

```
IR = list of StepSpec:
  - name: str
  - container: str
  - command: str
  - inputs: list[InputRef]   # reference to previous step output or URI
  - outputs: dict[str, str]  # name → glob pattern
  - task_spec: TaskSpec
  - depends: list[StepRef]
  - loop_vars: list[str]     # which wildcards drive this step
```

Both parsers (Nextflow, Snakemake) produce this IR, and a single code generator emits the scitq Python DSL.

## Implementation order

1. **Nextflow converter first** — easier parsing, higher coverage, more structured input
2. **Shared IR and code generator** — reusable across both converters
3. **Snakemake converter second** — reuses the code generator, focuses on DAG resolution
