# YAML Templates

## Goal

A declarative YAML format for defining scitq templates. A YAML template creates a workflow, just like a Python DSL template, but with a simpler, more constrained syntax. Targets the 80% common case (linear per-sample workflows) with near-zero syntax errors. Designed for AI agents and users who want simplicity over flexibility.

Python DSL templates remain available for complex workflows (multi-dimensional product sweeps, dynamic logic). Both types coexist under the unified `scitq template` CLI.

## Execution model

YAML templates are **executed by the server**. The user interacts through the CLI:

```sh
# Power user: run a local YAML file directly
scitq template run --path biomscope.yaml --param bioproject=PRJEB6070,location=azure.primary:swedencentral

# Standard user: run an uploaded template by name
scitq template run --name biomscope --param bioproject=PRJEB6070,location=azure.primary:swedencentral

# Dry run
scitq template run --path biomscope.yaml --param ... --dry-run

# Upload a template for reuse
scitq template upload --path biomscope.yaml

# Upload a YAML module to the server
scitq module upload --path modules/classify.yaml
```

The flow for `--path` (power user):
1. User writes `biomscope.yaml` on their machine
2. CLI reads the file, sends its content to the server
3. Server resolves `import:` and `module:` references from its module library
4. Server builds the Workflow, compiles it, starts execution
5. The template content is **not stored** on the server (transient)

The flow for `--name` (standard user):
1. Template was previously uploaded with `scitq template upload`
2. Server runs it from `{script_root}/`

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

### Parameter dependencies (`requires`)

A parameter can declare that it requires other parameters to have specific values. The `requires` block uses an optional `when:` clause to specify which value triggers the constraint:

```yaml
params:
  oral:
    type: boolean
    default: false
  nsat:
    type: boolean
    default: false
    requires:
      oral: true      # nsat=true requires oral=true (when: defaults to truthy)
```

For enum parameters, use `when:` to match a specific value:

```yaml
  metaphlan:
    type: enum
    choices: ["No", "4.0", "4.1"]
    default: "No"
    requires:
      when: "4.0"
      legacy_db: true   # metaphlan=4.0 requires legacy_db=true
```

If `when:` is omitted, the constraint triggers whenever the parameter is truthy. Validation runs after all parameters (including defaults) are resolved, so contradictory defaults are caught at launch time.

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

## Resource root

### Problem

Reference data (genome indexes, catalogs, databases) must live near the compute to avoid egress fees. Currently templates hardcode URIs like `azure://rnd/resource/igc2.tgz`, but if the workflow runs in a different region or provider, the data must be fetched cross-region (costly).

### Solution: `local_resources` in provider config

Add `local_resources` alongside `local_workspaces` in `scitq.yaml`:

```yaml
providers:
  azure:
    primary:
      local_workspaces:
        swedencentral: "azswed://rnd/workspace"
        westeurope: "azwest://rnd/workspace"
        northeurope: "aznorth://rnd/workspace"
      local_resources:
        swedencentral: "azswed://rnd/resource"
        westeurope: "azwest://rnd/resource"
        northeurope: "aznorth://rnd/resource"
  openstack:
    ovh:
      local_workspaces:
        GRA11: "s3://rnd/workspace"
      local_resources:
        GRA11: "s3://rnd/resource"
```

### `{RESOURCE_ROOT}` auto-variable

Like `workspace:`, the `RESOURCE_ROOT` is resolved from the provider/region and injected as a workflow-level variable:

```yaml
workspace: "{params.location}"    # resolves to azswed://rnd/workspace
# RESOURCE_ROOT is auto-resolved to azswed://rnd/resource (same provider/region)
```

Modules then reference resources relative to `{RESOURCE_ROOT}`:

```yaml
# In a module:
resource: "{RESOURCE_ROOT}/igc2.tgz|untar"
```

The template never hardcodes `azure://` or `s3://` — it's all resolved from the provider.

### Simplified modules

With `{RESOURCE_ROOT}`, modules can have sensible defaults:

```yaml
# genetic/bowtie2_host_removal.yaml — host reference defaults to chm13v2.0
name: humanfilter
container: gmtscience/bowtie2:2.5.4
language: bash
resource: "{RESOURCE_ROOT}/chm13v2.0.tgz|untar"    # default, overridable
command:
  cond: paired
  true: |
    . /builtin/std.sh
    bowtie2 -p $CPU --mm -x /resource/chm13v2.0/chm13v2.0 ...
  false: |
    ...
```

