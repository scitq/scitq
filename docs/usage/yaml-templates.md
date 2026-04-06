# YAML templates

YAML templates are a declarative alternative to the [Python DSL](dsl.md) for defining scitq workflows. They are designed to be readable, modular, and AI-friendly — no programming knowledge is required.

## Hello world

```yaml
name: helloworld
version: 1.0.0
description: Minimal workflow example

worker_pool:
  provider: local.local
  region: local

steps:
  - name: hello
    container: alpine
    command: echo 'Hello world'
```

YAML templates are plain `.yaml` files. You can run them locally or upload them to a scitq server.

### Running locally

```sh
export SCITQ_TOKEN=$(scitq login)
export SCITQ_SSL_CERTIFICATE=$(scitq cert)
source /path/to/my/venv/bin/activate
python -m scitq2.yaml_runner my_template.yaml --values '{}'
```

Available flags:

- `--values '{"key": "val"}'` : provide parameters as JSON,
- `--dry-run` : create the workflow, verify it compiles correctly, then delete it,
- `--no-recruiters` : create the workflow without deploying any workers,
- `--verbose` : print step decisions (which steps are built/skipped) to stderr,
- `--params` : print the parameter schema as JSON.

### Uploading to the server

```sh
scitq template upload --path my_template.yaml
scitq template run --name helloworld --param 'key=value'
```

## Structure of a YAML template

A YAML template has these top-level sections:

```yaml
name: my_workflow          # Required: unique template name
version: 1.0.0             # Required: semantic version
description: What it does   # Required: human-readable description
tag: "{params.project}"     # Optional: tag for each workflow instance

params:     # Parameter declarations
  ...
vars:       # Workflow-level variables
  ...
iterate:    # Sample/iteration source
  ...
worker_pool: # Default worker pool for all steps
  ...
workspace:  # Provider:region for workspace storage
  ...
language: sh  # Default shell (sh, bash, python, none)
retry: 2      # Default retry count for all steps

steps:      # The pipeline steps
  - ...
```

## Params

All workflow parameters must be declared. This makes templates self-documenting and enables the UI/CLI to present parameter forms.

```yaml
params:
  input_dir:
    type: string
    required: true
    help: Input data directory

  depth:
    type: enum
    choices: ["1x10M", "1x20M", "2x10M", "2x20M"]
    default: "2x20M"
    help: "Sequencing depth. 2x = paired, 1x = unpaired."

  max_workers:
    type: integer
    default: 10

  location:
    type: provider_region
    required: true

  paired:
    type: boolean
    default: true
```

Supported types: `string`, `integer`, `boolean`, `enum` (with `choices`), `provider_region`, `path`.

Parameters are referenced in the template as `{params.name}`. Optional parameters use the syntax `{params.name:default_value}` — the default is used when the parameter is not provided.

### Parameter dependencies (`requires`)

A parameter can require other parameters to have specific values:

```yaml
  nsat:
    type: boolean
    default: false
    requires:
      oral: true      # nsat=true requires oral=true
```

For enum params, `when:` specifies which value triggers the constraint:

```yaml
  metaphlan:
    type: enum
    choices: ["No", "4.0", "4.1"]
    requires:
      when: "4.0"
      legacy_db: true   # only when metaphlan="4.0"
```

If `when:` is omitted, the constraint triggers when the parameter is truthy. Validation runs after defaults are applied, catching contradictory defaults at launch time.

## A more realistic template

Here is a complete template that processes multiple samples through a three-step pipeline: quality control, alignment, and a final compilation step:

```yaml
name: simple_pipeline
version: 1.0.0
description: QC + alignment + compile

params:
  bioproject:
    type: string
    required: true
    help: URI to a folder with FASTQ files
  location:
    type: provider_region
    required: true
  max_workers:
    type: integer
    default: 5

iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  filter: "*.f*q.gz"

worker_pool:
  provider: "{params.location}"
  cpu: ">= 8"
  mem: ">= 30"
  max_recruited: "{params.max_workers}"

workspace: "{params.location}"
language: bash
retry: 2

steps:
  # Per-sample: quality control
  - import: genetic/fastp

  # Per-sample: alignment
  - name: align
    container: biocontainers/bowtie2:v2.5.4
    inputs: fastp.fastqs
    resource: "{RESOURCE_ROOT}/my_index.tgz|untar"
    command: |
      bowtie2 -x /resource/my_index/index \
        -U $(echo /input/*.fastq.gz | tr ' ' ',') \
        -S /output/${SAMPLE}.sam
    outputs:
      sam: "*.sam"
    task_spec:
      cpu: 8
      mem: 30

  # Fan-in: compile all samples
  - name: compile
    container: gmtscience/pandas
    inputs: align.sam
    grouped: true
    language: python
    command: |
      import os, glob
      files = glob.glob('/input/*.sam')
      print(f"Compiled {len(files)} samples")
    publish: "azure://results/{params.bioproject}/"
    worker_pool:
      cpu: ">= 4"
      max_recruited: 1
```

This template introduces the key concepts that differ from the hello world example. Let's walk through them.

### Iterators

The `iterate:` block defines what the template loops over. Each iteration produces one set of per-sample tasks.

```yaml
iterate:
  name: sample          # Iterator variable name (becomes {SAMPLE} in commands)
  source: uri           # Source type: uri, ena, sra, list, range
  uri: "{params.bioproject}"
  group_by: folder      # Group files by parent folder
  filter: "*.f*q.gz"    # Glob filter for files
```

The iterator automatically injects:
- `{SAMPLE}` — the current sample name (usable in commands),
- `{SAMPLE_COUNT}` — the total number of samples (usable in vars and worker_pool),
- `{SAMPLES}` — comma-separated list of all sample names.

Other sources:

```yaml
# Public data from ENA
iterate:
  name: sample
  source: ena
  identifier: "PRJEB6070"
  group_by: sample_accession
  filter:
    library_strategy: WGS

# Simple list
iterate:
  name: region
  source: list
  values: ["chr1", "chr2", "chr3"]

# Numeric range
iterate:
  name: batch
  source: range
  start: 1
  end: 100
  step: 10
```

### Conditional iterators

When the data source depends on a parameter, use `cond:`:

```yaml
iterate:
  name: sample
  cond: "{params.data_source}"
  uri:
    source: uri
    uri: "{params.bioproject}"
    group_by: folder
    filter: "*.f*q.gz"
  ena:
    source: ena
    identifier: "{params.bioproject}"
    group_by: sample_accession
```

### Three kinds of steps

Steps fall into three categories based on how they relate to the iteration loop:

1. **Per-sample steps** (default): run once per iteration. They have access to `{SAMPLE}` and the sample's input files.
2. **Fan-in steps** (`grouped: true`): run once after all iterations. They receive the combined outputs of all per-sample tasks.
3. **One-off steps** (`per_sample: false`): run once before the iteration loop (e.g. index building).

### Inputs and dependencies

Steps declare their inputs using dot notation: `step_name.output_name`.

```yaml
steps:
  - import: genetic/fastp
    # No inputs: first step gets sample files automatically

  - name: align
    inputs: fastp.fastqs      # Gets the "fastqs" output from the fastp step

  - name: compile
    inputs: align.sam          # Gets the "sam" output from the align step
    grouped: true              # Collects from ALL samples
```

Dependencies are automatic: if step B declares `inputs: A.output`, step B depends on step A. For fan-in steps, the dependency is on **all** tasks from the referenced step.

Inputs can also be **raw URIs** — any string containing `://` is passed through as-is:

```yaml
  - name: process
    inputs: "s3://bucket/data/{SAMPLE}/"   # Raw URI, not a step reference
```

Multiple inputs are combined with a list (mixing step references and URIs is allowed):

```yaml
  - name: compile
    inputs:
      - align.sam
      - fastp.json
      - "azure://ref/metadata.tsv"
    grouped: true
```

