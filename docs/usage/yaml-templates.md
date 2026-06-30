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
tag: "{params.project}"     # Optional: tag for each workflow instance (see "Tag" below)

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

## Tag

The `tag:` field is an arbitrary string appended to a workflow's name (and per-step task names) to distinguish multiple runs of the same template. It's purely cosmetic — used by the UI, CLI, and task naming — and has no semantic effect on scheduling.

### Workflow-level `tag:`

Plain string with the usual `{params.x}` and `{VAR}` substitutions:

```yaml
name: my_workflow
tag: "{params.project}/{params.depth}"
```

A `cond:` block dispatches the tag on a value, just like `cond:` everywhere else:

```yaml
tag:
  cond: "{params.mode}"
  dev:  "dev-{params.run}"
  prod: "prod-{params.run}"
  default: "scratch-{params.run}"
```

The chosen branch's value is re-resolved with substitutions, so referencing `{params.run}` inside a branch works.

### Step-level `tag:`

Each step also accepts a `tag:` field. When absent, scitq auto-derives the per-task tag from the iterator context (e.g. `S1.chr2` for a product-iter sample × chrom). Setting `tag:` explicitly overrides that derivation — useful for human-readable task names that don't expose every iteration coordinate:

```yaml
steps:
  - name: align
    tag: "{SAMPLE}-{params.mode}"
    command: ...
```

Step-level `tag:` is re-resolved per task, so it sees both the iter context (`{SAMPLE}`, `{CHROM}`, …) and workflow params. A `cond:` block works the same way as at workflow level:

```yaml
steps:
  - name: profile
    tag:
      cond: "{params.mode}"
      fast: "{SAMPLE}-quick"
      thorough: "{SAMPLE}-deep"
    command: ...
```

If you omit `tag:` on a step that uses iteration, the auto-derived tag (composite of all iter variables, or just the `grouped_by` key for keyed-grouping steps) remains the default.

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

  tolerance:
    type: float
    default: 0.8
    help: "Decimal threshold (0 < t <= 1). Accepts scientific notation: 1e-4."

  location:
    type: provider_region
    required: true

  paired:
    type: boolean
    default: true
```

Supported types: `string`, `integer`, `float`, `boolean`, `enum` (with `choices`), `provider_region`, `path`, `text`.

`float` accepts decimals (`0.8`) and scientific notation (`1e-4`, `-3.0e2`). Use `integer` for whole numbers and `float` for any value where decimals or exponents matter (rates, tolerances, p-values, learning rates, …). The CLI and UI both accept the same syntax; the UI renders `float` params with a `step="any"` number input that does not snap to integers.

Parameters are referenced in the template as `{params.name}`. Optional parameters use the syntax `{params.name:default_value}` — the default is used when the parameter is not provided.

#### `text` — long / multi-line string content

`type: text` is a string-shaped param meant for long, possibly multi-line content (sample lists, manifests, inline scripts, etc.). Server-side it's just a string; the type is a UX hint so the UI renders a textarea with an optional file picker instead of a single-line input.

```yaml
params:
  sample_list:
    type: text
    required: false
    help: "One glob per line, each resolving to a sample's input fastqs"
```

How to provide a value:
- **CLI**: paste it inline, or use the `@file` shorthand to read a local file: `--param sample_list=@/local/path/to/list.txt`. (See the next section — `@file` works on **any** string-shaped param, not just `text`.)
- **UI**: the template-run form renders a file picker plus a textarea; you can either upload a file (read client-side via `FileReader`) or paste content directly.
- **MCP / API**: pass the content as a normal string in the `param_values_json` payload.

The file does **not** need to exist on the server when the `@file` shorthand is used — the CLI/UI reads it locally and ships the content over the wire.

#### `@file` CLI shorthand

On any string-shaped param (`string`, `text`, etc.), a value starting with `@` is interpreted as a local path; the CLI reads the file and substitutes its content before sending the request:

```sh
scitq template run --name my_template --param 'manifest=@./manifest.txt,note=hello'
```

The file is read locally — it does not need to exist on the server. Use `\@literal` to pass a literal leading `@` (e.g. a GitHub handle).

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

#### Tabular iterator (`source: tsv`)

Sample sheets are the de-facto sample-discovery shape in bioinformatics — nf-core's `samplesheet.csv`, Snakemake's `samples.tsv`, etc. Use `source: tsv` to drive iteration from a TSV/CSV file's rows:

```yaml
iterate:
  name: sample
  source: tsv
  content: |                    # in-memory inline (or use `uri:` for a file)
    sample_name	depth_gb	assembly_file
    A	30	s3://bucket/A.fa.gz
    B	120	s3://bucket/B.fa.gz
  key: sample_name              # iter tag column (default: first column)
```

Each row becomes one iteration. Columns are exposed under the iterator name with dotted syntax:

| Reference | Resolves to |
|---|---|
| `{SAMPLE}` | the `key:` column value (the iter tag) |
| `{sample.depth_gb}` | the row's `depth_gb` column |
| `{sample.assembly_file}` | the row's `assembly_file` column |

In expression-aware fields (`when:`, `cond:`, `task_spec` …) the same dotted form is a Python attribute access, so all the F' operators apply:

```yaml
steps:
  - name: assemble
    when: "sample.depth_gb > 5"                  # data-driven gating per row
    inputs:
      - "{sample.assembly_file}"
    command: |
      megahit -r {sample.assembly_file} -o {SAMPLE}/
```

Input fields:

- `content:` — in-memory string. Typical use: a `type: text` param fed via the CLI's `@file` shorthand, an upload from the UI, or operator paste.
- `uri:` — local path on the server (the runner host). Remote URIs (`s3://`, `gs://`, …) are reserved for a future revision; today the workflow author either inlines the rows via `content:` or pre-stages the file on the server.

The two are mutually exclusive — supply exactly one.

Optional fields:

- `key:` — iteration tag, either a single column name (string) or a list of column names (composite key — see below). Defaults to the first column. The synthesized tag must be unique across rows.
- `sep:` — field separator. Auto-detected from the URI extension (`.tsv` → tab, `.csv` → comma), defaulting to tab.

Missing cells resolve to empty strings; this composes cleanly with `when:` for skip-if-missing patterns (`when: "sample.preqc_done"`).

##### Composite keys (`key: [colA, colB, ...]`)

