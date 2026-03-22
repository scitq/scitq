# Nextflow to scitq Converter — Specification

## Goal

Build a tool that converts Nextflow (DSL2) workflow files (`.nf`) into scitq Python DSL templates. The converter targets the common bioinformatics pipeline patterns, which represent the vast majority of real-world Nextflow usage.

## Concept mapping

| Nextflow | scitq DSL | Notes |
|---|---|---|
| `process` | `workflow.Step()` | Each process becomes a Step; multiple input items generate multiple tasks within that step |
| `channel` / `.out` | `Outputs()` / `.output()` | Named output connectors replace channel wiring |
| `workflow {}` block | main function + `Workflow()` | The workflow block becomes the Python function body |
| `params.x` | `Params` class with `ParamSpec` | `Param.string()`, `Param.integer()`, `Param.boolean()`, `Param.enum()` |
| `publishDir` | `Outputs(publish=...)` | Maps to the publish clause in Outputs |
| `cpus` directive | `TaskSpec(cpu=)` | Dynamic concurrency |
| `memory` directive | `TaskSpec(mem=)` | Value in GB |
| `disk` directive | `TaskSpec(disk=)` | Value in GB |
| `container` | `container=` | Direct mapping |
| `input: path(x)` | `inputs=` | Input files from previous step or URI |
| `input: val(x)` | Python variable interpolation in `fr""` command | No scitq equivalent needed, use `{var}` |
| `output: path("*.txt")` | `outputs=Outputs(name="*.txt")` | Glob pattern in named output |
| `script:` / `shell:` | `command=fr"..."` | Shell variables: `${{VAR}}`, Python variables: `{var}` |
| `Channel.fromFilePairs()` | `FASTQ()` or `URI.find(group_by="folder")` | Biological sample discovery |
| `Channel.fromPath()` | `URI.find()` | General file discovery |
| `.collect()` | `.grouped()` or `.output("x", grouped=True)` | Fan-in: depend on all tasks of a step |
| `.flatten()` | Loop iteration in Python | Explicit `for item in items` |
| `.map { }` | Python list comprehension or loop | No direct equivalent, use Python |
| `.combine()` / `.cross()` | Nested Python loops | Cartesian product via `for a in A: for b in B:` |
| `.join()` | Python dict matching | Match by key across collections |
| `errorStrategy 'retry'` | `retry=N` | On Step or Workflow level |
| `maxRetries` | `retry=N` | Same |
| `when:` guard | Python `if` statement | Conditional step creation |
| `storeDir` | Not directly supported | Use `Outputs(publish=...)` for persistent storage |
| `stageInMode 'symlink'` | Automatic (resources are shared read-only) | scitq `/resource` is read-only by design |
| `module` / `include` | Python `import` | Standard Python module system |

## Conversion patterns

### Pattern 1: Linear pipeline

**Nextflow:**
```groovy
process FASTP {
    container 'staphb/fastp:1.0.1'
    cpus 4
    memory '8 GB'

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.fastq.gz"), emit: fastqs

    script:
    """
    fastp -i ${reads[0]} -o ${sample_id}.trimmed.fastq.gz \
        --thread ${task.cpus} --json ${sample_id}.json
    """
}

process ALIGN {
    container 'biocontainers/bwa:0.7.17'
    cpus 8

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.bam"), emit: bams

    script:
    """
    bwa mem -t ${task.cpus} /resource/ref.fa ${reads} | samtools sort -o ${sample_id}.bam
    """
}

workflow {
    reads = Channel.fromFilePairs(params.input_dir + '/*_{1,2}.fastq.gz')
    FASTP(reads)
    ALIGN(FASTP.out.fastqs)
}
```

**scitq:**
```python
from scitq2 import *
from scitq2.biology import FASTQ

class Params(metaclass=ParamSpec):
    input_dir = Param.string(required=True, help="Input directory with FASTQ files")
    location = Param.provider_region(required=True)

def pipeline(params: Params):
    workflow = Workflow(
        name="linear-pipeline",
        description="Linear fastp + bwa pipeline",
        version="1.0.0",
        tag=params.input_dir.split("/")[-1],
        language=Shell("bash"),
        worker_pool=WorkerPool(W.cpu >= 8, W.mem >= 16),
        provider=params.location.provider,
        region=params.location.region,
    )

    samples = FASTQ(params.input_dir)

    for sample in samples:
        fastp = workflow.Step(
            name="fastp",
            tag=sample.sample_accession,
            container="staphb/fastp:1.0.1",
            command=fr"""
            fastp -i /input/*_1.fastq.gz -o /output/{sample.sample_accession}.trimmed.fastq.gz \
                --thread ${{CPU}} --json /output/{sample.sample_accession}.json
            """,
            inputs=sample.fastqs,
            outputs=Outputs(fastqs="*.fastq.gz"),
            task_spec=TaskSpec(cpu=4, mem=8),
        )

        align = workflow.Step(
            name="align",
            tag=sample.sample_accession,
            container="biocontainers/bwa:0.7.17",
            command=fr"""
            bwa mem -t ${{CPU}} /resource/ref.fa /input/*.fastq.gz | samtools sort -o /output/{sample.sample_accession}.bam
            """,
            inputs=fastp.output("fastqs"),
            outputs=Outputs(bams="*.bam"),
            task_spec=TaskSpec(cpu=8),
        )

run(pipeline)
```

