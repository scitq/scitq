# YAML Pipeline Definitions

## Goal

A declarative YAML format for defining scitq pipelines. Targets the 80% common case (linear per-sample pipelines) with near-zero syntax errors. Designed for AI agents and users who want simplicity over flexibility.

The Python DSL remains available for complex workflows (multi-dimensional product sweeps, dynamic logic).

## Execution model

YAML pipelines are **executed by the server**. The user interacts through the CLI only:

```sh
# Run a local YAML file — CLI reads it and sends content to the server
scitq pipeline run pipeline.yaml --param input_dir=azure://data,location=azure.primary:swedencentral

# Dry run — server creates and deletes the workflow
scitq pipeline run pipeline.yaml --param ... --dry-run

# Print parameter schema
scitq pipeline params pipeline.yaml

# Upload a YAML module to the server for reuse
scitq module upload modules/classify.yaml
```

The flow:
1. User writes `pipeline.yaml` on their machine
2. CLI reads the file, sends its content to the server
3. Server resolves `import:` references from its module library
4. Server builds the Workflow, compiles it, starts execution
5. The pipeline YAML content is **not stored** on the server (transient) unless uploaded as a template

No Python needed on the client side. The CLI is a Go binary.

### Modules on the server

YAML modules must be uploaded to the server before they can be imported:

```sh
scitq module upload modules/classify.yaml
scitq module upload modules/align.yaml
scitq module list
```

Modules are stored in `{script_root}/modules/`. When the server encounters `import: classify.yaml`, it looks up its module library.

Python modules (`scitq2_modules` and `extra_packages`) are installed in the server's venv and also resolved server-side.

## Format

```yaml
name: metagenomics
version: 1.0.0
description: Metagenomic classification pipeline

params:
  input_dir:
    type: string
    required: true
    help: Input data directory with FASTQ files
  paired:
    type: boolean
    default: true
    help: Whether data is paired-end
  depth:
    type: string
    default: "10M"
    help: Subsampling depth
  location:
    type: provider_region
    required: true
    help: Provider and region

iterate:
  name: sample
  source: uri
  uri: "{params.input_dir}"
  group_by: folder
  filter: "*.f*q.gz"

worker_pool:
  provider: "{params.location}"
  cpu: ">= 8"
  mem: ">= 60"
  max_recruited: 10

workspace: "{params.location}"

steps:
  - module: fastp
    paired: "{params.paired}"

  - module: bowtie2_host_removal
    inputs: fastp.fastqs
    paired: "{params.paired}"
    resource: "azure://ref/chm13v2.0.tgz|untar"

  - module: seqtk_sample
    inputs: humanfilter.fastqs
    paired: "{params.paired}"
    depth: "{params.depth}"

  - import: classify.yaml
    inputs: seqtk_sample.fastqs

  - module: multiqc
    inputs: fastp.json
    grouped: true
```

## Params

All workflow parameters must be declared. This makes pipelines self-documenting and enables the UI/CLI to present parameter forms.

```yaml
params:
  input_dir:
    type: string
    required: true
    help: Input data directory
  paired:
    type: boolean
    default: true
    help: Whether data is paired-end
  depth:
    type: string
    default: "10M"
  mode:
    type: enum
    choices: [fast, accurate]
    default: fast
  reference:
    type: path
    required: true
    help: URI to reference data (validated at parse time)
  location:
    type: provider_region
    required: true
    help: Provider and region for execution
```

Supported types: `string`, `integer`, `boolean`, `enum`, `path`, `provider_region`.

The `paired` parameter is particularly important in bioinformatics. It should be declared explicitly and passed to modules that need it.

## Iterators

Iterators define what the pipeline loops over. Each iterator declares a **named variable** that becomes available as `${NAME}` (uppercased) in commands.

### Single iterator (most common case)

```yaml
# URI-based sample discovery — defines ${SAMPLE}
iterate:
  name: sample
  source: uri
  uri: "{params.input_dir}"
  group_by: folder
  filter: "*.f*q.gz"
```

```yaml
# ENA public data
iterate:
  name: sample
  source: ena
  identifier: "{params.bioproject}"
  group_by: sample_accession
  filter:
    library_strategy: WGS
```

```yaml
# SRA public data
iterate:
  name: sample
  source: sra
  identifier: "{params.bioproject}"
  group_by: sample_accession
```

```yaml
# Range — defines ${SEED}
iterate:
  name: seed
  source: range
  start: 1
  end: 5
```

```yaml
# Explicit list — defines ${METHOD}
iterate:
  name: method
  source: list
  values: [ga, beam, mcmc]
```