When the iteration is over **pairs** (or larger tuples) rather than single samples, no single column is unique — but the combination is. Pass `key:` as a **list of column names**, and the runner synthesizes the iter tag as their dot-joined values and exposes each as both `{iter.col}` AND a top-level `{COL}` alias for `grouped_by:` lookup:

```yaml
iterate:
  name: pair
  source: tsv
  content: |
    ref     query   ref_assembly                          query_r1                            query_r2
    A       A       s3://bkt/m/A.contigs.fa               s3://bkt/q/A.1.fastq.gz             s3://bkt/q/A.2.fastq.gz
    A       B       s3://bkt/m/A.contigs.fa               s3://bkt/q/B.1.fastq.gz             s3://bkt/q/B.2.fastq.gz
    B       A       s3://bkt/m/B.contigs.fa               s3://bkt/q/A.1.fastq.gz             s3://bkt/q/A.2.fastq.gz
    B       B       s3://bkt/m/B.contigs.fa               s3://bkt/q/B.1.fastq.gz             s3://bkt/q/B.2.fastq.gz
  key: [ref, query]
```

For each row, the iter dict carries:

| Reference | Resolves to |
|---|---|
| `{PAIR}` | the composite tag (`"A.A"`, `"A.B"`, …) — the iter name's value |
| `{REF}`, `{QUERY}` | uppercase aliases of each key column. Use these in `grouped_by:`, `{REF}` substitution, and tag construction. |
| `{pair.ref}`, `{pair.query}` | lower-case dotted form (data access, same value as the alias) |
| `{pair.ref_assembly}`, `{pair.query_r1}`, `{pair.query_r2}` | any extra column (data, kept out of the tag) |

The duplicate (composite tag + aliases) is intentional: the composite gives you a one-shot identifier for naming files (`{PAIR}.cov.txt`), the aliases give you per-axis lookup for `grouped_by`. Extra columns (URIs, metadata) stay accessible for `inputs:` and command substitution but **do not pollute the task tag** — important when those values contain dots themselves (URIs do).

###### When to use it: the (ref × query) cross-mapping shape

The canonical case is metagenomic binning with multi-sample differential coverage: each ref's assembly receives coverage signal from N query samples. With `key: [ref, query]` you get 237 refs × 10 queries = 2370 iterations, and the workflow steps decompose cleanly:

```yaml
steps:
  - name: split_contigs
    grouped_by: ref           # 1 task per unique ref (237)
    inputs: ["{pair.ref_assembly}"]
    # ...

  - name: sketch
    grouped_by: query         # 1 task per unique query (237)
    inputs: ["{pair.query_r1}", "{pair.query_r2}"]
    # ...

  - name: coverage
    # per-(ref, query) pair — 2370 tasks; subset-matches the
    # right split_contigs (by REF) and the right sketch (by QUERY)
    inputs: [split_contigs.split, sketch.sketch]
    # ...

  - name: features
    grouped_by: ref           # 1 task per ref, fanning in 10 aemb tables
    inputs: [coverage.aemb]
    # ...
```

Two runner mechanics make this work:

1. **Build-order.** `grouped_by` steps whose inputs don't reference other steps (here `split_contigs` and `sketch`) are built BEFORE the per-iter loop, so the per-pair `coverage` step can resolve their outputs by subset-match. (Without this, the historical "grouped_by-last" order produced an empty upstream when per-iter tasks tried to chain off a grouped_by upstream.)
2. **Constant-column propagation.** `grouped_by: ref` runs once per unique ref, with the synthetic iter dict carrying only `REF` plus every column whose value is constant across the ref-group (e.g. `pair.ref_assembly`). Per-key-derived URIs are reachable in the grouped step's `inputs:` and command; per-other-key URIs (`pair.query_r1` for a ref-group) stay out of scope, since they vary.

###### Constraints

- The synthesized tag (the dot-joined values of the listed key columns) must be unique across rows — the runner errors at iteration time with `duplicate composite tag '<tag>'` otherwise. Repetition within a single column is fine (every row sharing a `ref` is exactly the multi-coverage shape).
- Every row that shares a value in a `grouped_by` key column should also share the data columns derived from that key. E.g. all rows with `ref=A` should have the same `ref_assembly`. The constant-column propagator filters silently — values that vary within the group simply don't get exposed in the grouped task's `{pair.col}` namespace.

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

# Lines — each non-empty line is one iteration.
# Two ways to feed it (mutually exclusive):
iterate:
  name: sample
  source: lines
  file: "/etc/scitq/samples/list.txt"   # path on the runner host
  # OR:
  # content: "{params.sample_list}"     # in-memory string, typically from a text param
```

##### `lines` with `item:` — per-sample input files from a list of globs

When each line is a URI or glob pointing at one sample's input files, set `item:` to the file-group name you want the URI stored as. The iterator passes each line through *literally* — globs are **not** expanded at submission time; the worker's downloader expands them at task start via its built-in rclone-filter path. This keeps template-run instant even for thousands of samples (no per-line S3 list call).

```yaml
params:
  sample_list:
    type: text
    required: true

iterate:
  name: sample
  source: lines
  content: "{params.sample_list}"
  item: "fastqs"      # each line's matched URIs land in sample.fastqs
  tag: "folder"       # iteration tag = parent folder name (default when item: is set)

steps:
  - import: private/megahit
    inputs: sample.fastqs
```

With `sample_list` containing:
```
s3://rnd/raw/SCAPIS/ERR11457772/*.fastq.gz
s3://rnd/raw/SCAPIS/ERR11457789/*.fastq.gz
```
…each line becomes one task; the task tag is `ERR11457772` / `ERR11457789` (from `tag: folder`, derived from the leaf folder before the glob); `sample.fastqs` is the literal glob string. The worker downloads matched files via rclone's filter at task start.

A line whose glob resolves to nothing **at task runtime** results in a clean task failure (no inputs downloaded → the task command can't find its files). This is preferable to silent skip — a typo'd sample becomes a single failed task that's easy to retry, rather than a missing iteration that might go unnoticed.

`tag:` currently supports `folder` — the leaf directory of each glob's prefix, derived locally from the line itself. When `item:` is set and `tag:` is omitted, `folder` is the default. Without `item:`, each line is taken as the iteration value verbatim (existing behavior, for plain enum-style lists).

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

#### `fastq_pair_filtering` — keep R1 only

When a workflow needs to run **single-end** computation on a sample whose source files are paired (`*_1.fastq.gz` / `*_2.fastq.gz`), set `fastq_pair_filtering: true` on the iterator. The iterator will keep R1 and drop R2 — but only on samples whose files actually look like an R1/R2 pair; anything classified as single-end already passes through unchanged. A sample is never dropped.

```yaml
vars:
  PAIRED: "{params.depth|is_paired}"   # workflow-level: true for 2x* depths

iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  fastqs: "*.f*q.gz"
  fastq_pair_filtering: "{PAIRED|not}"   # filter only when running unpaired
```

For ENA/SRA sources, opting in additionally rewrites the underlying URI with `@only-read1` so R2 is never fetched over the wire.

This is **opt-in by design**. Detecting an R1/R2 pair from filenames is fine on real paired-end data, but a single-end file that happens to match an R1 pattern by coincidence would otherwise have its sibling silently dropped. Making this explicit keeps the failure mode loud.

The flag also works under conditional iterators — it's inherited by every branch unless a branch overrides it:

```yaml
iterate:
  name: sample
  match: "{params.filter:*}"
  fastq_pair_filtering: "{PAIRED|not}"   # applied to whichever branch fires below
  cond: "{params.data_source}"
  uri:  { source: uri,  uri: "{params.bioproject}", group_by: folder, fastqs: "*.f*q.gz" }
  ena:  { source: ena,  identifier: "{params.bioproject}", group_by: sample_accession }
  sra:  { source: sra,  identifier: "{params.bioproject}", group_by: sample_accession }
```

Each iterator source declares the attributes it understands (`name`, `match`, `fastq_pair_filtering`, plus source-specific keys like `uri`, `identifier`, `where`, `group_by`, etc.). Passing an unknown attribute fails fast with an error listing the supported keys for that source — no silent ignores.

#### `product:` — outer-product (multi-dimensional iteration)

For "every sample × every chromosome" or "every sample × every parameter setting" patterns, declare an outer-product dimension nested inside the primary iterator:

```yaml
iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  fastqs: "*.f*q.gz"
  product:                       # outer-product dimension
    name: chrom
    source: list
    values: [chr1, chr2, chr3, ..., chrX]
# → iterations = samples × chroms; tag = "{SAMPLE}.{CHROM}"
```

The `product:` value is itself a full iterator spec, recursively validated, so it can use any source (`list`, `range`, `uri`, ...). Each iteration receives both variables; commands can reference `{SAMPLE}` and `{CHROM}` independently. The composite tag is the dot-join of the iterator values, so a per-sample step here produces tasks like `call.S001.chr1`, `call.S001.chr2`, ....

This is the static (compile-time-enumerable) version of the Cartesian product. For runtime-cardinality fan-out (e.g. "split until each chunk < N reads"), use [workflow chaining](#chaining-workflows) instead — the next workflow's iterator enumerates the prior workflow's outputs on storage.

### Three kinds of steps

Steps fall into four categories based on how they relate to the iteration loop:

1. **Per-sample steps** (default): run once per iteration. They have access to `{SAMPLE}` and the sample's input files.
2. **Fan-in steps** (`grouped: true`): run once after all iterations. They receive the combined outputs of all per-sample tasks.
3. **One-off steps** (`per_sample: false`): run once before the iteration loop (e.g. index building).
4. **Keyed-grouping steps** (`grouped_by: <iter-name>`): run once per *distinct value* of the named iteration variable, collecting outputs from upstream tasks that shared that value. The complement of `product:` — collapses one dimension of a multi-dimensional iteration back into one task per key. See ["Keyed grouping"](#keyed-grouping-grouped_by) below.

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

#### Keyed grouping (`grouped_by:`)

`grouped_by: <iter-name>` is the dimensional complement of `grouped: true`. Instead of one task collecting **all** upstream outputs, it produces **one task per distinct value** of the named iteration variable, each collecting only that group's outputs:

```yaml
iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  fastqs: "*.f*q.gz"
  product:
    name: chrom
    source: list
    values: [chr1, chr2, ..., chrX]

steps:
  - name: call
    inputs: sample.fastqs
    command: 'gatk HaplotypeCaller -L {CHROM} -I /input/*.bam -O {SAMPLE}.{CHROM}.vcf'
    outputs: { vcf: "*.vcf" }

  - name: merge
    inputs: call.vcf
    grouped_by: sample          # ← one merge task per sample, collecting that sample's chroms
    command: 'bcftools concat -O z -o {SAMPLE}.vcf.gz /input/*.vcf'
    outputs: { vcf: "*.vcf.gz" }

  - name: report
    inputs: merge.vcf
    grouped: true                # ← single task collecting ALL samples
```

The grouping key is the iterator's `name:` written lowercase (the same identifier you'd put in `name:`); inside the keyed-grouping task, `{SAMPLE}` (or whatever the key is) substitutes to the group value, and the task tag is just that value (`merge.S001`, not `merge.S001.chr1`).

Upstream-task selection per group works for any upstream shape:
- per-iter upstream (`call.S001.chr1`, `call.S001.chr2`, ...) — each `merge.S<i>` collects its same-sample call outputs.
- another `grouped_by:` upstream with the same key — straight 1:1 pairing.
- fully grouped upstream (`grouped: true`) — not addressable by group; use a plain inputs reference instead.

Together with `product:`, this closes most "Cartesian fan-out then per-key fan-in" patterns (variant calling per (sample × chrom) then per-sample merge; per-lane QC then per-sample merge; etc.) without needing the runtime topology gymnastics that channel-algebra workflow engines use for the same shape.

#### Deterministic file order in fan-in scripts

A fan-in step receives its inputs as a flat directory of files in `/input/`. Iteration with bare `glob('/input/*.csv')` returns files in **filesystem order**, which is not stable across runs (particularly across remote storage backends and depending on how the worker download interleaves file arrivals). For aggregations whose output depends on iteration order — typically anything involving floating-point summation — sort the list:

```python
from glob import glob
for path in sorted(glob('*.tsv')):       # ← deterministic across runs
    df = pd.read_csv(path, sep='\t')
    ...
```

The cost is one O(N log N) sort at the start; the payoff is bit-identical output across reruns of the same workflow on the same inputs (so a NSAT/abundance/quality table compares clean with `diff`, and reuse hits don't drift due to sum-accumulation order).

### Outputs

Named outputs let downstream steps reference specific file patterns:

```yaml
  - name: fastp
    outputs:
      fastqs: "*.fastq.gz"
      json: "*.json"
