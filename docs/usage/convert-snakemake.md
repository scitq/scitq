# Converting Snakemake pipelines to scitq

scitq includes an experimental converter that translates Snakemake workflows (`Snakefile`, `.smk` files) into scitq Python DSL templates. The generated code is a working starting point that needs manual review and adjustment.

Snakemake conversion is harder than Nextflow because the DAG is implicit (inferred from file patterns). The converter resolves the DAG by matching input/output patterns across rules.

## Usage

```sh
python -m scitq2.convert.snakemake Snakefile -o pipeline.py
```

Or without `-o` to print to stdout:

```sh
python -m scitq2.convert.snakemake Snakefile
```

Options:

- `--registry <name>` : Docker registry for generated mkdocker images (default: `gmtscience`)

## What it does

The converter:

- Parses Snakemake `rule` definitions (input, output, shell, threads, resources, conda, container)
- Parses `rule all` to identify targets and `expand()` calls
- Resolves the implicit DAG by matching output patterns to input patterns
- Classifies rules as one-off (no wildcards) or per-sample (with `{wildcards}`)
- Generates a complete scitq Python DSL template with `skip_if_exists=True`

### Automatic translations

| Snakemake | scitq |
|---|---|
| `rule` | `workflow.Step()` |
| `{wildcards}` in patterns | Per-sample loop with `tag=sample.sample_accession` |
| Rules without wildcards | One-off step (before the loop) |
| `expand()` in `rule all` | Fan-in step with `grouped=True` (after the loop) |
| `conda:` | `container=` + mkdocker Dockerfile on stderr |
| `container:` | `container=` (direct) |
| `threads:` | `TaskSpec(cpu=)` |
| `resources: mem_mb=` | `TaskSpec(mem=)` |
| `{threads}` | `${{THREADS}}` |
| `{resources.mem_mb}` | `${{MEM}}` |
| `{input.name}` | `/input/name` |
| `{output.name}` | `/output/name` |
| `{wildcards.sample}` | `{sample.sample_accession}` |
| `config["key"]` | `Params` class entry |
| `glob_wildcards()` | `URI.find()` (with TODO) |
| `directory()`, `temp()`, `protected()` | Stripped (scitq handles these natively) |
| DAG from file patterns | `skip_if_exists=True` at workflow level |

### DAG semantics with `skip_if_exists`

Snakemake's core model is "only run a rule if its output doesn't exist." The converter preserves this by setting `skip_if_exists=True` at the workflow level. When you re-run the workflow:

- Tasks whose output already exists are skipped (immediately marked as succeeded)
- Tasks whose output is missing run normally
- Dependencies are resolved correctly — a skipped task's dependents proceed immediately

This gives you Snakemake-style resumability for free.

### Conda environments