The template doesn't need to specify the resource at all — the default works:

```yaml
steps:
  - import: genetic/bowtie2_host_removal    # uses default chm13v2.0 from RESOURCE_ROOT
    inputs: fastp.fastqs
    paired: "{PAIRED}"
```

### Biomscope simplification

With `{RESOURCE_ROOT}` and catalog-name-based convention, the biomscope pipeline collapses from separate align/species/function/oral modules to a single parameterized module:

```yaml
# genetic/biomscope_align.yaml — works for both igc2 and oral
name: biomscope
container: 3jfz1gy8.gra7.container-registry.ovh.net/library/metagen_rust:1.0.0
language: bash
resource: "{RESOURCE_ROOT}/{CATALOG}.tgz|untar"
command: |
  . /builtin/std.sh
  cd /output && \
  bowtie2 --seed {SEED} --end-to-end --sensitive -p 6 \
      -x /resource/{CATALOG}/{CATALOG} \
      -U $(echo /input/*.fastq.gz | tr ' ' ',') \
      -k 100 --no-unal --no-sq --no-head --trim-to 80 --mm \
      --met-file /output/{SAMPLE}_{CATALOG_LABEL}_bowtie_stats.txt \
      2> >(tee /output/{SAMPLE}_{CATALOG_LABEL}_bowtie.txt >&2) \
  | metagen-counter -c 1 -a dist1 -s {SAMPLE} 1> {SAMPLE}_counter.tsv
```

The template uses it twice — once for gut, once for oral:

```yaml
steps:
  - import: genetic/biomscope_align
    inputs: seqtk.fastqs
    vars:
      CATALOG: "igc2"
      CATALOG_LABEL: "catalog"

  - import: genetic/biomscope_align
    when: "{params.oral}"
    inputs: seqtk.fastqs
    vars:
      CATALOG: "hs84oral"
      CATALOG_LABEL: "oral_catalog"
```

Same module, different catalog name. No code duplication. The resource `{RESOURCE_ROOT}/igc2.tgz` or `{RESOURCE_ROOT}/hs84oral.tgz` resolves to the right storage region automatically.

### Implementation

1. **Server config** (`server/config/config.go`): add `LocalResources map[string]string` to provider configs (same structure as `LocalWorkspaces`)
2. **Server API**: add `get_resource_root(provider, region)` alongside `get_workspace_root`
3. **Python DSL** (`grpc_client.py`): expose `get_resource_root`
4. **YAML runner**: resolve `RESOURCE_ROOT` at workflow level from provider/region, inject as auto-variable
5. **Modules**: update defaults to use `{RESOURCE_ROOT}/...`

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
| `accept_failure` | `true` to allow this step to run even if some prerequisites failed terminally (all retries exhausted). Default: `false` — any failed prerequisite blocks the step |
| `paired` | `true`/`false` or `"{params.paired}"` — passed to modules that support it |
| `task_spec` | `cpu:`, `mem:`, `disk:` per task |
| `worker_pool` | Override with `max_recruited:` etc. |
| `container` | Override the module's default container |

### Dependency behavior and `accept_failure`

Dependencies between steps are automatic: a step that declares `inputs: fastp.fastqs` depends on the `fastp` step. For `grouped: true` (fan-in) steps, the task depends on **all** tasks from the referenced step across all iterations.

By default, a dependent task only runs when **all** its prerequisites have succeeded (`status = 'S'`). If any prerequisite fails — even after exhausting all retries — the dependent task stays blocked in `W` (Waiting) forever.

The `accept_failure: true` flag changes this: a prerequisite is also considered done if it has **terminally failed** (status `'F'` with `retry = 0`, meaning all retries exhausted). This is useful for aggregation steps that should run with partial results:

```yaml
# Compile results even if some samples failed
- module: compile_results.yaml
  inputs: analysis.output
  grouped: true
  accept_failure: true
  publish: "{params.final_output}"
```

A prerequisite that fails but still has retries remaining is **not** considered terminal — the retry will create a fresh clone, and the dependency waits for the clone's outcome. Only when all retries are exhausted does `accept_failure` apply.

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