```

Without named outputs, the step exposes its entire `/output/` directory.

### Container options

`container_options:` appends free-form flags to the `docker run` argv used for this step's tasks. Use for runtime settings `task_spec` doesn't model — `--shm-size`, `--ipc=host`, `--privileged`, custom `--mount`, `--device`, etc.

```yaml
  - name: train
    container: pytorch/pytorch:2.5.1-cuda12.4-cudnn9-runtime
    container_options: "--shm-size=16g --ipc=host"
    task_spec:
      gpu: 1
```

Caveats:

- `--gpus` is auto-appended when `task_spec.gpu > 0` (or `gpu: "all"`). Don't repeat it here unless you need explicit device pinning — an explicit `--gpus` in `container_options` suppresses the auto-append.
- The string is interpolated like other fields (`{params.x}`, `{ITER}`, cond blocks).

### Task spec

`task_spec:` declares per-task settings — resource budget for the dynamic-concurrency scheduler, and a couple of opt-in worker-side behaviours.

```yaml
  - name: align
    task_spec:
      cpu: 8           # tasks of this step want 8 cpu each
      mem: 30          # …and 30 GB RAM each
      disk: 100        # …and 100 GB disk each
      gpu: 1           # …and 1 GPU each (see "GPU" below); or "all" for one task per worker with every device
      concurrency: 4   # OR: static concurrency (mutually exclusive with cpu/mem/disk)
      prefetch: "50%"  # download up to N task inputs in advance (N = concurrency * prefetch)
      scitq_auth: true # see below
      numa: 1          # OR: pin each task to N NUMA nodes (see below; mutually exclusive with cpu/mem)
```

At least one of `concurrency`, `cpu`, `mem`, `numa`, or `gpu ≥ 1` (integer or `"all"`) is required. `cpu`/`mem`/`disk`/`gpu` drive dynamic concurrency (scitq picks a per-worker concurrency that fits the worker's flavour and these per-task budgets); `concurrency` pins it to a static value; `numa` (see below) derives the per-task CPU and memory budget from the worker's NUMA topology.

**Heads-up:** declaring `task_spec.gpu` on a step whose `worker_pool` has no GPU filter (`gpu: true` / `W.has_gpu == True`) emits a `RuntimeWarning` at compile time. The recruiter would otherwise pick a CPU-only flavor and every assignment would then fail the fit predicate, stranding the step.

#### Conditional `task_spec` (data-driven branching)

Real workloads have sample-size variance — an assembly step for a 200 GB sample needs a different worker class than a 5 GB sample. Express that with a top-level `cond:` block. The branches are full sub-specs; the matching branch's fields merge with any sibling fields outside the cond:

```yaml
  - name: assemble
    task_spec:
      cond: sample.depth_gb > 100
      true:
        cpu: 32
        mem: 256
      false:
        cpu: 16
        mem: 64
      disk: 400                  # outside the cond → applies to both branches
```

The `cond:` field accepts the full F' expression grammar (`==`, `!=`, `<`, `<=`, `>`, `>=`, `in`, `~`, `and`, `or`, `not`), so it composes with all the iter-context features:

```yaml
    task_spec:
      cond: "sample.platform in ('illumina', 'mgi') and sample.depth_gb > 50"
      true:  { cpu: 32, mem: 128 }
      false: { cpu: 16, mem: 64 }
```

Branches may declare any subset of fields; whatever isn't in the chosen branch but is in the sibling block applies to all branches. A `default:` key catches the unmatched-cond case.

**Why a structured block rather than per-field expressions.** Free-form expressions in `cpu:` / `mem:` (`mem: "{sample.size * 2 + 8}"`) would produce a unique spec per sample in the worst case and fragment recruitment into 1-flavor-per-task — directly at odds with scitq's batching model. A `cond:` block forces the workflow author to declare a finite set of buckets; the recruiter recruits a flavor per branch, not per sample.

#### `numa: <int>` — pin tasks to specific NUMA nodes

When set, each task of this step is launched with `--cpuset-cpus` / `--cpuset-mems` pointing at `numa` consecutive NUMA nodes on its worker. The per-task `$CPU` env var becomes the count of CPUs in those nodes; `$MEM` is approximated from the host's total memory scaled by node share.

```yaml
  - name: assemble
    task_spec:
      numa: 1          # one task per NUMA node (the common case)
      prefetch: 1
```

Why use it: memory-bandwidth-bound workloads (de Bruijn graph assemblers like megahit, alignment with large in-RAM indexes) lose substantial throughput on multi-die hosts when threads from different tasks contend for the same memory controller. Binding each task to a single die preserves locality and removes cross-die traffic.

Rules:

- **Mutually exclusive with `cpu` / `mem`.** Concurrency and per-task budget come from the topology; expressing them again is overdetermined and rejected by the DSL.
- **Positive integer.** `numa: 0` is rejected; `numa: 2` spans two adjacent NUMA nodes (useful when one node's memory is too small for a single task).
- **Concurrency is auto-derived on first contact.** The first time a worker receives a task with `numa: N`, it computes `concurrency = floor(host_nodes / N)` and pushes that back to the server via `UpdateWorker`. So `numa: 1` on a 4-NUMA-node host gives concurrency 4 automatically — no need to set `--concurrency` at launch. The derivation happens **once per worker boot**; if you afterwards prefer a different value, `scitq worker update --worker-id N --concurrency M` sets it and the worker won't fight you. The auto-derive log line looks like: `📐 Auto-deriving concurrency from numa: 1 → 4 (host has 4 NUMA node(s), task wants 1)`.
- **Docker-only in v1.** Bare tasks (no container) run without binding even when `numa` is set; bare-task NUMA binding via `numactl` is a planned follow-up.
- **Non-NUMA hosts** (single-node, or `/sys/devices/system/node/` absent — e.g. macOS dev boxes) are treated as if they had one NUMA node: `numa: 1` gives concurrency 1, the task runs unbound, and a warning is logged once. `numa: N` with N>1 also runs unbound with concurrency 1 — the YAML still works on small dev boxes, just with reduced parallelism.

#### `gpu` — GPU access for the task

Per-task GPU requirement. Drives the assignment fit predicate (worker must have `flavor.gpu_count >= task.min_gpu`) and, when set, makes the worker auto-append `--gpus all` plus `CUDA_VISIBLE_DEVICES=<indices>` so each in-flight task sees a disjoint slice of the host's GPUs.

```yaml
  - name: train
    task_spec:
      gpu: 1          # one GPU per task — N tasks run in parallel on an N-GPU host