When a rule uses `conda:` instead of `container:`, the converter generates [mkdocker](https://github.com/gmtsciencedev/mkdocker) Dockerfiles (printed to stderr). For example, `conda: "envs/salmon.yaml"` produces:

```docker
FROM gmtscience/mamba
# TODO: parse envs/salmon.yaml and add _conda install commands
#registry gmtscience
```

For direct conda specs like `conda: "bioconda::salmon=1.10.3"`, the converter generates a complete Dockerfile:

```docker
FROM gmtscience/mamba
RUN _conda install salmon=1.10.3
#tag 1.10.3
#registry gmtscience
```

Build with `mkdocker dockers/salmon` before running the workflow.

## Example

Given this Snakefile:

```python
SAMPLES = glob_wildcards("data/{sample}_R1.fastq.gz").sample

rule all:
    input:
        expand("results/{sample}.bam", sample=SAMPLES),
        "results/merged.txt"

rule trim:
    input:
        r1="data/{sample}_R1.fastq.gz",
        r2="data/{sample}_R2.fastq.gz"
    output:
        r1="trimmed/{sample}_R1.fastq.gz",
        r2="trimmed/{sample}_R2.fastq.gz"
    threads: 4
    conda: "envs/fastp.yaml"
    shell:
        "fastp --in1 {input.r1} --in2 {input.r2} --out1 {output.r1} --out2 {output.r2} --thread {threads}"

rule align:
    input:
        r1="trimmed/{sample}_R1.fastq.gz",
        r2="trimmed/{sample}_R2.fastq.gz"
    output:
        "results/{sample}.bam"
    threads: 8
    conda: "envs/bwa.yaml"
    shell:
        "bwa mem -t {threads} ref.fa {input.r1} {input.r2} | samtools sort -o {output}"

rule merge:
    input:
        expand("results/{sample}.bam", sample=SAMPLES)
    output:
        "results/merged.txt"
    shell:
        "samtools merge {output} {input}"
```

The converter produces:

```python
from scitq2 import *

class Params(metaclass=ParamSpec):
    input_dir = Param.string(required=True, help="Input data directory")
    location = Param.provider_region(required=True, help="Provider and region")

def converted_workflow(params: Params):

    workflow = Workflow(
        name="converted-pipeline",
        description="Converted from Snakemake",
        version="1.0.0",
        language=Shell("bash"),
        skip_if_exists=True,  # Snakemake DAG semantics: only run if output missing
        worker_pool=WorkerPool(
            W.cpu >= 8,
            W.mem >= 8,
            max_recruited=10,
        ),
        provider=params.location.provider,
        region=params.location.region,
    )

    # TODO: adjust sample discovery to your data source
    samples = URI.find(params.input_dir, group_by="folder", filter="*.f*q.gz",
        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})

    for sample in samples:

        trim = workflow.Step(
            name="trim",
            tag=sample.sample_accession,
            container="gmtscience/fastp:latest",
            command=fr"""
            fastp --in1 /input/r1 --in2 /input/r2 \
                --out1 /output/r1 --out2 /output/r2 \
                --thread ${{THREADS}}
            """,
            inputs=sample.fastqs,
            outputs=Outputs(r1="*_R1.fastq.gz", r2="*_R2.fastq.gz"),
            task_spec=TaskSpec(cpu=4),
        )

        align = workflow.Step(
            name="align",
            tag=sample.sample_accession,
            container="gmtscience/bwa:latest",
            command=fr"""
            bwa mem -t ${{THREADS}} ref.fa /input/r1 /input/r2 \
                | samtools sort -o /output/
            """,
            inputs=trim.output("r1"),
            outputs=Outputs(output="*.bam"),
            task_spec=TaskSpec(cpu=8),
        )

    merge = workflow.Step(
        name="merge",
        command=fr"""
        samtools merge /output/ /input/*
        """,
        inputs=align.output("output", grouped=True),
        outputs=Outputs(output="merged.txt"),
    )

run(converted_workflow)
```

## What needs manual review

The generated code includes areas requiring attention:

- **Sample discovery**: `glob_wildcards()` is translated to a generic `URI.find()` call that needs adjustment
- **Input/output paths**: Snakemake uses file paths relative to the working directory; scitq uses `/input/`, `/output/`, `/resource/` mount points
- **Reference data**: Files like genome indexes should be `resources=` (read-only, shared) rather than `inputs=`
- **Container images**: mkdocker Dockerfiles from `conda:` YAML files need the actual package list filled in
- **Multi-input rules**: rules taking input from multiple upstream rules need manual wiring verification

## Limitations

The converter handles common patterns. It does not support:

- `checkpoint` rules (dynamic DAG) — these fundamentally change the DAG at runtime
- Complex `lambda wildcards:` expressions in input blocks
- `scatter`/`gather` patterns — generated as skeleton with TODO
- `run:` blocks (Python code instead of shell) — flagged for manual conversion
- `wrapper:` directives — too varied to automate
- Snakemake profiles and cluster configs