### Outputs

Named outputs let downstream steps reference specific file patterns:

```yaml
  - name: fastp
    outputs:
      fastqs: "*.fastq.gz"
      json: "*.json"
```

Without named outputs, the step exposes its entire `/output/` directory.

### Worker pool

The worker pool defines what kind of machines to recruit:

```yaml
worker_pool:
  provider: "{params.location}"    # Cloud provider:region
  cpu: ">= 8"                     # CPU filter
  mem: ">= 30"                    # Memory (GB) filter
  disk: ">= 100"                  # Disk (GB) filter
  max_recruited: 10                # Maximum workers to recruit
  task_batches: 2                  # How many batches of tasks per worker
```

A step can override the workflow-level pool:

```yaml
  - name: compile
    worker_pool:
      cpu: ">= 4"
      max_recruited: 1
    grouped: true
```

### Resources

Resources are read-only data files downloaded to `/resource/` before the task runs:

```yaml
  - name: align
    resource: "azure://ref/my_index.tgz|untar"
```

The `|untar` suffix tells the worker to extract after download. `|gunzip` is also supported. Multiple resources use a list:

```yaml
    resource:
      - "azure://ref/genes.tsv.gz"
      - "azure://ref/species.tsv.gz"
```

Resources are validated before the workflow starts — if a resource doesn't exist, the workflow is aborted with a clear error.

### The `{RESOURCE_ROOT}` variable

When your provider has a local copy of reference data (configured via `local_resources` in the server config), `{RESOURCE_ROOT}` resolves to the local path automatically. This avoids costly cross-region data transfer:

```yaml
    resource: "{RESOURCE_ROOT}/my_index.tgz|untar"
```

If no local resource root is configured, `{RESOURCE_ROOT}` is empty and the resource path is used as-is.

## Variable interpolation

YAML templates use two kinds of variables:

| Syntax | Resolved by | Example |
|---|---|---|
| `{VAR}` | YAML engine at compile time | `{SAMPLE}`, `{params.depth}`, `{SEED}` |
| `${VAR}` | Shell at runtime | `${CPU}`, `${MEM}`, `${THREADS}` |

This distinction is critical: `{SAMPLE}` is replaced before the task is submitted, while `${CPU}` is evaluated when the task runs on a worker.

### Typed interpolation for Python and R

When the step language is `python` or `r`, `{VAR}` resolves to a **typed literal** appropriate for the language:

| Value | Shell | Python | R |
|---|---|---|---|
| `"true"` | `true` | `True` | `TRUE` |
| `"false"` | `false` | `False` | `FALSE` |
| `"42"` | `42` | `42` | `42` |
| `"3.14"` | `3.14` | `3.14` | `3.14` |
| `"hello"` | `hello` | `'hello'` | `'hello'` |

This means you can use `{VAR}` directly in Python/R code without manual type conversion:

```yaml
- name: compile
  language: python
  command: |
    sample_list = {SAMPLES}.split(',')     # 'S001,S002,...'.split(',')
    catalogs = {CATALOGS}.split(',')

    if {COMPUTE_NSAT}:                     # if True: / if False:
        print('Computing NSAT')

    with open('params.json', 'wt') as f:
        json.dump({
            'batch': {BATCH},              # 'my_batch'
            'depth': {DEPTH},              # 20000000
            'unpaired': {UNPAIRED},        # True
        }, f)
```

**Convention**: YAML variables are UPPERCASE (`{BATCH}`, `{SAMPLES}`). Python/R variables are lowercase (`sample`, `catalog`). The YAML engine only resolves identifiers it knows — unknown names are left untouched for the language runtime.

**Important**: for shell commands (`sh`, `bash`), an unresolved `{UPPERCASE_VAR}` in the command is treated as an error (likely a typo). For Python and R, this check is **disabled** — `{TOTO}` is valid syntax in both languages (a set literal in Python, a code block in R), and those languages have their own error checking at runtime. This means a typo like `{VESRION}` in a Python step will not be caught by the YAML engine; it will produce a `NameError` at runtime instead. To avoid this, follow the convention: assign YAML vars to lowercase Python/R variables at the top of the command, then use the lowercase names throughout.