```

Accepted values:

| value | meaning |
|---|---|
| omitted, `0`, `false` | No GPU needed (default). The task fits any flavor; no driver injection. |
| `N` (integer ≥ 1) | Each task pins `N` device indices. On a 4-GPU host with `gpu: 1`, four tasks run in parallel each seeing one device. With `gpu: 2`, two tasks run in parallel each seeing two devices. |
| `true` | Sugar for `gpu: 1`. |
| `"all"` | **Exclusive access.** One task per worker; that task sees every GPU on the host. Resolved at assignment — the workflow stays portable across 1-GPU and 8-GPU SKUs. See "GPU exclusive access" below. |

Notes:

- **Concurrency is auto-derived.** With dynamic `cpu`/`mem`/`disk`, `gpu: N` is the 4th dimension of the per-task ratio. The recruiter picks `min(cpu/cpu_per_task, mem/mem_per_task, disk/disk_per_task, gpu_count/N)` for the worker's concurrency. A 4-GPU host with `gpu: 1` and `cpu: 4` on a 32-vCPU flavor gets `min(8, 4) = 4` concurrent tasks.
- **The fit predicate is strict.** A task with `gpu: 5` on a 4-GPU flavor stays `P` — no implicit downgrade. Either ask for fewer GPUs or filter for a bigger flavor in `worker_pool`.
- **Driver injection.** When `gpu > 0`, the worker also exports `SCITQ_GPU=<count>` and `SCITQ_GPU_DEVICES=<comma-separated indices>` so the task command can branch on them.

##### `gpu: "all"` — exclusive multi-GPU access

For workloads that need to see every device on the host (multi-GPU training, in-process model sharding, anything that calls `torch.cuda.device_count()`):

```yaml
  - name: train-multi-gpu
    task_spec:
      gpu: "all"
    worker_pool:
      gpu: true
      flavor: "Standard_NC%"     # filter to a multi-GPU SKU
```

Semantics:

- **Concurrency is forced to 1.** The recruiter pins one task per worker; per-task `cpu`/`mem`/`disk` ratios are ignored (they'd be meaningless with `concurrency=1`).
- **The integer count is resolved at assignment.** The task's stored `min_gpu` is `NULL` until a worker is picked, at which point `min_gpu = worker.flavor.gpu_count` and `CUDA_VISIBLE_DEVICES` lists every index.
- **Portable across SKUs.** The same template works on a 4-GPU NC4 and an 8-GPU NC24 — the per-task count adjusts to whichever flavor the recruiter picked.
- **Mutually exclusive with `concurrency: N > 1`.** `gpu: "all" + concurrency: 2` is rejected at parse time (incoherent on a fixed-GPU host).
- **Retries re-resolve.** If an eviction sends a retry onto a differently-sized flavor, the integer is recomputed cleanly — `gpu_all=TRUE` stays on the row, `min_gpu` clears and re-materializes.

#### `scitq_auth: true` — the task can call `scitq file copy`

When `task_spec.scitq_auth: true`, the worker, before launching the task, does two extra things:

1. Injects `SCITQ_SERVER` (the address the worker registered with) and `SCITQ_TOKEN` (the worker's own auth token) as environment variables in the task.
2. For docker tasks, bind-mounts the worker's `/usr/local/bin/scitq` into the container at the same path, read-only.

The task can then call `scitq file copy <src> <dst>` (or any other `scitq` CLI subcommand) and authenticate against the server as the worker. The server's rclone configuration — including any encrypted-archive crypt password or cloud-provider access key — never reaches the container; the task only ever holds a token.

Use cases:

- The same file must land at two different destinations (e.g. an S3 raw bucket and an encrypted Azure archive) in a single task.
- The destination layout doesn't fit one fixed `publish:` URI per step (asymmetric paths like `s3://rnd/raw/<gmt>/...` vs `s3://rnd/raw/<gmt>.meta/...`).

The bind-mounted `scitq` is the worker's own binary, kept in sync with the server by `make server-upgrade` for collocated workers and by Phase II auto-upgrade for cloud workers (the upgrade pass refreshes the CLI alongside the worker binary).

Default: `false`. Most tasks never see scitq credentials.

### Worker pool

The worker pool defines what kind of machines to recruit:

```yaml
worker_pool:
  provider: "{params.location}"    # Cloud provider:region
  cpu: ">= 8"                     # CPU filter
  mem: ">= 30"                    # Memory (GB) filter
  disk: ">= 100"                  # Disk (GB) filter
  gpu: true                        # GPU filter — see "GPU filters" below
  flavor: "Standard_NC%"           # Flavor-name filter (exact or LIKE with %)
  image: "Canonical/UbuntuServer/24.04-LTS/latest"     # Cloud image override (any flavor)
  gpu_image: "microsoft-dsvm/ubuntu-hpc/2204/latest"   # Cloud image override (GPU flavors only)
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

#### GPU filters

`worker_pool.gpu` restricts the candidate flavors to GPU-bearing ones:

| value | effect |
|---|---|
| `true`, `1`, `">= 1"` | Any GPU flavor (`flavor.has_gpu = TRUE`). |
| `">= N"` (N > 1) | Currently collapses to "has any GPU" — multi-count filtering on the flavor side is a planned extension. To target a specific multi-GPU SKU today, combine with `flavor:` (below). |
| `false`, `0`, omitted | No GPU constraint. |

`worker_pool.gpu` is the *flavor* filter (does this SKU have a GPU at all). `task_spec.gpu` is the per-task budget (how many devices each task wants). The two compose: `worker_pool.gpu: true` + `task_spec.gpu: 1` recruits any GPU flavor and runs N tasks per worker where N = flavor's `gpu_count`.

#### `flavor:` — name-based filter

When `worker_pool.gpu: true` isn't selective enough (different GPU families need different drivers — Azure NC = full-passthrough HPC image, NV = vGPU Grid image), narrow further by flavor name:

```yaml
worker_pool:
  gpu: true
  flavor: "Standard_NC%"     # SQL LIKE — every Standard_NC* SKU
  # flavor: "Standard_NC24ads_A100_v4"   # exact match (no %)