| Value | Meaning | Typed interpolation |
|---|---|---|
| `sh` | Default. Command runs in `sh -c "..."`. Works in all containers (alpine, ubuntu, etc.) | No |
| `bash` | Command runs in `bash -c "..."`. Needed for bash-specific features (arrays, `set -o pipefail`) | No |
| `python` | Command runs as a Python script. The container must have Python | Yes (`True`/`False`) |
| `r` | Command runs as an R script (via `Rscript`). The container must have R | Yes (`TRUE`/`FALSE`) |
| `none` | No shell wrapping. Command is passed directly as Docker entrypoint arguments. Use for binaries that don't need shell features | No |

Note: scitq builtins (`. /builtin/std.sh`, `_para`, `_wait`) require a shell. If `language: none`, builtins are not available.

### Typed interpolation for Python and R

When `language` is `python` or `r`, `{VAR}` resolves to a **typed literal** appropriate for the language instead of a raw string:

| Value | Shell (`sh`/`bash`) | Python | R |
|---|---|---|---|
| `"true"` | `true` | `True` | `TRUE` |
| `"false"` | `false` | `False` | `FALSE` |
| `"42"` | `42` | `42` | `42` |
| `"hello"` | `hello` | `'hello'` | `'hello'` |

This allows using `{VAR}` directly in Python/R code without manual type conversion:

```yaml
- name: compile
  language: python
  command: |
    sample_list = {SAMPLES}.split(',')    # → 'S001,S002,...'.split(',')

    if {COMPUTE_NSAT}:                    # → if True: / if False:
        compute_nsat()

    json.dump({
        'batch': {BATCH},                # → 'my_batch'
        'depth': {DEPTH},                # → 20000000
        'unpaired': {UNPAIRED},          # → True
    }, f)
```

**Convention**: YAML variables are UPPERCASE (`{BATCH}`, `{VERSION}`). Python/R variables are lowercase (`sample`, `catalog`). The YAML engine only resolves identifiers it knows — unknown names are left untouched for the language runtime.

**Important edge case**: for shell commands (`sh`, `bash`), an unresolved `{UPPERCASE_VAR}` is treated as an error (catches typos like `{VESRION}`). For Python and R, this check is **disabled** because `{NAME}` is valid syntax in both languages — a set literal in Python, a code block in R. A typo like `{VESRION}` in a Python step will not be caught by the YAML engine; it will produce a `NameError` at runtime. To mitigate this, assign YAML vars to lowercase Python/R variables at the top of the command, then use the lowercase names throughout.

### Variables in commands

YAML commands have access to these runtime environment variables (set by the worker):

| Variable | Source | Description |
|---|---|---|
| `${CPU}` | Worker | Number of CPUs available for this task |
| `${THREADS}` | Worker | Same as CPU (alias for Snakemake compatibility) |
| `${MEM}` | Worker | Available memory in GB |

These are container environment variables. They require a shell to expand (`language: sh` or `bash`). In `language: python`, access them via `os.environ['CPU']`. In `language: r`, use `Sys.getenv('CPU')`. In `language: none`, they are still set but not expanded — the binary must read them from the environment itself.

## Top-level options

```yaml
name: template-name
version: 1.0.0
description: What this template does
tag: "{params.input_dir}"              # workflow name suffix
language: sh                            # default: sh (shell). Options: sh, bash, python, r, none
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

A module may declare **variables** it expects — in the `cond:` construct, the condition variable (e.g. `paired`) must be provided by the importing template as a step field.

A module does **not** define: `inputs`, `grouped`, `resource` — those come from the importing template.

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

The template provides what the module doesn't: inputs, resource, paired flag, grouped mode. Any field from the module can be overridden.

### Merge semantics

| Source | Fields |
|---|---|
| **From module** | `name`, `command`, `container`/`conda`, `outputs`, `task_spec` |
| **From template** (always) | `inputs`, `grouped`, `resource`, `paired` |
| **From template** (override) | `container`, `task_spec`, `worker_pool`, `name` |

## Comparison with Python DSL

| Aspect | YAML | Python DSL |
|---|---|---|
| Target user | AI agents, bioinformaticians | Developers |
| Complexity | Linear pipelines, product sweeps | Any topology |
| Conditionals | `cond:` on any param (boolean, enum, string) | Full Python (`cond()`, `if/else`) |
| Loops | `iterate:` (single, product, range, list, lines) | Any Python loop |
| Modules | YAML files + Python modules | Python functions |
| Execution | Via CLI (`scitq template run`) or direct (`python -m scitq2.yaml_runner`) | Direct (`python template.py`) or via CLI |
| Server dependency | Always (needs server for workflow creation) | Always (needs server for workflow creation) |

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

## Advanced features

### Module-level vars

Modules can define their own `vars:` block with defaults and conditionals. This allows modules to encapsulate their internal logic — the template only needs to pass a minimal set of parameters.

```yaml
# biomscope_align.yaml — module knows its own catalog logic
vars:
  ORAL: "false"
  CATALOG:
    cond: ORAL
    true: "hs84oral"
    false: "igc2"
  CATALOG_LABEL:
    cond: ORAL
    true: "oral_catalog"
    false: "catalog"