For shell commands, no typing is applied — `{VAR}` resolves to the raw string value as before.

### Workflow-level vars

Variables declared under `vars:` are available to all steps:

```yaml
vars:
  SEED: "42"
  DEPTH: "{params.depth|reads}"
  VERSION: "1.0.0"
```

### Filters

Variable references support filters with the `|` syntax:

| Filter | Effect | Example |
|---|---|---|
| `\|reads` | Extract read count from depth string | `"2x20M"` → `"20000000"` |
| `\|total_reads` | Total reads (depth × multiplier) | `"2x20M"` → `"40000000"` |
| `\|is_paired` | `"true"` if depth starts with `2x` | `"2x20M"` → `"true"` |
| `\|name` | Basename without extension | `"/path/file.txt"` → `"file"` |
| `\|basename` | Basename with extension | `"/path/file.txt"` → `"file.txt"` |
| `\|dir` | Parent directory | `"/path/file.txt"` → `"/path"` |
| `\|int` | Convert to integer | `"42"` → `"42"` |
| `\|lower` | Lowercase | `"Hello"` → `"hello"` |
| `\|upper` | Uppercase | `"Hello"` → `"HELLO"` |
| `\|format=FMT` | Python %-style formatting | `{IDX\|format=%04d}` → `"0042"` |

### Default values

Use `{VAR:default}` for optional variables:

```yaml
filter: "{params.filter:*}.f*q.gz"   # defaults to *.f*q.gz if filter not provided
```

## Conditional logic (`cond:`)

The `cond:` construct selects a value based on a condition — it replaces if/else branching:

```yaml
command:
  cond: PAIRED
  true: |
    bowtie2 --in1 /input/*.1.fastq.gz --in2 /input/*.2.fastq.gz ...
  false: |
    bowtie2 -U /input/*.fastq.gz ...
```

`cond:` works on any field: `command`, `container`, `resource`, `inputs`, individual `vars`, and even the `iterate:` block itself. The condition can reference parameters, iterator variables, or step vars.

### Truthy/falsy values

The following are considered falsy: empty string, `"false"`, `"False"`, `"No"`, `"no"`, `"none"`, `"None"`.

Everything else is truthy. This means enum values like `"4.0"` or `"both"` are truthy, which lets you use them naturally as conditions:

```yaml
  - name: metaphlan
    when: "{params.metaphlan}"      # Skipped when metaphlan="No" (falsy)
    container:
      cond: METAPHLAN_VERSION
      "4.0": "gmtscience/metaphlan4:4.0.6.1"
      "4.1": "gmtscience/metaphlan4:4.1"
```

## Conditional steps (`when:`)

The `when:` field skips a step entirely when its value is falsy:

```yaml
  - module: oral_align.yaml
    when: "{params.oral}"           # Only built when oral=true
    inputs: seqtk.fastqs
```

This is cleaner than wrapping steps in conditionals — the step simply doesn't exist when the condition is false.

## Modules

Modules are reusable step definitions. There are two kinds:

### Public modules (`import:`)

Shipped with the scitq2 Python package. They encapsulate common bioinformatics tools:

```yaml
steps:
  - import: genetic/fastp
  - import: genetic/bowtie2_host_removal
    inputs: fastp.fastqs
  - import: genetic/seqtk_sample
    inputs: humanfilter.fastqs
```

Public modules provide sensible defaults for container, command, outputs, and task_spec. You can override any field:

```yaml
  - import: genetic/fastp
    task_spec:
      cpu: 8        # Override the default CPU allocation
```

### Private modules (`module:`)

Project-specific modules stored on the server. Upload and manage them with the CLI:

```sh
scitq module upload --path modules/my_alignment.yaml
scitq module list
scitq module download --name my_alignment.yaml -o local_copy.yaml
```

Reference them in your template:

