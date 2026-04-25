# YAML templates

YAML templates are a declarative alternative to the [Python DSL](dsl.md) for defining scitq workflows. They are designed to be readable, modular, and AI-friendly — no programming knowledge is required.

## Hello world

```yaml
format: 2
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
format: 2                  # YAML engine format (default: 1 for backward compat)
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
format: 2
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
  fastqs: "*.f*q.gz"

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
  - import: genomics/fastp
    inputs: sample.fastqs

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
  fastqs: "*.f*q.gz"    # Named file group (referenced as inputs: sample.fastqs)
  match: "{params.filter:*}"  # Optional: filter which samples to include
```

The iterator automatically injects:
- `{SAMPLE}` — the current sample name (usable in commands),
- `{SAMPLE_COUNT}` — the total number of samples (usable in vars and worker_pool),
- `{SAMPLES}` — comma-separated list of all sample names.

#### Named file groups

Instead of a generic `filter:`, iterators declare **named file groups** — glob patterns that define what files each iteration provides. Steps reference them explicitly as `inputs: iterator_name.group_name`:

```yaml
iterate:
  name: sample
  source: uri
  uri: "{params.data_dir}"
  group_by: folder
  fastqs: "*.f*q.gz"        # named file group

steps:
  - name: fastp
    inputs: sample.fastqs    # explicit reference to the named group
```

Multiple named groups are supported:

```yaml
iterate:
  name: dataset
  source: uri
  uri: "{params.data_dir}"
  group_by: folder
  csvs: "*.csv"
  configs: "param.yaml"

steps:
  - name: train
    inputs: dataset.csvs
  - name: validate
    inputs: dataset.configs
```

The legacy `filter:` syntax is still supported for backward compatibility but named groups are preferred for clarity.

#### `match:` — filter iterations by name

The `match:` key filters which iterations to include, based on the sample/folder name pattern:

```yaml
iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  fastqs: "*.f*q.gz"
  match: "SRR123*"           # only process samples matching this pattern
```

This is separate from file selection (what files within each sample) and metadata filtering (ENA/SRA attributes). It works on all source types.

#### `where:` — metadata filter (ENA/SRA)

For ENA and SRA sources, `where:` filters iterations by metadata attributes:

```yaml
iterate:
  name: sample
  source: ena
  identifier: "PRJEB6070"
  group_by: sample_accession
  where:
    library_strategy: WGS
```

ENA and SRA are FASTQ databases — a `fastqs` file group is provided implicitly (`inputs: sample.fastqs` works without declaring it). For URI sources, you must declare named groups explicitly, or use `inputs: sample.files` for the default unnamed group.

#### Other sources

```yaml
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

When the data source depends on a parameter, use `cond:`. The `match:` key is inherited by all branches:

```yaml
iterate:
  name: sample
  match: "{params.filter:*}"
  cond: "{params.data_source}"
  uri:
    source: uri
    uri: "{params.bioproject}"
    group_by: folder
    fastqs: "*.f*q.gz"
  ena:
    source: ena
    identifier: "{params.bioproject}"
    group_by: sample_accession
    fastqs: "*.f*q.gz"
    where:
      library_strategy: WGS
  sra:
    source: sra
    identifier: "{params.bioproject}"
    group_by: sample_accession
    fastqs: "*.f*q.gz"
    where:
      library_strategy: WGS
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
  - import: genomics/fastp
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

A fan-in step can also consume the iterator's per-sample inputs directly, without a per-sample step in between. The runner expands `<itervar>.<group>` (e.g. `sample.fastqs`) on a `grouped: true` step to the union of that file group across every iteration:

```yaml
iterate:
  name: sample
  source: uri
  uri: "{params.input_uri}"
  group_by: folder
  fastqs: "*.f*q.gz"

steps:
  # No per-sample step here — the grouped step pulls the iterator's
  # fastqs directly across all samples.
  - import: metagenomics/simka
    inputs: sample.fastqs
    grouped: true
```