resource: "{RESOURCE_ROOT}/{CATALOG}.tgz|untar"
command: |
  bowtie2 ... -x /resource/{CATALOG}/{CATALOG} ...
```

The template just passes `ORAL: "true"` and the module figures out the rest:

```yaml
  - module: biomscope_align.yaml
    inputs: seqtk.fastqs

  - module: biomscope_align.yaml
    when: "{params.oral_catalog}"
    name: oralbiomscope
    inputs: seqtk.fastqs
    vars:
      ORAL: "true"
```

Merge order: **workflow vars → module vars → step vars**. Each level can reference and override the previous. Vars are resolved sequentially within each level, so later vars can use `cond:` referencing earlier vars.

### Nested imports

A private module can import a public module and extend it with installation-specific knowledge (e.g. resource locations):

```yaml
# workflow/modules/metaphlan.yaml (private — knows where resources live)
import: genetic/metaphlan
vars:
  METAPHLAN_RESOURCE:
    cond: METAPHLAN_VERSION
    "4.0": "{RESOURCE_ROOT}/metaphlan4.0.5.tgz|untar"
    "4.1": "{RESOURCE_ROOT}/metaphlan/metaphlan4.1.tgz|untar"
    "4.2": "{RESOURCE_ROOT}/metaphlan/metaphlan4.2.tgz|untar"
resource: "{METAPHLAN_RESOURCE}"
```

The public module (`genetic/metaphlan`) defines the command, container, and version-specific flags. The private module adds which resources to use for this particular installation. The template just says:

```yaml
  - module: metaphlan.yaml
    vars:
      METAPHLAN_VERSION: "{params.metaphlan}"
```

This separates tool knowledge (public, shareable) from deployment knowledge (private, installation-specific).

### Strict variable resolution

Unresolved YAML variables in commands cause a hard error at compile time:

```
ValueError: Step 'biomscope': unresolved YAML variables in command: ['MISSING_VAR']
```

This catches typos and missing configuration before any task is created.

To declare an optional variable with a fallback, use the default syntax:

```yaml
command: |
  tool --option {SOME_VAR:default_value}
```

If `SOME_VAR` is not defined in any vars scope, `default_value` is used instead. Without a default, an unresolved var is an error.

Note: this only applies to YAML variables (`{VAR}`). Shell variables (`${VAR}`) are left for the shell and never checked at compile time.

### Variable interpolation rules summary

| Syntax | Resolution | When |
|---|---|---|
| `{VAR}` | YAML variable — resolved at compile time | Must be defined in workflow/module/step vars |
| `{VAR:default}` | YAML variable with fallback | Uses default if VAR not defined |
| `{params.name}` | Parameter reference | From template params |
| `{params.name\|filter}` | Parameter with filter | `\|name`, `\|reads`, `\|is_paired`, etc. |
| `${VAR}` | Shell variable — left for runtime | `CPU`, `THREADS`, `MEM`, etc. |

## Template integration

YAML templates are unified with Python DSL templates under the existing `scitq template` CLI commands. The server detects the type from the file extension.

### Two modes of execution

YAML and Python templates support two usage modes:

**Standard users** — upload a template to the server, then run it by name:

```sh
scitq template upload --path biomscope.yaml
scitq template run --name biomscope --param bioproject=PRJEB6070,location=azure.primary:swedencentral
```

**Power users** — run a local file directly without uploading:

```sh
scitq template run --path biomscope.yaml --param bioproject=PRJEB6070,location=azure.primary:swedencentral
```

The distinction is `--name` (server-side lookup) vs `--path` (local file). With `--path`, the CLI reads the file and sends its content to the server for transient execution — the file is not stored permanently.

### Upload

```sh
# Upload a YAML template
scitq template upload --path biomscope.yaml