```yaml
  - module: my_alignment.yaml
    inputs: fastp.fastqs
    vars:
      CATALOG: "igc2"
```

A private module is a YAML file with the same fields as an inline step (name, container, command, outputs, etc.). It can itself import a public module to add deployment-specific knowledge:

```yaml
# modules/my_metaphlan.yaml
import: genetic/metaphlan      # Inherit from public module
vars:
  METAPHLAN_RESOURCE:           # Add local resource paths
    cond: METAPHLAN_VERSION
    "4.1": "{RESOURCE_ROOT}/metaphlan/metaphlan4.1.tgz|untar"
resource: "{METAPHLAN_RESOURCE}"
```

### Module-level vars

Modules can declare their own vars, including `cond:` blocks. These are merged with step-level vars (step vars override module vars):

```yaml
# modules/biomscope_align.yaml
name: biomscope
vars:
  ORAL: "false"
  CATALOG:
    cond: ORAL
    true: "hs84oral"
    false: "igc2"
resource: "{RESOURCE_ROOT}/{CATALOG}.tgz|untar"
command: |
  bowtie2 -x /resource/{CATALOG}/{CATALOG} ...
```

The calling template can override `ORAL` to switch catalogs:

```yaml
  - module: biomscope_align.yaml
    vars:
      ORAL: "true"    # Now uses hs84oral instead of igc2
```

## Dependency behavior

By default, a task only runs when **all** its prerequisites have succeeded. If any prerequisite fails (even after exhausting retries), the dependent task stays blocked.

### `accept_failure`

For aggregation steps that should run with partial results, use `accept_failure: true`:

```yaml
  - name: compile
    inputs: analysis.output
    grouped: true
    accept_failure: true    # Run even if some samples failed
```

A prerequisite must be **terminally** failed to satisfy `accept_failure` — meaning all retries exhausted (`retry = 0`). A task that fails but still has retries left will be retried first.

### `skip_if_exists`

Tasks with `skip_if_exists: true` are automatically marked as succeeded if their output path already contains files. This enables resumable workflows:

```yaml
# Set at workflow level
skip_if_exists: true

# Or per step
steps:
  - name: expensive_step
    skip_if_exists: true
    ...
```