This is the right pattern when the only thing you want to do with the per-sample fastqs is feed them to a many-to-one tool (simka, k-mer comparisons, joint variant calling, etc.) — no need for a no-op pass-through step purely to register an upstream output for the resolver. (See note: until this support landed, an explicit `name: stage` step that did `mv /input/*.fastq.gz /output/` was required between the iterator and the grouped step.)

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
  prefetch: 1                      # Tasks to download in advance per worker
```

The `prefetch` setting controls how many tasks a worker prepares in advance while executing its current tasks. This is key for performance: without prefetch, there's idle time between tasks while inputs are downloaded. With `prefetch: 1`, the next task's inputs are downloaded during the current task's execution. Higher values help for very fast tasks with large inputs. See the [CLI recruiter documentation](cli.md#recruiter-create) for details on tuning prefetch.

A step can override the workflow-level pool:

```yaml
  - name: compile
    worker_pool:
      cpu: ">= 4"
      max_recruited: 1
    grouped: true
```

### Workspace

The `workspace:` field specifies where task outputs are stored between steps. It takes a `provider:region` value (e.g. `azure.primary:swedencentral` or `openstack.ovh:GRA11`), typically matching the worker pool so data stays close to compute.

```yaml
workspace: "{params.location}"
```

Behind the scenes, the server resolves the provider:region pair into a concrete storage URI via the `local_workspaces` configuration in `scitq.yaml`. For example, `azure.primary:swedencentral` might map to `aznorth://workspace`. Task outputs are then stored at `{workspace_root}/{workflow_name}/{task_name}/`. This indirection means templates are portable — the same template works across providers without hardcoding storage paths.

When a task has no `publish` path, its `/output/` directory is uploaded to the workspace. Downstream tasks download their inputs from there.

When `publish` is defined, successful tasks upload to the publish path instead of the workspace. However, **failed tasks always upload to the workspace** (never to publish), so their output can be inspected for debugging.

If omitted, task outputs are only stored locally on the worker and lost when it is destroyed. Multi-step workflows need either `workspace` or `publish` on every step so that downstream tasks can find their inputs. Using `workspace` is the standard approach; relying on `publish` alone works but is not recommended (publish is meant for final results, not intermediate data).

### Run strategy

The optional `run_strategy:` field at the top level of the workflow tells the server how to schedule tasks across workers. It accepts either a single-letter DB code (`B`, `T`, `D`, `Z`) or a friendly word (`batch`, `thread`, `debug`, `suspended`). Default when omitted: `batch`.

```yaml
name: my-workflow
version: 1.0.0
run_strategy: thread     # or "T", or "batch", or "B", or omitted
```

| value | meaning |
|---|---|
| `batch` (`B`) | **Default.** Workers pick up tasks step-by-step: one step's tasks complete on a worker pool, outputs round-trip through the workspace, then the next step's tasks can land on any free worker. |
| `thread` (`T`) | Sticky scheduling: a thread of related tasks (typically per-sample chains, optionally followed by a co-located grouped step) is pinned to one worker, and the workspace round-trip between sticky tasks is skipped. *Currently a partially-implemented feature: the field is plumbed through to the database, but the sticky scheduling and worker-side I/O short-circuit logic still needs to ship before `T` behaves differently from `B` at runtime. See `specs/sticky_thread_run_strategy.md` for the design.* |
| `debug` (`D`) | Caps `maximum_workers` to 1 and starts the workflow paused — useful for stepping through a template by hand. |
| `suspended` (`Z`) | Workflow is created but no tasks are dispatched. Useful when you want to inspect/edit before kicking off. |

For per-sample workflows that end with a many-to-one fan-in (e.g. simka, joint variant calling) and where the per-sample data is large, `run_strategy: thread` is the eventual escape hatch from the workspace round-trip — once the server-side support lands. Today, set it if you want the workflow record to advertise the intent and pick up the optimisation automatically when the server upgrades.

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