# Upload a Python template (unchanged)
scitq template upload --path biomscope.py
```

The server detects `.yaml`/`.yml` vs `.py`:
- **Python**: runs `python script.py --metadata` and `--params` to extract name/version/params
- **YAML**: reads `name:`, `version:`, `description:`, `params:` directly from the YAML (no execution needed)

Both are stored in `{script_root}/` and in the `template` DB table with a `type` field (`yaml` or `python`).

### Run

```sh
# Run an uploaded template by name (type-agnostic)
scitq template run --name biomscope --param bioproject=PRJEB6070,location=azure.primary:swedencentral

# Run a local file directly (power user, file is sent to server transiently)
scitq template run --path ./biomscope.yaml --param bioproject=PRJEB6070,location=azure.primary:swedencentral

# Both modes support --dry-run
scitq template run --path ./biomscope.yaml --param ... --dry-run
```

With `--name`, the server looks up the template in its DB and runs it from `{script_root}/`.
With `--path`, the CLI reads the local file and sends the content to the server via a `RunTransientTemplate` RPC. The server writes it to a temp file, executes it, and deletes the temp file.

### Download

```sh
# Download template source code
scitq template download --name biomscope --version 1.0.2 --output biomscope.yaml

# Or by ID
scitq template download --id 42 --output biomscope.yaml
```

New `DownloadTemplate` RPC that returns the script content from `{script_root}/`.

### List and detail

```sh
# Lists both YAML and Python templates
scitq template list
scitq template detail --name biomscope
```

The UI shows both types in the same list. The template type (`yaml`/`python`) can be shown as a badge but doesn't change the user interaction.

### Module upload

YAML modules are uploaded separately from templates:

```sh
scitq module upload --path modules/biomscope_align.yaml
scitq module list
scitq module download --name biomscope_align.yaml --output modules/biomscope_align.yaml
```

Modules are stored in `{script_root}/modules/` and are available to all YAML templates on the server.

### Server changes for YAML templates

In `scriptRunner()` (`workflow_templates.go`):

```go
func (s *taskQueueServer) scriptRunner(ctx context.Context, scriptPath string, mode string, ...) {
    var args []string

    if strings.HasSuffix(scriptPath, ".yaml") || strings.HasSuffix(scriptPath, ".yml") {
        // YAML template: run via yaml_runner module
        switch mode {
        case "metadata":
            // Read directly from YAML — no execution needed
            return readYAMLMetadata(scriptPath)
        case "params":
            args = []string{"-m", "scitq2.yaml_runner", scriptPath, "--params"}
        case "run":
            args = []string{"-m", "scitq2.yaml_runner", scriptPath, "--values", paramJSON}
        }
    } else {
        // Python template: run script directly (existing logic)
        switch mode {
        case "metadata":
            args = []string{scriptPath, "--metadata"}
        case "params":
            args = []string{scriptPath, "--params"}
        case "run":
            args = []string{scriptPath, "--values", paramJSON}
        }
    }

    cmd := exec.CommandContext(ctx, venvPython, args...)
    // ... rest unchanged
}
```

For metadata extraction from YAML, no Python execution is needed — just parse the YAML file:

```go
func readYAMLMetadata(path string) (string, string, int, error) {
    // Read YAML, extract name/version/description, return as JSON
    data, _ := os.ReadFile(path)
    // Parse YAML header fields...
    metadata := map[string]string{"name": name, "version": version, "description": desc}
    json, _ := json.Marshal(metadata)
    return string(json), "", 0, nil
}
```

## Implementation plan

1. **Server**: detect `.yaml` extension in `scriptRunner`, dispatch to `yaml_runner`
2. **Server**: `readYAMLMetadata()` for fast metadata extraction without execution
3. **Server**: `{script_root}/modules/` directory for YAML modules
4. **CLI**: `scitq template download` command (new)
5. **CLI**: `scitq module upload/list/download` commands (new)
6. **Proto**: `DownloadTemplate` RPC (returns script content)
7. **DB**: add `type` column to template table (`yaml`/`python`)
8. **UI**: show both types in template list