When a task has a `publish` path, the skip check uses the **publish path** (not the workspace path). Since failed tasks never publish (see [Publishing results](#publishing-results)), this guarantees that files at the publish path represent genuinely successful output.

The output path supports **glob patterns** for more precise checks. Instead of skipping whenever any file exists, you can require specific file types:

```yaml
  - name: align
    skip_if_exists: true
    outputs:
      sam: "*.sam"
    publish: "azure://results/project/sample_A/*.sam"
```

This only skips if `.sam` files exist at the publish path — partial output (e.g. log files from a failed run) won't trigger a false skip.

## Publishing results

The `publish` field copies task outputs to permanent storage **on success only**. If a task fails, its output is uploaded to the workspace path (for debugging) but never to the publish path. This ensures that `skip_if_exists` checks against the publish path are reliable — if files exist there, the task genuinely succeeded.

```yaml
  - name: compile
    grouped: true
    publish: "azure://results/my_project/"
```

Or use `true` with a workflow-level `publish_root`:

```yaml
publish_root: "azure://results/my_project"

steps:
  - name: compile
    publish: true    # Publishes to azure://results/my_project/compile/
```

## Language

The `language` field controls how the command is executed:

| Value | Behavior | Typed interpolation |
|---|---|---|
| `sh` (default) | BusyBox sh — validated with `sh -n` | No |
| `bash` | Full bash — supports process substitution `>(...)`, arrays, etc. | No |
| `python` | Command is a Python script | Yes (`True`/`False`, quoted strings) |
| `r` | Command is an R script (via `Rscript`) | Yes (`TRUE`/`FALSE`, quoted strings) |
| `none` | Command is passed as-is (no wrapping) | No |

Set at workflow level or per step:

```yaml
language: bash     # Workflow default

steps:
  - name: compile
    language: python
    command: |
      import pandas as pd
      ...
```

## Comparison with Python DSL

| Feature | Python DSL | YAML template |
|---|---|---|
| Syntax | Python code | Declarative YAML |
| Iteration | `for sample in samples:` | `iterate:` block |
| Conditionals | `if`/`else`, `cond()` | `cond:` blocks, `when:` |
| Modules | Python imports | `import:` / `module:` |
| Fan-in | `step.grouped()` | `grouped: true` |
| Variables | Python variables + `fr""` strings | `vars:` + `{VAR}` / `${VAR}` |
| Arbitrary logic | Full Python | Limited to `cond:` branching |

The Python DSL is more powerful for complex logic, but YAML templates are simpler to write, review, and maintain for standard pipelines. Both produce identical workflows — you can start with YAML and switch to Python if you outgrow it.

## Opportunistic reuse

Many workflows are re-run on overlapping data batches. **Opportunistic reuse** lets scitq skip tasks that have already been executed with identical computation and inputs, reusing their output across workflows.

Unlike `skip_if_exists` (which checks a single output path), reuse is content-addressed: it matches on *what the task does* and *what it processes*, not on *where the output lives*.

### Enabling reuse

Reuse is controlled at **workflow level**, not per step:

```yaml
params:
  opportunistic:
    type: boolean
    default: false
    help: Reuse results from previous identical runs
  untrusted:
    type: string
    default: ""
    help: Comma-separated step names to force re-execute

opportunistic: "{params.opportunistic}"
untrusted: "{params.untrusted}"
```

Running with reuse:

```sh
# Normal run — everything executes
scitq template run my_pipeline input_dir=azure://data/batch42 location=azure.swedencentral

# With reuse — skip tasks already done on identical inputs
scitq template run my_pipeline input_dir=azure://data/batch42 location=azure.swedencentral opportunistic=true

# Reuse, but force QC to re-run (e.g. container uses :latest which was updated)
scitq template run my_pipeline input_dir=azure://data/batch42 location=azure.swedencentral opportunistic=true untrusted=qc
```

### How it works

Each task is identified by a **reuse key** — a SHA-256 hash of:

- **Task fingerprint**: command, shell, container (including tag), container_options, sorted resources
- **Input identities**: for external inputs, the URI itself; for internal inputs (outputs of a previous step), the reuse key of the producing task

Two tasks with the same reuse key produce the same output. When a task with a reuse key is about to be assigned to a worker:

1. The server looks up the key in the reuse store
2. **Hit**: the task is instantly marked as succeeded with the cached output path — no worker needed
3. **Miss**: the task runs normally, and on success its output is stored for future reuse

### Trust chain

Internal inputs create a transitive trust chain. If step A produces output used by step B, then step B's reuse key incorporates step A's reuse key. If any step in the chain is untrusted or has no reuse key, downstream steps cannot be reused either.

### Untrusted steps

The `untrusted` parameter lists steps that must always re-execute. This is useful when:

- A step's container uses a mutable tag like `:latest` that was updated
- You want to re-validate a specific step
- A resource was updated in-place

Since breaking trust at one step breaks the entire downstream chain, typically there's at most one untrusted step per run.

### Lazy output verification

Reuse hits are database lookups (instant). If a cached output turns out to be missing (deleted workspace, cleaned storage), the downstream task will fail — at that point the stale entry is automatically invalidated and the prerequisite task re-runs.

### Reuse vs skip_if_exists

Both can be active simultaneously. The reuse check runs first (DB lookup, no I/O). If it misses, `skip_if_exists` can still skip based on output path presence. They are complementary:

| Feature | `skip_if_exists` | Opportunistic reuse |
|---|---|---|
| Scope | Single task, output path check | Cross-workflow, content-addressed |
| Check | File existence at output/publish path | Database key lookup |
| Speed | Requires I/O (listing remote files) | Instant (DB primary key) |
| Granularity | Per step | Workflow-level opt-in |