```yaml
# Lines from a file — defines ${SAMPLE_ID}
iterate:
  name: sample_id
  source: lines
  file: "{params.sample_list}"
```

### Product iterator (cartesian product)

Multiple iterators with `mode: product` generate all combinations — the YAML equivalent of `itertools.product()` in gpredomics:

```yaml
iterate:
  mode: product
  over:
    - name: seed
      source: range
      start: 1
      end: 5
    - name: method
      source: list
      values: [ga, beam]
    - name: k_penalty
      source: list
      values: [0.0001, 0.0005, 0.001]
```

This produces 5 × 2 × 3 = 30 iterations, each with `${SEED}`, `${METHOD}`, and `${K_PENALTY}` set.

### Iterator variables in commands

Each iterator's `name` field defines an environment variable available in step commands:

```yaml
iterate:
  name: sample        # → ${SAMPLE}

steps:
  - name: process
    command: tool --input /input/${SAMPLE}.fastq.gz --output /output/${SAMPLE}.result
```

For URI/ENA/SRA sources, the iterator also provides `inputs` (the file URIs) that the first step receives automatically.

### No iterator

If `iterate:` is omitted, the pipeline has a single pass (no loop). All steps run once. Useful for aggregation-only pipelines or preparation workflows.

## Worker pool and workspace

Both reference the location **explicitly** — no implicit use of params:

```yaml
worker_pool:
  provider: "{params.location}"     # explicit
  cpu: ">= 8"
  mem: ">= 60"
  disk: ">= 400"
  max_recruited: 10
  task_batches: 2

workspace: "{params.location}"      # explicit — determines where intermediate data is stored
```

The `workspace:` field resolves to the workspace root configured for that provider/region in `scitq.yaml` (the `local_workspaces` setting). This controls where intermediate task outputs live.

## Steps

### Step types

There are four ways to define a step:

#### 1. Python module

```yaml
- module: fastp
  paired: "{params.paired}"
  inputs: sample.fastqs
```

Resolves via Python import: `scitq2_modules.fastp` or `gmt_modules.hermes`.

#### 2. YAML module import

```yaml
- import: classify.yaml
  inputs: fastp.fastqs
  resource: "{params.kraken_db}"
```

Resolves from the server's module library (`script_root/modules/`). Must be uploaded beforehand with `scitq module upload`.

#### 3. Ad-hoc container

```yaml
- name: classify
  conda: bioconda::kraken2=2.1.3
  command: kraken2 --db /resource/ --threads ${THREADS} /input/*.fastq.gz
  inputs: fastp.fastqs
  resource: "{params.kraken_db}"
```

Auto-generates a preparation task that builds the Docker image on each worker (see [adhoc-containers.md](adhoc-containers.md)). Image name is the md5 of the install instruction.

Supported install methods: `conda:`, `apt:`, `binary:`, `pip:`.

#### 4. Custom inline step

```yaml
- name: merge_reports
  container: ubuntu:latest
  command: |
    cat /input/*.txt > /output/merged.txt
  inputs: classify.report
  grouped: true
  outputs:
    merged: "*.txt"
  publish: true
```

### Common step fields

| Field | Description |
|---|---|
| `inputs` | Dot notation: `step_name.output_name`. Default: previous step's first output, or `sample.fastqs` for the first step |
| `resource` | URI to a resource file or folder (mounted read-only in `/resource/`). Supports `\|untar`, `\|gunzip` actions |
| `outputs` | Named output globs: `report: "*.txt"` |
| `publish` | `true` (uses `publish_root`) or a full URI |
| `grouped` | `true` for fan-in steps (after the sample loop, collects all samples) |
| `per_sample` | `false` for one-off steps (before the sample loop, e.g. index building) |
| `skip_if_exists` | `true` to skip if output already has files |
| `paired` | `true`/`false` or `"{params.paired}"` — passed to modules that support it |
| `task_spec` | `cpu:`, `mem:`, `disk:` per task |
| `worker_pool` | Override with `max_recruited:` etc. |
| `container` | Override the module's default container |

### The `resource` field

Called `resource`, not `db_resource` or `database`. A resource is any read-only data mounted in `/resource/` — it can be a database, a reference genome, a training set, a binary, an index. The name is deliberately generic:

```yaml
# A Kraken2 database
- import: classify.yaml
  resource: "azure://ref/kraken2_gtdb.tgz|untar"

# A training set for ML
- import: gpredomics.yaml
  resource: "azure://ref/training_data.tsv"

# A genome index
- module: bowtie2_host_removal
  resource: "azure://ref/chm13v2.0.tgz|untar"
```

### Conditional commands (`cond:`)

When a command depends on a parameter value, use `cond:` to select between variants:

```yaml
command:
  cond: "{params.paired}"
  true: |
    . /builtin/std.sh
    . /builtin/bio.sh
    _find_pairs /input/*.f*q.gz
    _para zcat $READS1 > /tmp/r1.fq
    _para zcat $READS2 > /tmp/r2.fq
    _wait
    fastp --in1 /tmp/r1.fq --in2 /tmp/r2.fq \
        --out1 /output/${SAMPLE}.1.fastq.gz \
        --out2 /output/${SAMPLE}.2.fastq.gz \
        --thread ${CPU} --json /output/${SAMPLE}.json
  false: |
    . /builtin/std.sh
    zcat /input/*.f*q.gz | fastp --stdin \
        -o /output/${SAMPLE}.fastq.gz \
        --thread ${CPU} --json /output/${SAMPLE}.json
```

`cond:` names a parameter. The keys are its possible values. For booleans: `true`/`false`. For enums or strings: the literal values.

```yaml
# Boolean condition
command:
  cond: "{params.paired}"
  true: |
    bowtie2 -1 /input/*.1.fq.gz -2 /input/*.2.fq.gz ...
  false: |
    bowtie2 -U /input/*.fq.gz ...

# Enum condition
command:
  cond: "{params.aligner}"
  bowtie2: |
    bowtie2 -x /resource/index ...
  bwa: |
    bwa mem /resource/index ...

# String condition
command:
  cond: "{params.version}"
  "4.0": |
    metaphlan --bowtie2db /resource/v4.0 ...
  "4.1": |
    metaphlan --bowtie2db /resource/v4.1 ...
```

If `command:` is a plain string (no `cond:`), it's used unconditionally.

`cond:` also works on other fields where branching is useful:

```yaml
container:
  cond: "{params.version}"
  "4.0": "gmtscience/metaphlan4:4.0.6.1"
  "4.1": "gmtscience/metaphlan4:4.1"

resource:
  cond: "{params.version}"
  "4.0": "azure://rnd/resource/metaphlan4.0.5.tgz|untar"
  "4.1": "azure://rnd/resource/metaphlan4.1.tgz|untar"
```

### Language

YAML commands default to `sh` — because `${CPU}`, `${SAMPLE}`, pipes, and redirects all require shell expansion. This differs from the Python DSL which defaults to `Raw()` (no shell) since it has f-strings for variable interpolation.

```yaml
# Top-level default (applies to all steps)
language: sh              # default, can be omitted

# Per-step override
steps:
  - name: my_analysis
    language: python
    container: python:3.12
    command: |
      import os, json
      sample = os.environ['SAMPLE']
      with open(f'/input/{sample}.json') as f:
          data = json.load(f)
      # ... process ...
      json.dump(result, open(f'/output/{sample}_result.json', 'w'))
```

| Value | Meaning |
|---|---|
| `sh` | Default. Command runs in `sh -c "..."`. Works in all containers (alpine, ubuntu, etc.) |
| `bash` | Command runs in `bash -c "..."`. Needed for bash-specific features (arrays, `set -o pipefail`) |
| `python` | Command runs as a Python script. The container must have Python |
| `none` | No shell wrapping. Command is passed directly as Docker entrypoint arguments. Use for binaries that don't need shell features |

Note: scitq builtins (`. /builtin/std.sh`, `_para`, `_wait`) require a shell. If `language: none`, builtins are not available.

### Variables in commands

YAML commands have access to these environment variables (set by the worker and the YAML runner):

| Variable | Source | Description |
|---|---|---|
| `${CPU}` | Worker | Number of CPUs available for this task |
| `${THREADS}` | Worker | Same as CPU (alias for Snakemake compatibility) |
| `${MEM}` | Worker | Available memory in GB |
| Iterator variables | `iterate:` block | `${SAMPLE}`, `${SEED}`, etc. — from the iterator's `name` field, uppercased |

These are container environment variables. They require a shell to expand (`language: sh` or `bash`). In `language: python`, access them via `os.environ['CPU']`. In `language: none`, they are still set but not expanded — the binary must read them from the environment itself.

## Top-level options

```yaml
name: pipeline-name
version: 1.0.0
description: What this pipeline does
tag: "{params.input_dir}"              # workflow name suffix
language: sh                            # default: sh (shell). Options: sh, bash, python, none
container: alpine                       # default container for custom steps
publish_root: "azure://results/project" # base URI for publish=true
skip_if_exists: true                    # DAG mode for all steps
retry: 2                               # default retries
```

## YAML modules

### Definition

A YAML module is a single-step YAML file defining a reusable tool wrapper:

```yaml
# modules/classify.yaml
name: classify
conda: bioconda::kraken2=2.1.3
command:
  cond: paired
  true: |
    kraken2 --db /resource/ --threads ${THREADS} \
        --report /output/${SAMPLE}.report \
        --gzip-compressed --paired \
        /input/${SAMPLE}.1.fastq.gz /input/${SAMPLE}.2.fastq.gz \
        --output /dev/null
  false: |
    kraken2 --db /resource/ --threads ${THREADS} \
        --report /output/${SAMPLE}.report \
        --gzip-compressed \
        /input/${SAMPLE}.fastq.gz \
        --output /dev/null
outputs:
  report: "*.report"
task_spec:
  cpu: 8
  mem: 60
```

A module defines: `name`, `command` (plain or with `cond:`), `container` or `conda`/`apt`/`binary`/`pip`, `outputs`, `task_spec`.

A module may declare **variables** it expects — in the `cond:` construct, the condition variable (e.g. `paired`) must be provided by the importing pipeline as a step field.

A module does **not** define: `inputs`, `grouped`, `resource` — those come from the importing pipeline.

### Uploading

```sh
scitq module upload modules/classify.yaml
scitq module upload modules/align.yaml
scitq module list
scitq module detail --name classify
```

Modules are stored in `{script_root}/modules/` on the server.

### Importing

```yaml
steps:
  - import: classify.yaml
    inputs: fastp.fastqs
    resource: "{params.kraken_db}"
    paired: "{params.paired}"
```

The pipeline provides what the module doesn't: inputs, resource, paired flag, grouped mode. Any field from the module can be overridden.

### Merge semantics

| Source | Fields |
|---|---|
| **From module** | `name`, `command`, `container`/`conda`, `outputs`, `task_spec` |
| **From pipeline** (always) | `inputs`, `grouped`, `resource`, `paired` |
| **From pipeline** (override) | `container`, `task_spec`, `worker_pool`, `name` |

## Comparison with Python DSL

| Aspect | YAML | Python DSL |
|---|---|---|
| Target user | AI agents, bioinformaticians | Developers |
| Complexity | Linear pipelines, product sweeps | Any topology |
| Conditionals | `cond:` on any param (boolean, enum, string) | Full Python (`cond()`, `if/else`) |
| Loops | `iterate:` (single, product, range, list, lines) | Any Python loop |
| Modules | YAML files + Python modules | Python functions |
| Execution | Via CLI (`scitq pipeline run`) | Direct (`python template.py`) or via CLI |
| Server dependency | Always (CLI sends to server) | Optional (can `--dry-run` locally) |

## Complete example: parameter sweep with product iterator

This is the YAML equivalent of `workflow/gpredomics.py`:

```yaml
name: gpredomics
version: 0.1.0
description: Gpredomics parameter sweep

params:
  bioproject:
    type: string
    required: true
  X:
    type: path
    required: true
    help: Learning dataset
  y:
    type: path
    required: true
    help: Labels for learning dataset
  Xtest:
    type: path
    required: true
    help: Testing dataset
  ytest:
    type: path
    required: true
    help: Labels for testing dataset
  location:
    type: provider_region
    required: true

iterate:
  mode: product
  over:
    - name: seed
      source: range
      start: 1
      end: 5
    - name: k_penalty
      source: list
      values: [0.0001, 0.0005, 0.001]
    - name: fitting_mode
      source: list
      values: [auc, specificity]

worker_pool:
  provider: "{params.location}"
  cpu: "== 32"
  mem: ">= 120"
  disk: ">= 400"
  max_recruited: 10
  task_batches: 2

workspace: "{params.location}"

steps:
  - name: gpredomics
    container: gmtscience/gpredomics:0.7.7
    command: |
      cd /output
      cat << EOF > param.yaml
      general:
        seed: ${SEED}
        fit: ${FITTING_MODE}
        k_penalty: ${K_PENALTY}
        thread_number: ${CPU}
      data:
        X: "/resource/X"
        y: "/resource/y"
      EOF
      gpredomics
    resource:
      - "{params.X}"
      - "{params.y}"
      - "{params.Xtest}"
      - "{params.ytest}"
    outputs:
      results: "*"
    publish: true
```

## Implementation plan

1. **CLI**: `scitq pipeline run`, `scitq pipeline params`, `scitq module upload/list/detail`
2. **Server**: YAML runner integrated into the template execution engine (recognizes `.yaml`)
3. **Server**: `{script_root}/modules/` directory for YAML modules
4. **Server**: iterator variables injected as container environment variables
5. **YAML runner**: resolve imports, `cond:` evaluation, ad-hoc containers, product iterators
6. **Server config**: `extra_packages` for private Python module packages
