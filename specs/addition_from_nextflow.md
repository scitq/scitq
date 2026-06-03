# YAML model additions inspired by Nextflow

## Motivation

While pressure-testing `python -m scitq2.convert.nextflow` on an example
`binning_fairy.nf` (302 lines, 7 processes, SemiBin2 + Fairy + CheckM2 binning),
several genuinely valuable Nextflow patterns surfaced that scitq's YAML model
either lacks or covers only partially. 

Things we don't do and why:
- channel algebra (`combine by:`, `groupTuple`, `join`): we already have 
the product iterator + `grouped_by` and workflows chains which cover real 
use cases more readably. 

Each addition is described as:
- the NF pattern that motivates it
- what scitq covers today
- the proposed YAML surface
- the server / runner touchpoints
- open questions / edge cases
- a rough effort estimate

A recommended implementation order is at the end. None of these features
depend on each other to ship; E composes with A and F' if those land first
but doesn't require them.

---

## A — Retry with resource escalation

### What NF does

```groovy
process semibin2_train_self {
    cpus 16
    memory { 40.GB * task.attempt }
    errorStrategy 'retry'
    maxRetries 1
    // ...
}
```

The `memory` closure is re-evaluated on each attempt. Attempt 1 asks for
40 GB; if the task is killed (OOM), the retry clone asks for 80 GB. This is
the standard nf-core pattern for OOM-prone steps (assemblers, binners,
profilers — semibin, megahit, spades, sourmash, kraken).

### What scitq covers today

- Retry exists: a failed task is cloned (`edit_and_retry`) and the clone
  runs from scratch.
- `task_spec.mem` / `cpu` / `disk` are static scalars. The clone inherits
  the same shape.
- A worker that ran the first attempt successfully holds the same shape; a
  fresh worker for the clone is recruited with the same shape.

### Proposal

`task_spec.cpu` / `mem` / `disk` can be either:

- a scalar (today's behaviour, unchanged), or
- a **list of scalars** indexed by attempt number, with the last value used
  for any attempt beyond the list length:

```yaml
steps:
  - name: semibin2_train_self
    task_spec:
      cpu: 16
      mem: [40, 80, 160]     # GB; attempt 1, 2, 3+
      disk: 200
```

`max_retries` (existing field on `task_spec`) caps the curve in the obvious
way: `len(curve) - 1` retries are enough to reach the last value. Asking
for more retries than curve length means "stay on the last value". Asking
for fewer retries than curve length is fine — the unused tail is dead
code, but harmless.

Multiplicative shorthand (open question, not in initial scope): `mem: 40,
mem_attempt_factor: 2` would generate `[40, 80, 160, …]` lazily. Skip
until someone asks; lists are clearer.

### Touchpoints

- **YAML schema**: `task_spec.cpu`/`mem`/`disk` accept `Union[int, float,
  List[int|float]]`. Validation: list must be non-empty, monotonically
  non-decreasing (warn otherwise — that's almost always a mistake).
- **Task model** (DB): the curve is part of the *step* definition, not the
  task — the recruiter looks it up by step. Task carries its current
  `attempt` integer (which already exists for `task.attempt`-style retry
  accounting).
- **Recruiter**: when promoting an `A`→`C` retry clone, re-evaluate the
  required (cpu, mem, disk) at the new attempt index. If the assigned
  worker no longer meets the requirement, return the task to the
  recruitment pool (this composes with existing edit-and-retry: the clone
  is a fresh task from the scheduler's POV).
- **UI**: surface the current attempt's spec on the task detail view
  ("running with 80 GB — attempt 2 of curve [40, 80, 160]").

### Open questions

- **Which failure classes advance the curve.** OOM and timeout are the
  intended triggers. Eviction (worker reclaimed by the provider,
  spot-instance preemption, manual `scitq worker delete`) is *not* — the
  task failed for reasons unrelated to its resource shape. Same for
  network/upload failures. The retry clone of an evicted task must
  inherit the same attempt index, not advance. Specify the per-class
  rule in the spec and surface it on the failed task (`failure_class:
  oom | timeout | eviction | network | other`).

### Effort

Medium. ~2 days including tests and UI surfacing.

---

## B — Publish: `copy` mode and `|rename:` action

### What NF does

```groovy
process checkm2 {
    publishDir "${params.output_dir}/${sample_name}_semibin2",
        mode: 'copy',
        saveAs: { filename -> 'checkm2_quality_report.tsv' }
}
```

Two pieces: `mode: 'copy'` (output stays in the workdir *and* lands at the
publish path) and `saveAs:` (per-file rename on the way out).

### What scitq covers today

`publish:` is a destination folder URI (templated against params/iter), or
`true` to mean `<publish_root>/<step_name>/` — already per-task via the
iter context. The semantic is **move**: a successful task uploads to
`publish` *instead of* the workspace, so downstream steps that need the
data go fetch it from `publish`. Renaming exists for resources via
`|mv:<name>` but that wraps the download into a named subdirectory; it
isn't a per-file rename. There is no `|rename:` today.

### Proposal

Two narrow additions.

**1. `publish_mode: copy | move`** at step level (default: `move`,
today's behaviour). `copy` makes the task upload to *both* the workspace
and the publish path. Use case: a step whose output is consumed by a
downstream step in the same workflow *and* needs to land in the
project's final-results bucket. Today the workflow author has to choose:
publish (and force downstream consumers to fetch from results storage)
or workspace-only (and lose the published copy). `copy` keeps both for
that step.

This is a rare case; the default stays `move` because the workspace copy
is usually wasted disk after a successful publish.

```yaml
steps:
  - name: bin
    publish: "{params.results}/{sample.id}/bins/"
    publish_mode: copy            # downstream qc reads from workspace; results bucket also gets the bins
```

**2. `|rename:<perl-pattern>` action**, usable on both `publish:` and
`resource:` URIs. Perl-style substitution syntax (matching the Unix
`rename` utility):

```yaml
steps:
  - name: checkm2
    publish: "{params.results}/{sample.id}/qc/|rename:s/quality_report/checkm2_quality_report/"
```

The pattern applies to each file basename being uploaded (for `publish:`)
or downloaded (for `resource:` and inputs). Supports the standard `s///`
form with optional flags (`g` for global, `i` for case-insensitive).
Capturing groups available with `$1`, `$2`, …

Composes with the existing `|mv:<name>`, `|untar`, `|gunzip` action chain
(`|rename:` should run *before* `|mv:` since rename operates on file
names and mv operates on the containing directory).

### Touchpoints

- **YAML schema**: new `publish_mode:` field at step level (enum: `move`
  default, `copy`); `|rename:` accepted in the URI-actions tokenizer
  alongside `|mv:`, `|untar`, `|gunzip`.
- **Worker upload code** (`client/` final-upload path): apply the
  rename to each file's basename before upload; when `publish_mode:
  copy`, also write to the workspace path on success.
- **Worker download code** (`fetch/`): apply rename to each downloaded
  file before placing it in `/input/` or `/resource/`.
- **Action chain order**: document `|rename:` runs first, then `|mv:`,
  then `|untar`/`|gunzip`.

### Actions-on-target rule

Actions always apply to the **target** of the transfer, with no
remote/local asymmetry. For `resource:` and `inputs:` the target is
local (so `|untar` / `|gunzip` are valid in addition to `|mv:` / `|rename:`).
For `publish:` the target is remote (so only `|mv:` and `|rename:` are
valid; `|untar` / `|gunzip` are rejected at compile). Same syntax, same
mental model, different scope of valid actions.

### Open questions

- **Collision after rename.** Two source files mapping to the same
  destination name. Fail the upload (or download) explicitly rather than
  silent overwrite.

### Effort

Small. ~half a day for `publish_mode: copy`; ~half a day for the
`|rename:` action. ~1 day total.

---

## C — TSV/CSV-driven `iterate:`

### What NF does

```groovy
Channel.fromPath(params.samples_file)
    .splitCsv(header: true, sep: '\t')
    .map { row ->
        tuple(row.sample_name, file(row.assembly_file))
    }
    .set { assemblies_ch }
```

Read a TSV with named columns. Bind each row to per-task context. The
columns can include URIs (raw paths or `file()`-wrapped paths) that
later steps reference as inputs.

This is the **default** sample-discovery shape in bioinformatics:
nf-core's `samplesheet.csv`, Snakemake's `samples.tsv`, every internal
pipeline at every wet lab.

### What scitq covers today

`iterate:` discovers from URI globs grouped by folder / regex / extension.
A sample is a set of files at a URI prefix. Per-sample *metadata* from a
sidecar TSV isn't expressible — you have to put it in URIs (which mostly
doesn't work for non-file data like depth, library type, or platform).

### Proposal

```yaml
iterate:
  name: sample
  source: tsv               # new value alongside today's `source: uri`
  uri: "{params.samples_file}"
  key: sample_name          # column to use as iter key
  sep: "\t"                 # optional; defaults to tab if file ends in .tsv, comma if .csv
  # All other columns auto-exposed as {sample.<column_name>}
```

Per-task references:

```yaml
steps:
  - name: semibin2_split_contigs
    inputs:
      - "{sample.assembly_file}"        # column value is a URI; auto-fetched
    command: |
      SemiBin2 split_contigs -i /input/$(basename {sample.assembly_file}) -o {sample.id}_split

  - name: fairy_sketch
    inputs:
      - "{sample.fastq_dir}/*_1.fastq.gz"   # glob templated from a column
      - "{sample.fastq_dir}/*_2.fastq.gz"
    command: |
      fairy sketch ...
```

Rules:

- `key:` column must be present and unique across rows; defaults to the
  first column.
- Other columns are exposed as `{sample.<col>}`. Values are strings (TSV is
  text); type coercion is the step's responsibility — except that
  `inputs:` entries beginning with a known scheme (`s3://`, `gs://`,
  `https://`, `azure://`, `local://`) are recognised as URIs and fetched.
- A column whose value is missing on a given row resolves to the empty
  string; `when: "{sample.preqc_done}"` then naturally gates on
  presence.

### Touchpoints

- **Server iterator path**: new "TSV source" reader. Fetches the file at
  iteration time (URI fetch via the same rclone path used elsewhere),
  parses with stdlib `csv`, materialises one iter context per row.
- **YAML parser**: `iterate.source: tsv` accepted alongside `uri`.
- **Re-fetch on `--continue`**. The TSV is re-read every run. If the
  content changed, the iter set changes; new rows produce new tasks,
  removed rows are no-ops, modified rows go through the standard cascade
  (command/inputs changed → edit-and-retry, downstream invalidated).
  The user's intent when they edit the file is to change the task set;
  if they want it stable, they leave the file alone. The slightly
  awkward case is the UI: re-launching from the web has to re-upload an
  unchanged TSV. Acceptable; surface a "reuse last TSV" affordance
  later.
- **Per-row downloads**: columns whose values are URIs and which appear
  in `inputs:` follow the existing fetch pipeline.

### Open questions

- **Local vs remote TSV transport.** Sample sheets are usually small
  text files that the user *edits locally* between runs — exactly the
  shape where putting them on a remote URI is awkward. Remote URIs are
  the right model for large immutable inputs (reference data, raw
  reads); a hand-edited "list of orders" is the opposite. So `uri:`
  should accept a local path too, and the python runner ships the file
  to the server. Open: which transport? Inlining the rows into the
  `RunTemplateRequest` (gRPC) is simplest if the file is small, but
  caps total size; a separate side-channel upload (gRPC stream or
  HTTPS POST to an upload endpoint) is more general. Pick one and
  document the size threshold (or just pick the streaming path and
  forget the threshold).
- **Multi-source join.** Pipelines that read two TSVs (e.g. `samples`
  plus a separate `mappings` file describing cross-sample relationships
  for multi-coverage binning) are closer to channel algebra than to a
  sample sheet, and are best expressed with the existing `product`
  iterator + `grouped_by`. Document the pattern in
  `convert-nextflow.md`; don't extend C to support a multi-source
  iterator natively.

### Effort

Medium. ~1.5–2 days including tests, docs, and the converter teach-back
(the Nextflow converter should emit `source: tsv` whenever it sees the
`splitCsv(header: true)` pattern).

---

## E — Dynamic `task_spec` from iter context

### What NF does

```groovy
process assemble {
    memory { row.depth_gb > 100 ? 256.GB : 64.GB }
    cpus   { row.depth_gb > 100 ?  32   :  16 }
    // ...
}
```

Per-task resource shape driven by sample metadata. Critical for variable
sample-size workloads (assembly is the canonical case: a 200 GB sample
needs an entirely different worker class than a 5 GB sample).

### What scitq covers today

`task_spec.cpu` / `mem` / `disk` are static numbers per step. Heuristic
workaround: split the iter into "big" / "small" with `iterate.filter`,
run two parallel step instances with different specs. Awkward and loses
the single-DAG view.

### Proposal

`task_spec:` accepts a structured `cond:` block that branches the whole
spec on an expression evaluated per task against iter context:

```yaml
steps:
  - name: assemble
    task_spec:
      cond: sample.depth_gb > 100
      true:
        cpu: 32
        mem: 256
      false:
        cpu: 16
        mem: 64
      disk: "{sample.depth_gb * 4 + 50}"   # outside the cond — applies to both branches
```

Why a structured block rather than free-form expressions in each field:
unrestricted templates (`mem: "{sample.depth_gb * 2 + 8}"`) produce a
spec per task in the worst case and fragment recruitment into
1-flavor-per-task — directly at odds with scitq's
cost-oriented batching. A `cond:` block forces the author to declare a
finite set of buckets; the recruiter recruits a flavor per branch, not
per sample.

Branches may declare any subset of `cpu`/`mem`/`disk` (and per-attempt
curves per A); fields outside the branches apply uniformly. The same
restricted evaluator as F' powers `cond:`.

Composes with A:

```yaml
task_spec:
  cond: sample.depth_gb > 100
  true:
    cpu: 32
    mem: 256
    disk: [600, 800]
  false:
    cpu: 16
    mem: 64
    disk: 400
```

Nested `cond:` blocks are allowed when more than two buckets are needed;
deep nesting is a signal that the buckets should be reshaped (e.g. via a
TSV column that encodes the class directly).

### Touchpoints

- **YAML parser**: `task_spec` accepts either today's flat shape or a
  `{cond, true, false, …}` shape. The non-branch keys merge with each
  branch's spec.
- **Recruiter**: resolves the per-task branch at task-creation time and
  groups tasks by resolved spec for recruitment.
- **Validation**: each branch must produce a complete, valid spec
  (after merging with the outer fields). Compile-time check.

### Resolution order under combined cond + curve

When a branch declares a per-attempt curve (per A), resolution is
**branch first, then attempt index**. The branch decides which curve
applies; the attempt index then picks a value from it. A retry that
advances the curve stays in the same branch — `cond:` is data-driven
and the input data doesn't change between attempts.

### Effort

Medium. ~2 days. The expression evaluator extension (F') and the
attempt-curve work (A) overlap meaningfully — landing those first makes E
much smaller.

---

## F' — Comparison & boolean operators in `when:` (and `cond()`)

### Background

Step-level `when:` already exists (`yaml_runner.py:1540–1550`), is
evaluated per iteration with iter context, and is used in production
(e.g. `when: "{params.oral}"`). What's missing is **expressive enough
conditions** for data-driven gating.

The current expression evaluator `_eval_arithmetic` (yaml_runner.py:97)
allows only:

```
+ - * / ( ) , max min int
```

So `when: "{sample.read_type == 'long'}"` template-substitutes to
`"long == 'long'"`, which is a non-empty string and therefore truthy
regardless of the comparison's truth value.

### Proposal

Widen the allowlist to include comparison, regex-match, and boolean
operators (still running under the same restricted `eval` with
`__builtins__` disabled and a whitelisted local-name set):

```
==  !=  <  <=  >  >=
~                       # regex match, e.g. {sample.platform ~ '^illumina'}
and  or  not
in  not in
```

`~` is a binary operator: `<value> ~ <pattern>` is true iff `re.search`
matches. Negate with `not (… ~ …)`. The pattern is a Python regex
string; substitution-syntax (`s/…/…/`) is reserved for the `|rename:`
action (B).

This unlocks per-row gating without any new YAML surface:

```yaml
steps:
  - name: align_long
    when: "{sample.read_type == 'long'}"
    # ...

  - name: profile_species
    when: "{sample.n_reads >= 1_000_000}"
    # ...

  - name: extended_qc
    when: "{params.profile in ('full', 'extended')}"
    # ...
```

The same evaluator backs `cond()` and (per E) `task_spec` expressions, so
this change benefits all three uniformly.

### Touchpoints

- `_eval_arithmetic` → `_eval_expression`: extend allowed-chars and
  whitelisted operators. Stay restricted: no attribute access, no
  function calls except the existing `max`/`min`/`int`, no
  list/dict/set literals.
- **String values**: `==` against a quoted string needs to work
  (`{sample.read_type == 'long'}`). The template substitution must
  preserve quotes around string values, *or* the evaluator must accept
  bare identifiers as string-literal sugar. Cleaner: substitute strings
  with their quoted form (`'long'`), numbers as bare (`12345`).
- **Tests**: add a focused unit test suite covering comparison,
  boolean composition, mixed-type expressions, and refusal of any new
  attack surface (no `__builtins__` access leaks through).

### String coercion at boundaries

TSV values are always strings. The evaluator auto-coerces
numeric-looking strings (`"42"`, `"1_000_000"`, `"3.14"`) when a
numeric operator is applied, so `{sample.n_reads >= 1_000_000}` Just
Works. `int()` (already in the allowlist) is available when the user
wants the coercion to be explicit and to fail loudly on non-numeric
input.

### Effort

Small. ~half a day including tests.

---

## Recommended implementation order (to delete when implemented)

1. **F'** — half-day, unlocks expressive gating *and* preconditions E.
2. **C** — TSV iteration. Highest user-visible payoff per day spent;
   removes a whole class of "convert your sample sheet first" friction.
3. **A** — retry escalation. Real-world OOM-prone steps benefit
   immediately; doesn't touch the iter / scheduling model deeply.
4. **B** — richer publish. Small standalone change; can land any time.
5. **E** — dynamic task_spec. Largest blast radius (recruiter
   bucketing); land after A so per-attempt + per-task share a single
   resolution path.

Total scope: ~6–7 engineer-days for all five, sequenced. F' and B can
land in a single afternoon if a quick win is wanted.