**`{RESOURCE_ROOT}` must resolve to a real value whenever a template uses it.** If the template contains `{RESOURCE_ROOT}` anywhere (in `resource:`, `publish:`, `command:`, `vars:`, or any nested field) and the runner cannot produce a value — no `workspace:`, workspace doesn't resolve to a `provider:region` pair, no `local_resources` entry for that provider, or the server lookup fails — the run is **rejected before any workflow is created**, with the specific reason printed. Silent expansion to an empty string is no longer supported: it used to produce malformed URIs like `/meteor2/hs_10_4_gut/` that only surfaced as obscure fetch errors at worker runtime.

If the template does not reference `{RESOURCE_ROOT}` at all, resolution is skipped silently regardless of configuration.

Typical prerequisites to use `{RESOURCE_ROOT}`:

1. The template declares `workspace: "{params.location}"` (or another expression that evaluates to `provider:region`).
2. The server's `scitq.yaml` has a `local_resources` entry for the selected `provider:region`.
3. The running user's session has permission to call the `get_resource_root` RPC (standard user sessions qualify).

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

Modules are reusable step definitions. scitq keeps them in a **server-side versioned library** that is shared between bundled modules (shipped with scitq) and user-uploaded modules — see [Module library reference](../reference/module-library.md) for the full surface.

Reference a module from a step with `import:`:

```yaml
steps:
  - import: genomics/fastp
  - import: metagenomics/bowtie2_host_removal
    inputs: fastp.fastqs
  - import: genomics/seqtk_sample
    inputs: humanfilter.fastqs
```

Modules resolve in this order:

1. Server library, at `path` (highest version) or `path@version` if pinned.
2. Installed `scitq2_modules` Python package (fallback for offline/dev runs).

### Pinning a version

If you want reproducibility or need to pin a specific release, append `@version`:

```yaml
steps:
  - import: genomics/fastp@1.0.0
  - import: metagenomics/bowtie2_host_removal@latest   # same as no @
```

`latest` is a magic alias for "highest-ordered version in the library at the moment of resolution"; every actual template run records the concrete version it resolved in `template_run.module_pins` so a replay is reproducible even after a later library upgrade.

### Overriding fields

Module fields can be overridden at the call site. Any field in the `import:` block shadows the module's:

```yaml
  - import: genomics/fastp
    task_spec:
      cpu: 8        # Override the module's default CPU allocation
```

### Managing modules from the CLI

```sh
# Upload a project-specific module. --as sets the server-side path;
# it defaults to the filename without extension if omitted.
scitq module upload --path modules/my_alignment.yaml --as internal/my_alignment

# List and inspect
scitq module list
scitq module list --tree                       # grouped by folder
scitq module list --versions internal/my_alignment
scitq module origin internal/my_alignment@1.0.0

# Download
scitq module download --name internal/my_alignment -o local_copy.yaml

# Admin: seed/refresh bundled modules from the installed scitq2 package
scitq module upgrade                           # dry-run
scitq module upgrade --apply                   # commit

# Admin: fork a bundled module to make a site-specific variant
scitq module fork genomics/fastp@1.0.0 --new-version 1.0.0-site
```

### Writing a private module

A module file is a YAML file with the same fields as an inline step (name, container, command, outputs, task_spec, vars, …) plus a required `version:` field. A module can itself import another module to add deployment-specific knowledge:

```yaml
# modules/my_metaphlan.yaml
name: my_metaphlan
version: "1.0.0"
import: metagenomics/metaphlan      # Inherit from a library module
vars:
  METAPHLAN_RESOURCE:           # Add local resource paths
    cond: METAPHLAN_VERSION
    "4.1": "{RESOURCE_ROOT}/metaphlan/metaphlan4.1.tgz|untar"
resource: "{METAPHLAN_RESOURCE}"
```

Once uploaded:

```sh
scitq module upload --path modules/my_metaphlan.yaml --as internal/my_metaphlan
```

reference it the same way as any library module:

```yaml
  - import: internal/my_metaphlan
    inputs: fastp.fastqs
    vars:
      METAPHLAN_VERSION: "4.1"
```

> The legacy `module:` keyword (pre-library) still resolves as a synonym for `import:`, so existing templates keep working unchanged.

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