```

A value containing `%` is matched with SQL `LIKE`; otherwise the filter is exact. Useful for pinning to a known-working image/driver combination on Azure or OVH.

#### `image:` / `gpu_image:` — per-pool cloud image override

The cloud image booted for a recruited worker normally comes from server-wide config (`scitq.yaml`'s `azure.image` / `azure.gpu_image` / `openstack.image_id` / `openstack.gpu_image_id`). When a single workflow needs a different image — a custom-baked image with a baked-in tool, the vGPU Grid image instead of the HPC image, an ARM-native variant — the worker pool can override per-recruiter:

| field | applies to | typical use |
|---|---|---|
| `image:` | every flavor the recruiter picks | custom-baked image with a baked-in tool; ARM variant |
| `gpu_image:` | only flavors with `has_gpu=TRUE` | Grid driver for Azure NV family, alternative CUDA stack |

Precedence at deploy time: `gpu_image` (when the picked flavor has a GPU) → `image` → server-config default. Either field can be omitted; an empty string is treated as "not set".

Format is provider-specific:

| provider | format | example |
|---|---|---|
| Azure | `publisher/offer/sku[/version]` | `microsoft-dsvm/ubuntu-hpc/2204/latest` |
| OpenStack (OVH) | bare image name or UUID | `NVIDIA GPU Cloud (NGC)` |

The provider parses its own value and errors at VM creation time on malformed input (so a typo doesn't silently fall back to a wrong default — you see the failed job).

```yaml
worker_pool:
  gpu: true
  flavor: "Standard_NV%"                                   # NV family — needs Grid driver
  gpu_image: "nvidia/nvidia-gpu-optimized-vmi/24-02/latest"
```

Defaults shipped with scitq (when neither field is set):

- **Azure** — `Canonical/UbuntuServer/24.04-LTS/latest` (CPU), `microsoft-dsvm/ubuntu-hpc/2204/latest` (GPU, NC family).
- **OVH** — config-defined default, `NVIDIA GPU Cloud (NGC)` for GPU flavors.

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

Resources are validated before the workflow starts — if a resource doesn't exist, the workflow is aborted with a clear error. A resource that is published by another step in the same workflow is exempt from the existence check (and from sub-paths of such a publish path) — the producing step creates it before the consumer runs.

#### File vs directory resources

A resource URI without a trailing slash points to a single file:

```yaml
    resource: "azure://ref/index.tgz|untar"   # one file, extracted in /resource/
