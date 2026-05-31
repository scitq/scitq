# Converting Nextflow pipelines to scitq

scitq includes a converter that translates Nextflow DSL2 workflows (`.nf` files) into scitq [YAML templates](yaml-templates.md). The generated template is a working starting point that usually needs a small amount of manual review (per-step `worker_pool` overrides for GPU steps, promoting reference-data inputs to `resource:`, etc.).

A legacy [Python DSL](dsl.md) target is also available behind `--format dsl` for backward compatibility, but YAML is the recommended path: it carries the recent feature work (chaining, product iterator, keyed grouping, `--continue`/`--extend-workflow`, the module library) and is the format the converter is actively maintained against.

## Usage

```sh
# default: YAML output (format: 2)
python -m scitq2.convert.nextflow pipeline.nf -o pipeline.yaml

# explicit
python -m scitq2.convert.nextflow pipeline.nf --format yaml -o pipeline.yaml

# legacy Python DSL output
python -m scitq2.convert.nextflow pipeline.nf --format dsl -o pipeline.py

# print to stdout
python -m scitq2.convert.nextflow pipeline.nf
```

Optional flags:

- `--name <name>` — override the workflow `name:` field (default: `converted-pipeline`).
- `--registry <namespace>` — Docker registry used for conda-derived containers (default: `gmtscience`).

## What it does

The converter parses Nextflow `process` definitions (directives, inputs, outputs, scripts) and the `workflow {}` block, then emits a complete scitq YAML template (`format: 2`):

- one `params:` entry per `params.X = "..."` declaration, type-inferred from the default value;
- an `iterate:` block targeting URI-based sample discovery with a named `fastqs:` group (the bioinformatics default);
- a workflow-level `worker_pool:` sized to the largest declared process and per-step overrides where a process needs more (heavier RAM, GPU);
- a `workspace:` and `language: bash` setting;
- a `publish_root:` derived from `params.outdir` when the source defines one;
- one `steps:` entry per `process` invocation in workflow-call order.

### Automatic translations