Prerequisites are usually wired implicitly via `inputs:` — referencing a previous step's output creates a data dependency AND an ordering dependency in one move. When no data flows but you still need ordering (e.g. a setup step that publishes into the resource tree for a later step to read via `resource:`), use `depends:`.

### `depends:` — ordering without data flow

`depends:` takes one step name or a list of step names. Tasks in the declaring step won't start until every task of the listed step(s) has succeeded — no file contents are fetched, only the ordering constraint is enforced.

```yaml
steps:
  - import: metagenomics/meteor2_catalog          # one-off setup: downloads a catalog,
                                              # publishes to {RESOURCE_ROOT}/meteor2/<name>/

  - import: metagenomics/meteor2
    inputs: fastp.fastqs                     # data dep on the upstream QC step
    depends: meteor2_catalog                 # pure ordering dep: don't start the
                                              # per-sample profiler until the catalog
                                              # is fully uploaded
```

Typical pattern for a **setup + compute** pair:
- Setup step (`per_sample: false`, `skip_if_exists: true`, `publish: {RESOURCE_ROOT}/...`) runs once and leaves artefacts in the workspace. `skip_if_exists` makes it a no-op on reruns, so the expensive download happens at most once per workspace.
- Compute step reads those artefacts via `resource:` and declares `depends: <setup_step_name>` so it waits until the upload finishes.

The step name you pass to `depends:` is whatever the target step's `name:` field resolves to — for a bundled module that's the module's own `name:` value. Referenced steps must appear **before** the declaring step in the `steps:` list, so they're built first.

### `requires:` — companion modules a module pulls in

Module authors can declare companion modules that should always accompany theirs. `requires:` is a **module-level** field (can also be set on an inline step). When a template imports a module that declares `requires:`:

- Every required module not already explicitly imported by the template is auto-injected as a `- import: <path>` step ahead of the importing step.
- The required module's step name is auto-added to the importing step's `depends:` list.
- `requires:` chains transitively — a required module's own `requires:` are resolved the same way, with injection order guaranteeing prerequisites precede their consumers.

The practical effect: a template author writes the compute step once and everything else falls into place.

```yaml
# scitq2_modules/yaml/metagenomics/meteor2.yaml  (library module)
name: meteor2
version: "1.0.0"
requires:
  - metagenomics/meteor2_catalog          # pull in the companion setup module
resource: "{RESOURCE_ROOT}/meteor2/{params.meteor2_catalog}/"
command: …
```

A template that wants meteor2 profiling therefore only needs:

```yaml
steps:
  - import: genomics/fastp
    inputs: sample.fastqs
  - import: metagenomics/meteor2          # catalog auto-injected + depends auto-wired
    inputs: fastp.fastqs
```

Behind the scenes the runner rewrites this to the equivalent of:

```yaml
steps:
  - import: genomics/fastp
    inputs: sample.fastqs
  - import: metagenomics/meteor2_catalog
  - import: metagenomics/meteor2
    inputs: fastp.fastqs
    depends: meteor2_catalog
```

If you want explicit control — for instance, to place the setup step somewhere specific, or to pass step-level overrides — just import it yourself in the template. The runner detects the explicit import and skips the injection, but still wires `depends:` for you.

**When to use `requires:` vs. explicit `import:`**:

- Module author: declare `requires:` on your module body if your step fundamentally doesn't work without another module also being present. Self-healing templates.
- Template author: if you want to place a setup step in a custom spot or override its params, write an explicit `import:` for it. `requires:` won't duplicate.

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

## Quality scoring

Steps can define **quality variables** — named metrics extracted from task stdout/stderr via regex patterns. These are combined into a **quality score** that enables optimization loops and monitoring.

```yaml
steps:
  - name: train
    container: my_trainer
    command: "train --lr 0.01 /input/data"
    quality:
      variables:
        accuracy: "accuracy: ([0-9.]+)"
        loss: "final loss: ([0-9.]+)"
      score: "accuracy"
```

Quality extraction runs when a task succeeds (and periodically during execution for live monitoring). Each regex is matched against the full stdout+stderr, and the **last match** is taken (supporting iterative programs that output metrics per epoch). The score formula is a simple arithmetic expression over variable names (`+`, `-`, `*`, `/`, parentheses).