### Pattern 2: Fan-in (collect)

**Nextflow:**
```groovy
process MERGE {
    input:
    path(all_results)

    script:
    """
    cat *.txt > merged.txt
    """
}

workflow {
    // ... per-sample steps ...
    MERGE(PER_SAMPLE_STEP.out.results.collect())
}
```

**scitq:**
```python
    merge = workflow.Step(
        name="merge",
        command=fr"cat /input/*.txt > /output/merged.txt",
        container="alpine",
        inputs=per_sample_step.output("results", grouped=True),
        outputs=Outputs(merged="merged.txt"),
    )
```

### Pattern 3: Conditional steps

**Nextflow:**
```groovy
process OPTIONAL_STEP {
    when:
    params.run_optional

    input:
    path(data)

    script:
    """
    process_data.sh ${data}
    """
}
```

**scitq:**
```python
    if params.run_optional:
        optional_step = workflow.Step(
            name="optional",
            tag=sample.sample_accession,
            command=fr"process_data.sh /input/*",
            container="myimage",
            inputs=previous_step.output("data"),
        )

    # For downstream steps that may or may not depend on this:
    next_input = cond(
        (params.run_optional, lambda: optional_step.output("result")),
        default=lambda: previous_step.output("data"),
    )
```

### Pattern 4: Multi-output process

**Nextflow:**
```groovy
process SPLIT {
    output:
    path("*.bam"), emit: bams
    path("*.log"), emit: logs

    script:
    """
    tool --out-bam output.bam --log output.log
    """
}

workflow {
    SPLIT(input)
    DOWNSTREAM_A(SPLIT.out.bams)
    DOWNSTREAM_B(SPLIT.out.logs)
}
```

**scitq:**
```python
    split = workflow.Step(
        name="split",
        tag=sample_id,
        command=fr"tool --out-bam /output/output.bam --log /output/output.log",
        container="myimage",
        outputs=Outputs(bams="*.bam", logs="*.log"),
    )

    downstream_a = workflow.Step(
        name="downstream_a",
        tag=sample_id,
        inputs=split.output("bams"),
        # ...
    )

    downstream_b = workflow.Step(
        name="downstream_b",
        tag=sample_id,
        inputs=split.output("logs"),
        # ...
    )
```

## Variable translation

Nextflow variables in `script:` blocks must be converted:

| Nextflow | scitq `fr""` string |
|---|---|
| `${sample_id}` (Groovy interpolation) | `{sample_id}` (Python f-string) |
| `${task.cpus}` | `${{CPU}}` (shell variable set by scitq) |
| `${task.memory.toGiga()}` | `${{MEM}}` (shell variable set by scitq) |
| `\${SHELL_VAR}` (escaped, passed to shell) | `${{SHELL_VAR}}` (double braces in fr-string) |
| `${reads[0]}` | Depends on context; usually `/input/*_1.fastq.gz` glob |

## What the converter should handle (scope)

### Automated (Phase 1)
- Parse Nextflow DSL2 `.nf` files (process definitions, workflow block, params)
- Generate `Params` class from `params.*` usage
- Convert `process` blocks to `workflow.Step()` calls
- Map `container`, `cpus`, `memory` directives to `container=`, `TaskSpec()`
- Convert `script:` blocks to `fr"""..."""` commands with variable translation
- Wire `input`/`output` channels to `inputs=`/`outputs=Outputs()`
- Handle `.collect()` → `.grouped()`
- Handle `publishDir` → `Outputs(publish=...)`
- Handle `when:` → Python `if`

### Semi-automated (Phase 2 — generates TODO comments)
- `.map {}`, `.combine()`, `.cross()`, `.join()` — generate skeleton with TODO
- Complex Groovy expressions in `script:` blocks
- `Channel.from()` with computed values
- Multi-process fan-out from a single channel

### Out of scope
- Nextflow DSL1 (legacy syntax)
- Nextflow `Tower` / `Wave` integration
- Nextflow configs (`nextflow.config`) — these map to `scitq.yaml` but are too varied to automate
- Subworkflows (`include` from other `.nf` files) — flag for manual integration

## Tool design

The converter should be a Python CLI tool:

```sh
scitq convert-nf --input pipeline.nf --output pipeline.py
```

Or integrated into the scitq CLI:

```sh
scitq template convert --from-nextflow pipeline.nf
```

### Output format
- A complete, runnable scitq Python DSL template
- `TODO` comments where manual review is needed
- Warnings printed to stderr for unsupported constructs

### Implementation approach
- Use a Groovy/Nextflow parser (or regex-based extraction for the structured DSL2 syntax)
- Build an intermediate representation (list of processes with their directives, inputs, outputs, scripts)
- Generate Python code from the IR
- The generator should produce idiomatic scitq DSL (fr-strings, proper indentation, cond() where appropriate)