| Nextflow | scitq YAML |
|---|---|
| `process` | step in `steps:` |
| `container 'image'` | `container: image` |
| `conda 'pkg=ver'` | `container: gmtscience/pkg:ver` + a mkdocker Dockerfile emitted on stderr |
| `cpus 4` / `memory '8 GB'` | `task_spec: { cpu: 4, mem: 8 }` |
| `label 'process_medium'` / `'process_high'` | `task_spec` filled from the [nf-core label heuristic](#nf-core-label-heuristics) when explicit `cpus`/`memory` aren't set |
| `publishDir "..."` | `publish: true` (paired with workflow-level `publish_root:`) |
| `input: tuple val(meta), path(reads)` | per-sample step (`tag` carried implicitly by the iterator) |
| `input: path(file)` only | one-off step (`per_sample: false`) |
| `.collect()` | `grouped: true` on the consumer step |
| `PROC.out.x` | `<proc>.x` step reference in `inputs:` |
| `Channel.fromFilePairs(params.X)` | `iterate: { source: uri, group_by: folder, fastqs: "*.f*q.gz" }` |
| `params.X` channel passed to a process | `{params.X}` URI in `inputs:` *(see "Manual review" below)* |
| `params.X` declaration | one `params:` entry with type-inferred default (`true`/`false` → boolean, digit → integer, otherwise string) |
| `params.outdir` | top-level `publish_root: "{params.location}://{params.outdir}"` |

### Script variable translation

| Nextflow | YAML `command: \|` |
|---|---|
| `${task.cpus}` | `$CPU` *(scitq sets this shell env var per task)* |
| `${task.memory}` | `$MEM` |
| `${params.X}` | `{params.X}` *(YAML engine substitution)* |
| `${meta.id}` / `${prefix}` / `${sample_id}` | `{SAMPLE}` *(iterator value, upper-case)* |
| `${reads[0]}` / `${reads[1]}` | `/input/*_1.fastq.gz` / `/input/*_2.fastq.gz` |
| `${ref_index}` (path() input) | `/input/ref_index` *(best-effort; may need fixing)* |
| `$VAR` (bare shell variable) | `$VAR` *(unchanged; YAML doesn't need double-braces)* |

### nf-core label heuristics

When a process declares only a `label '...'` directive (no explicit `cpus`/`memory`), the converter fills `task_spec` from the standard nf-core sizing:

| Label | CPU | Memory (GB) |
|---|---|---|
| `process_low` | 2 | 6 |
| `process_medium` | 6 | 36 |
| `process_high` | 12 | 72 |
| `process_high_memory` | 12 | 200 |
| `process_long` | 6 | 36 |
| `process_single` | 1 | 6 |

Explicit `cpus`/`memory` always win over the label.

### Conda → container

When a process uses `conda:` instead of `container:`, the converter:

1. Generates a [mkdocker](https://github.com/gmtsciencedev/mkdocker) Dockerfile (printed to **stderr** so it doesn't pollute the YAML).
2. Substitutes `{registry}/{package}:{version}` as the `container:` value.

For example, `conda 'bioconda::salmon=1.10.3'` produces:

```docker
FROM gmtscience/mamba
RUN _conda install salmon=1.10.3
#tag 1.10.3
#registry gmtscience
```

Build the image with `mkdocker dockers/salmon` before running the workflow.

## Example

Nextflow input:

```groovy
params.input_dir = "data"
params.outdir = "results"

process FASTP {
    container 'staphb/fastp:1.0.1'
    cpus 4
    memory '8 GB'

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.trimmed.fastq.gz"), emit: fastqs

    script:
    """
    fastp --in1 ${reads[0]} --in2 ${reads[1]} \
        --out1 ${sample_id}.1.trimmed.fastq.gz \
        --thread ${task.cpus}
    """
}

process MERGE {
    container 'alpine'
    publishDir "${params.outdir}", mode: 'copy'

    input:
    path(all_logs)

    output:
    path("merged.txt"), emit: merged

    script:
    """
    cat *.txt > merged.txt
    """
}

workflow {
    reads = Channel.fromFilePairs("${params.input_dir}/*_{1,2}.fastq.gz")
    FASTP(reads)
    MERGE(FASTP.out.fastqs.collect())
}
```

Converted YAML:

```yaml
# Converted from a Nextflow DSL2 pipeline by scitq2.convert.nextflow.
# Review before running: per-step worker_pool overrides, GPU labels,
# and reference-data inputs (consider promoting params.X channels
# referenced by inputs: to `resource:`).
format: 2
name: converted-pipeline
version: 1.0.0
description: Converted from Nextflow
params:
  input_dir:
    type: string
    default: data
  outdir:
    type: string
    default: results
  location:
    type: provider_region
    required: true
iterate:
  name: sample
  source: uri
  uri: '{params.input_dir}'
  group_by: folder
  fastqs: '*.f*q.gz'
worker_pool:
  provider: '{params.location}'
  cpu: '>= 4'
  mem: '>= 8'
  max_recruited: 10
workspace: '{params.location}'
language: bash
publish_root: '{params.location}://{params.outdir}'
steps:
  - name: fastp
    container: staphb/fastp:1.0.1
    inputs: sample.fastqs
    command: |
      fastp --in1 /input/*_1.fastq.gz --in2 /input/*_2.fastq.gz \
          --out1 {SAMPLE}.1.trimmed.fastq.gz \
          --thread $CPU
    outputs:
      fastqs: '*.trimmed.fastq.gz'
    task_spec:
      cpu: 4
      mem: 8
  - name: merge
    container: alpine
    inputs: fastp.fastqs
    command: |
      cat *.txt > merged.txt
    publish: true
    grouped: true
```

## What needs manual review

The converter is intentionally conservative — it gives you a runnable template skeleton, but a few classes of input are not reliably introspectable from the source `.nf`:

- **Sample discovery**. `Channel.fromFilePairs(params.X)` becomes `iterate: { source: uri, ..., fastqs: "*.f*q.gz" }` against `{params.input_dir}` (or `{params.reads}` if that's the declared param). If your data is on ENA/SRA, switch the iterator source. If file names differ from the default glob, adjust `fastqs:`.
- **Reference data vs sample data**. Nextflow uses the same `path(...)` channel mechanism for both, so the converter emits both as `inputs:`. For static reference data (host index, kraken DB, CheckM2 DB, …), move the URI to a step-level `resource:` so it's downloaded once and cached, not bundled into every task's input list. Look for `{params.X}`-shaped entries in `inputs:` — those are typically the reference channels.
- **GPU steps**. NF labels like `process_high_memory` or `accelerator 1, type: 'nvidia-...'` aren't currently auto-translated to a `worker_pool: { gpumem: ">= ..." }` override; add one by hand on the step that needs GPU recruitment (typical for SemiBin2 training, DeepVariant, etc.).
- **Multi-input channels mixing per-sample and reference**. Processes that take two inputs (per-sample reads + a shared index) get both listed in `inputs:`. The reference one usually wants to move to `resource:` (see above).
- **Cross-sample fan-in patterns**. The converter handles `.collect()` correctly (→ `grouped: true`). More advanced channel-algebra operators (`.combine()`, `.cross()`, `.map { … }`, `.join()` on a non-tag key) are not auto-translated and produce a best-effort guess; pressure-test the result and review.
- **Generated containers**. For `conda:` processes, verify that the mkdocker-generated container in `dockers/<step>/` matches the tool version you actually want (the converter strips the channel prefix; if you depend on a specific channel pin, edit the Dockerfile).
- **`workflow.containerEngine == 'singularity' ? ... : ...` ternaries**. The converter extracts the **Docker** branch (the `:` side); singularity URLs are ignored. If your shop uses singularity, regenerate with `--registry` pointing at your singularity-shaped registry, or hand-edit the `container:` values.

## Verifying the output

Once converted, the standard YAML-template flow applies:

```sh
# Schema check (no upload, no run)
python -m scitq2.yaml_runner pipeline.yaml --params

# Compile-time validation (creates the workflow, then deletes it)
python -m scitq2.yaml_runner pipeline.yaml --dry-run --values '{...}'

# Real run
scitq template upload --path pipeline.yaml
scitq template run --name converted-pipeline --param "input_dir=...,location=..."
```

## Limitations

- Nextflow DSL1 (legacy syntax) — out of scope.
- Subworkflows / `include` from other `.nf` files — merge manually before converting, or convert each `.nf` separately and wire them together with [chaining](yaml-templates.md#chaining-workflows).
- `nextflow.config` — too varied to auto-translate; settings that map naturally to scitq concepts (worker pool defaults, default container, retries) need to be folded into the YAML by hand.
- Complex Groovy logic inside `script:` blocks (if/else over `meta`, variable assignments) — the converter takes the last (usually paired-end) branch and emits TODO comments for the rest.
- Channel algebra: `.combine()`, `.cross()`, `.map { ... }`, `.join()` on non-tag keys — flagged with TODO. For static multi-dimensional fan-out (per (sample × chrom) variant calling, per (sample × lane) preprocessing), use the YAML [`product:` iterator and `grouped_by:` step](yaml-templates.md#product-outer-product-multi-dimensional-iteration). For runtime data-dependent fan-out (e.g. per-MAG QC after a binner emits N variable MAGs), [chain a follow-up workflow](yaml-templates.md#chaining-workflows) whose iterator enumerates the prior workflow's outputs.