```

A resource URI that ends in `/` points to a directory and **copies its contents** into `/resource/` — i.e. the trailing folder name is *not* preserved, files land directly under `/resource/`. This matches the convention `cp dir/ /dest/` ≡ `cp dir/* /dest/` (the contents move, not the folder itself):

```yaml
    resource: "azure://ref/catalog/"
    # /resource/<file1>, /resource/<file2>, ...
    # NOT /resource/catalog/<file1>
```

Sub-directories inside the source are preserved relative to it. So pointing one level higher *does* recreate the intermediate folder:

```yaml
    resource: "azure://ref/"
    # /resource/catalog/<file1>, /resource/catalog/<file2>, ...
```

#### The `|mv:<name>` action

Use `|mv:<name>` to wrap the downloaded contents into a named subdirectory after the copy. Useful when the per-sample task expects a specific directory name (e.g. a tool that requires its index files to live under `/resource/<name>/`) but the source layout doesn't naturally produce that name:

```yaml
    resource: "{RESOURCE_ROOT}/metaphlan/{INDEX}/bowtie2/|mv:bowtie2"
    # contents of .../bowtie2/ → /resource/<files> (directory copy)
    # |mv:bowtie2 then moves them into /resource/bowtie2/<files>
    # so the command can reference --bowtie2db /resource/bowtie2
```

`|mv:<name>` and the parent-directory trick are interchangeable for two-level layouts — the choice is stylistic. The metagenomics catalog modules (`metaphlan`, `meteor2`, `humann`) use `|mv:<name>` consistently because they need to pull *several* sibling sub-directories independently from one published catalog (e.g. `fasta/` and `database/` from a meteor2 catalog), each landing at its own `/resource/<name>/`. Pointing at the parent would force a single recursive copy of all siblings.

#### The `|rename:<spec>` action

Rename the basename of each transferred file. Pattern syntax is Perl-style `s/<pattern>/<replacement>/<flags>` (matching the Unix `rename(1)` utility); the delimiter is the character right after `s` (typically `/`, but `s#a/b#x/y#` is also accepted for patterns containing literal slashes).

```yaml
    resource: "azure://ref/genes_v2.tsv.gz|rename:s/_v2//|gunzip"
    # downloads .../genes_v2.tsv.gz, renames to genes.tsv.gz before gunzip
```

Flags:

- `g` — apply to every match (default: first only).
- `i` — case-insensitive.

Replacement supports Go regexp Expand backreferences (`$1`, `$2`, `${name}`).

`|rename:` is target-side: same syntax whether the target is the local download path (under `resource:` or `inputs:`) or a remote publish path (under `publish:`):

```yaml
  - name: checkm2
    publish: "azure://results/{params.project}/{SAMPLE}/qc/|rename:s/quality_report/checkm2_quality_report/"
    # The uploaded file lands at .../checkm2_quality_report.tsv
```

`|` is the URI action separator; it isn't allowed inside the pattern. Use a character class `[|]` if you genuinely need the literal character.

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

### `default:` — fallthrough branch

A `cond:` block can declare a `default:` branch that fires when no other branch matches. This is the cleanest way to express "if a knob is set, use it; otherwise fall back":

```yaml
vars:
  # If params.metaphlan_index is empty, the empty key matches and the inner
  # cond on METAPHLAN_VERSION picks a per-version default. Otherwise default:
  # passes the user's value through.
  METAPHLAN_INDEX:
    cond: "{params.metaphlan_index}"
    "":
      cond: METAPHLAN_VERSION
      "4.0": "mpa_vOct22_CHOCOPhlAnSGB_202212"
      "4.1": "mpa_vOct22_CHOCOPhlAnSGB_202403"
      "4.2": "mpa_vJan25_CHOCOPhlAnSGB_202503"
    default: "{params.metaphlan_index}"
```

The runner resolves `cond:` blocks recursively — a branch that is itself a `cond:` is unwrapped until a leaf value is reached. The example above first matches against `params.metaphlan_index`; if that's empty, the engine then evaluates the inner `cond` against `METAPHLAN_VERSION`.

## Conditional steps (`when:`)

The `when:` field skips a step entirely when its value is falsy:

```yaml
  - module: oral_align.yaml
    when: "{params.oral}"           # Only built when oral=true
    inputs: seqtk.fastqs
```

This is cleaner than wrapping steps in conditionals — the step simply doesn't exist when the condition is false.

### Expression syntax

`when:` accepts a single ref (above) or a full expression. The supported operators are:

| Category | Operators |
|---|---|
| Arithmetic | `+ - * /`, `max(…)`, `min(…)`, `int(…)`, `float(…)` |
| Comparison | `== != < <= > >=` |
| Boolean | `and  or  not` |
| Membership | `in  not in` |
| Regex match | `~` (e.g. `path ~ '\\.bam$'` → True when the regex matches) |

References inside an expression don't need braces — `params.x`, the iterator name (e.g. `SAMPLE`), and any `vars:` entry are available directly:

```yaml
steps:
  - name: align_long
    when: "SAMPLE_READ_TYPE == 'long'"

  - name: profile_species
    when: "SAMPLE_N_READS >= 1_000_000"     # auto-coerces numeric-looking strings

  - name: extended_qc
    when: "params.profile in ('full', 'extended')"

  - name: filter_bams
    when: "SAMPLE_PATH ~ '\\.bam$'"
```

The whole expression may be wrapped in `{…}` for consistency with the substitution syntax used elsewhere; it's purely cosmetic. Strings drawn from string-typed sources (TSV columns, string params) auto-coerce to numbers when compared or arithmetically combined with a number; use explicit `int(...)` if you want to fail loudly on non-numeric input.

The same evaluator powers the `cond:` field of [cond blocks](#conditional-iterators), so `cond: params.depth_gb > 100` selects between `true:` / `false:` branches.

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

#### Vars propagate to auto-injected requires

A step's `vars:` block is also visible to the modules its `requires:` line pulls in. Setting `METAPHLAN_INDEX:` on the metaphlan step is enough to make the auto-injected `metaphlan_catalog` step download and publish the same catalog — both modules then resolve to the same `{RESOURCE_ROOT}/metaphlan/{METAPHLAN_INDEX}/` cache slot:

```yaml
steps:
  - import: metagenomics/metaphlan
    inputs: fastp.fastqs
    vars:
      METAPHLAN_VERSION: "4.0"
      METAPHLAN_INDEX: "mpa_vJan21_CHOCOPhlAnSGB_202103"   # legacy 4.0 catalog
```

Two requirers with **different** vars produce two distinct cache slots (different `METAPHLAN_INDEX` → different publish path → both catalogs coexist). Two requirers with the **same** vars are deduplicated into one auto-injected step.

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

`skip_if_exists` takes precedence over [opportunistic reuse](#opportunistic-reuse): a step with `skip_if_exists: true` is never short-circuited by a reuse cache hit, even when the workflow runs with `opportunistic: true`. The current publish target is the single source of truth.

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

### `publish_mode: copy` — keep the workspace copy too

By default a successful task uploads to the publish URI **instead of** the workspace, so downstream consumers reading from `workspace:` no longer find the artefacts there. That's the right trade-off for steps whose outputs are only ever read by the published-results bucket.

For the rare case where the same outputs are consumed *by a downstream step in the same workflow* **and** need to land in the results bucket, set `publish_mode: copy`:

```yaml
  - name: bin
    publish: "{params.results}/{SAMPLE}/bins/"
    publish_mode: copy        # → both .../bins/ in the results bucket and the workspace
```

With `copy`, the worker uploads each output file twice (once to the publish path, once to the workspace path). The default `move` (or omitting the field) keeps today's exclusive-or semantic.

Failed tasks always upload to the workspace only — `publish_mode` is ignored when the task ends in `F`.

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

### Producer vs consumer

The `opportunistic:` flag is a **consumer-side** opt-in: it controls whether *this* workflow looks up cached results in the reuse store. Production happens regardless — every reuse-eligible task (i.e. step not in `untrusted:`) computes a `reuse_key` from its content and is recorded in the reuse store on success.

So a workflow run with `opportunistic: false` (or default) **still seeds the cache** for future opportunistic workflows to consume. There's no "producer mode" to enable; the only opt-out is the `untrusted:` list, which excludes specific steps from contributing reuse keys (and therefore from being reused).

Practical consequence: turn `opportunistic: true` on a fresh run that you want to *reuse* from prior runs. Run with the default elsewhere — it doesn't cost you anything, and your outputs become eligible for reuse later.

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

`skip_if_exists` **takes precedence over opportunistic reuse**: a task declared with `skip_if_exists: true` is excluded from the reuse path entirely, even when the workflow runs with `opportunistic: true`. Such a task is evaluated only against its current publish target via the file-existence check.

Why: `skip_if_exists` is for resource-driven tasks (catalog downloads, host indexes, anything anchored to `{RESOURCE_ROOT}` or a shared publish location). What matters is whether the publish target exists *now*, in *this* resource context — not whether some prior task with the same `reuse_key` happened to succeed in a different workspace. Reusing a stale cache entry whose publish target has since been evicted would silently break downstream consumers that fetch from the publish path.

For all other tasks (no `skip_if_exists`), the reuse check runs first (DB lookup, no I/O). A reuse miss falls through to the regular dispatch path.

### Same-workspace-root constraint

Opportunistic reuse only fires when the cached output and the current task's output share the same `scheme://authority` (e.g. both on `s3://rnd/…`, or both on `azswed://rnd/…`). A `task_reuse` row whose `output_path` lives on a different cloud/region is ignored.

Reusing across regions silently turns intra-region reads into paid cross-region egress on every dependent task that consumes the redirected input. Co-locality is the only signal we have at assign time that the redirect is cheap, so it is required. If you genuinely want to consume a remote artefact, write an explicit import workflow that copies it into the current workspace — that makes the transfer cost visible and one-shot rather than amortised across every dependent.

| Feature | `skip_if_exists` | Opportunistic reuse |
|---|---|---|
| Scope | Single task, output path check | Cross-workflow, content-addressed |
| Check | File existence at output/publish path | Database key lookup |
| Speed | Requires I/O (listing remote files) | Instant (DB primary key) |
| Granularity | Per step | Workflow-level opt-in |
| Precedence | Wins when both are set | Skipped when `skip_if_exists=true` |

## Extending an existing workflow

By default each `scitq template run` of a YAML template creates a new workflow.
You can instead **reconcile the template against an existing workflow** —
declaratively, by identity:

```sh
# Re-run the exact same thing into the same workflow, without looking up its id
# (e.g. after fixing a module the template imports). Resolves your most recent
# run of this template with matching params:
scitq template run --name biomscope --param "bioproject=...,depth=2x20M,location=azure.primary:swedencentral" --continue

# Or target a workflow explicitly by id:
scitq template run --name biomscope --param "bioproject=...,depth=2x20M,location=azure.primary:swedencentral" --extend-workflow 2525

# Re-run only the tasks that failed (no cascade); leave succeeded/running alone:
scitq template run --name biomscope --param "..." --extend-workflow 2525 --retry-failed-only
```

What happens, level by level:

| Level | Identity | Action |
|---|---|---|
| Workflow | `--extend-workflow <id>` (or resolved by `--continue`) | reused, settings untouched |
| Step | `(workflow, step name)` | found-or-created |
| Task | `(step, tag)` | found-or-referenced; a task whose generated command **drifted** (e.g. an imported module changed) is edit-and-retried |

Default mode **cascades**: re-running a drifted task also re-runs its dependents
(their inputs changed). `--retry-failed-only` narrows it to just the failed
tasks (no cascade). Brand-new tags (e.g. more samples) are always added, so this
is also how you feed more inputs into an existing workflow.

`--continue` matches your most recent run of the same template **name** (any
version) with **identical params** (compared canonically), restricted to your
own runs, and errors rather than silently creating a new workflow if there's no
match. Because it matches on identical params, it's for reconciling/retrying the
same run (a template or module fix) — adding a sample changes the params, so use
`--extend-workflow <id>` for that. Run with the same provider/region the
workflow already uses so outputs land in the same workspace.

Full semantics, caveats, and the cascade rule: `specs/workflow_extend.md`, and
[CLI → Extending a workflow](cli.md#extending-an-existing-workflow).

## Chaining workflows

Extending reconciles within one workflow. **Chaining** sequences one workflow
into another — when the parent reaches a terminal status (`S` by default),
the server fires one or more child template runs whose parameters are
mapped from the parent's state.

The most common use is "a step in workflow A produces an unknown number of
outputs that workflow B must fan out over": A's outputs are real files on
storage by the moment B is submitted, so B's iterator does its normal
compile-time enumeration over those URIs — no need for dynamic
task-materialisation inside one workflow.

```yaml
format: 2
name: binning
version: "1.0.0"
description: Cross-sample contig catalog + GPU binning

params:
  project: { type: string, required: true }
  location: { type: provider_region, required: true }
publish_root: "azure://results/{params.project}"

iterate:
  name: sample
  source: uri
  uri: "{params.bioproject}"
  group_by: folder
  fastqs: "*.f*q.gz"

steps:
  - import: binning/semibin2
    publish: true        # → azure://results/{project}/binning/semibin2/

chain:
  - template: bin_qc
    when: "{params.run_qc}"        # optional: only fire if user opted in
    params:
      project:  "{parent.params.project}"
      location: "{parent.params.location}"
      bins_dir: "azure://results/{parent.params.project}/binning/semibin2/"
  - template: notify_failure
    on: failed                      # only fire if parent failed
    params:
      workflow_id: "{parent.workflow_id}"
```

### Entry filters: `when:` and `on:`

A chain entry has two orthogonal filters; both must hold for the child to
fire:

- **`when:`** — same semantics as a step's `when:` (skip if the value is
  falsy). Use it for opt-in / opt-out on a parameter.
- **`on:`** — closed enum (default `succeeded`) gating on the parent's
  terminal status:
  - `succeeded` — fire only when parent ends `S`,
  - `failed` — fire only when parent ends `F`,
  - `always` — fire on either (cleanup / archival chains).

> YAML 1.1 booleanises bare `on:` as `True`. The runner normalises this
> automatically so you can write `on: failed` unquoted in chain entries.

### Param mapping surface (v1)

`params:` is a mapping from child param name to an expression. Mapping
values support the usual `{params.X}` / `{vars.X}` substitutions plus a
narrow `parent.*` namespace resolved server-side at fire time:

| Reference | Resolves to |
|---|---|
| `{parent.workflow_id}` | The parent's workflow id (int). |
| `{parent.workflow_name}` | The parent's workflow name. |
| `{parent.run_by}` | The parent's `run_by` user id (int). |
| `{parent.run_by_username}` | The parent's `run_by` username. |
| `{parent.params.<name>}` | A value from the parent's submitted params. |

Refs that require schema additions (`{parent.publish.<step>}`,
`{parent.workspace_root}`, `{parent.tag}`) return a clear "not in v1" error
pointing at the workaround: pass the URI explicitly via
`{parent.params.X}` and your template's known `publish_root` convention.
See [`specs/workflow_chain.md`](../../specs/workflow_chain.md) for the v2
plan.

### Lifecycle and operator controls

Each `chain:` entry becomes a first-class row in `workflow_chain_entry`
when the parent is submitted, with status `pending`. It progresses to:

- **`fired`** — child submitted; the entry records `child_workflow_id`.
- **`skipped`** — `when:` evaluated false, or `on:` rejected the parent's
  terminal status. Permanent.
- **`failed`** — chain firing itself failed (template not found, param
  resolution error, mapping malformed); `error_message` records why.
- **`cancelled`** — operator cancelled before fire (terminal).
- **`suspended`** — operator paused the entry; can be edited or resumed.

The operator can pause, fix, and resume an armed entry — useful when you
realise mid-run that the child template needs a fix before it fires:

```sh
scitq workflow chain list --workflow-id 2484          # show what's armed
scitq workflow chain suspend --id 7                   # pause entry 7
# ... edit the child template (scitq template upload --force), then:
scitq workflow chain edit --id 7 --param bins_dir=...
scitq workflow chain resume --id 7                    # fires immediately if parent already terminated
```

Re-runs (`--continue` / `--extend-workflow` on the parent) re-fire `fired`
entries against their recorded `child_workflow_id` via `--extend-workflow`
— deterministic idempotency tied to the chain entry's own lineage. Set
`always_new: true` on the entry to opt out and produce a fresh child each
time (e.g. dated archival workflows).

Full design, full lifecycle diagram, and the rerun semantics:
[`specs/workflow_chain.md`](../../specs/workflow_chain.md).