### Multi-objective quality

For multi-objective optimization (e.g., maximizing test AUC while minimizing overfitting), use `objectives` instead of `score`:

```yaml
    quality:
      variables:
        train_auc: "train_auc: ([0-9.]+)"
        test_auc: "test_auc: ([0-9.]+)"
      objectives:
        - formula: "test_auc"
          direction: maximize
        - formula: "test_auc - train_auc"
          direction: maximize    # maximize negative gap = minimize overfitting
```

Each objective has its own formula and direction. The primary `quality_score` on the task stores the first objective. All objective scores are available in `quality_vars`.

The quality score and individual variables are stored on the task and returned by the API (`quality_score`, `quality_vars` in task list output and MCP).

For optimization workflows, quality scoring is used with the `optimize:` block (below) or the [Python DSL live mode](dsl.md#live-mode-and-optimization).

## Optimization (`optimize:`)

The `optimize:` block adds Optuna-driven hyperparameter search to a YAML template. It works with the `iterate:` block: for each trial, Optuna suggests parameter values which are substituted into the target step's command, and the step runs once per sample. The trial score is the aggregated quality across all samples.

```yaml
format: 2
name: gpredomics_opt
version: 1.0.0
description: Hyperparameter optimization for gpredomics

params:
  data_dir:
    type: string
    required: true
    help: URI to a folder containing param.yaml, Xtrain.tsv, Ytrain.tsv
  location:
    type: provider_region
    required: true
  n_trials:
    type: integer
    default: 50
  n_parallel:
    type: integer
    default: 5

worker_pool:
  provider: "{params.location}"
  cpu: ">= 8"
  mem: ">= 30"

optimize:
  direction: maximize
  n_trials: "{params.n_trials}"
  n_parallel: "{params.n_parallel}"
  aggregation: mean
  step: train
  storage: "sqlite:///optuna_gpredomics.db"
  search_space:
    mutation_pct:
      type: int
      low: 5
      high: 50
    population_size:
      type: categorical
      choices: [1000, 2000, 5000, 10000]
    k_penalty:
      type: float
      low: 0.00001
      high: 0.01
      log: true
  pruning:
    enabled: true
    grace_period: 60

steps:
  - name: train
    container: gpredomics:latest
    inputs: "{params.data_dir}"
    command: |
      cp /input/param.yaml /tmp/param.yaml
      sed -i "s/mutation_non_null_chance_pct:.*/mutation_non_null_chance_pct: {mutation_pct}/" /tmp/param.yaml
      sed -i "s/population_size:.*/population_size: {population_size}/" /tmp/param.yaml
      sed -i "s/k_penalty:.*/k_penalty: {k_penalty}/" /tmp/param.yaml
      gpredomics --config /tmp/param.yaml --csv-report
    quality:
      variables:
        train_auc: "QUALITY.*train_auc=([0-9.]+)"
        test_auc: "QUALITY.*test_auc=([0-9.]+)"
      score: "test_auc"
```

Each trial copies the base `param.yaml` from the input data, patches the hyperparameters via `sed`, and runs gpredomics. The quality score (AUC) is extracted from stdout. With `n_parallel: 5`, five trials run simultaneously on different workers.

The `pruning.grace_period: 60` gives gpredomics 60 seconds to save state when a trial is stopped early (SIGTERM → clean exit).

For cross-dataset evaluation (one trial = same hyperparameters evaluated across multiple datasets), add an `iterate:` block — the trial score becomes the aggregated quality across all samples.

### How it works

1. The workflow is created in **live mode** (prevents auto-completion)
2. The target step (`train`) is created with its quality definition and recruiter, but no tasks are submitted during the normal iteration loop
3. The YAML runner enters an Optuna loop:
   - Asks Optuna for `n_parallel` trial parameter sets
   - For each trial, substitutes the suggested values (`{lr}`, `{depth}`, `{algo}`) into the command template and submits one task per sample
   - Waits for all tasks to complete and extracts quality scores
   - Aggregates scores across samples (mean, median, min, or max) and reports to Optuna
   - Repeats until `n_trials` is reached
4. The workflow is closed (status set to S)

The Optuna study is persisted in `storage` (SQLite by default). If the process crashes and restarts, it resumes from the last completed trial (`load_if_exists=True`).

### `optimize:` fields

| Field | Default | Description |
|-------|---------|-------------|
| `direction` | `maximize` | Single-objective: `maximize` or `minimize` |
| `directions` | (none) | Multi-objective: list of `maximize`/`minimize` (one per objective in quality definition). Replaces `direction` |
| `n_trials` | `100` | Total number of trials |
| `n_parallel` | `1` | Trials to run concurrently |
| `aggregation` | `mean` | How to aggregate per-sample scores: `mean`, `median`, `min`, `max`. Applied per-objective for multi-objective |
| `step` | (required) | Name of the step to optimize |
| `storage` | `sqlite:///optuna_{name}.db` | Optuna study storage URL |
| `study_name` | `scitq_{name}` | Optuna study name |
| `seed` | (none) | Random seed for the sampler (makes trials reproducible) |
| `sampler` | `tpe` | Sampling algorithm: `tpe`, `cmaes`, `random`, `qmc`, `nsgaii`. See below |
| `sampler_options` | `{}` | Extra kwargs passed to the sampler constructor (e.g. `{multivariate: true, n_startup_trials: 20}`) |
| `search_space` | (required) | Parameter definitions (see below) |
| `pruning.enabled` | `false` | Enable early stopping of bad trials |
| `pruning.grace_period` | `10` | Seconds before SIGKILL after SIGTERM |

### Samplers

| Name | Algorithm | Best for |
|------|-----------|----------|
| `tpe` | Tree-structured Parzen Estimator (default) | General purpose, mixed parameter types |
| `cmaes` | Covariance Matrix Adaptation | Continuous parameters, smooth landscapes |
| `random` | Uniform random | Baseline, large search spaces |
| `qmc` | Quasi-Monte Carlo | Systematic coverage, few trials |
| `nsgaii` | NSGA-II genetic algorithm | Multi-objective (auto-selected when `directions` is set) |

If `tpe` seems to wander in unproductive regions, try `cmaes` for continuous parameters or increase exploration with `sampler_options: {n_startup_trials: 20, multivariate: true}`.

Example:
```yaml
optimize:
  step: train
  sampler: cmaes
  seed: 42
  n_trials: 100
  search_space:
    lr: { type: float, low: 0.0001, high: 0.1, log: true }
    depth: { type: int, low: 1, high: 10 }
```

### Multi-objective example

To simultaneously maximize test performance and minimize overfitting:

```yaml
steps:
  - name: train
    quality:
      variables:
        train_auc: "train_auc: ([0-9.]+)"
        test_auc: "test_auc: ([0-9.]+)"
      objectives:
        - formula: "test_auc"
          direction: maximize
        - formula: "test_auc - train_auc"
          direction: maximize

optimize:
  step: train
  directions:
    - maximize     # test_auc
    - maximize     # test_auc - train_auc (minimize overfitting)
  n_trials: 100
  n_parallel: 5
  search_space:
    lr: { type: float, low: 0.0001, high: 0.1, log: true }
    depth: { type: int, low: 1, high: 10 }
```

Multi-objective optimization returns a **Pareto front** instead of a single best trial — the set of trials where no other trial is better in all objectives simultaneously. The YAML runner reports the Pareto front at the end.

### Search space parameter types

```yaml
search_space:
  # Float parameter (continuous)
  lr:
    type: float
    low: 0.0001
    high: 0.1
    log: true          # log-uniform distribution

  # Integer parameter
  depth:
    type: int
    low: 1
    high: 10

  # Categorical parameter
  algo:
    type: categorical
    choices: ["rf", "xgb", "svm"]
```

Suggested values are available in the step command as `{param_name}` (lowercase) and `{PARAM_NAME}` (uppercase).

### Prerequisites

The `optuna` Python package must be installed in the server's DSL virtual environment:

```sh
pip install optuna
```

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
