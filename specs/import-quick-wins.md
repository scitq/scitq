# Quick wins to ease Nextflow/Snakemake import

Small, focused changes to scitq that reduce friction when converting workflows from Nextflow or Snakemake. Ordered by estimated impact/effort ratio.

---

## 1. Expose `$MEM` and `$THREADS` as container environment variables

**Current state:** Only `$CPU` is injected into the Docker container (`client/client.go:213`).

**Problem:** Nextflow uses `${task.memory}`, Snakemake uses `{resources.mem_mb}`. Converted scripts need to reference available memory. Also, Snakemake universally uses `{threads}` — having `$THREADS` as alias for `$CPU` makes converted shell commands more readable without manual editing.

**Change:** In `client/client.go`, add `-e MEM=<mem_gb>` and `-e THREADS=<cpu>` alongside the existing `CPU` env var.

**Effort:** ~5 lines of Go.

---

## 2. Default container at Workflow level

**Current state:** `container` is required on every `workflow.Step()` call. `language` already has a workflow-level default.

**Problem:** Most Nextflow/Snakemake pipelines use the same container for many steps. Converted code repeats `container="same/image:tag"` everywhere.

**Change:** Add optional `container` parameter to `Workflow()`. Make `container` optional in `Step()` — fall back to workflow default.

**Effort:** ~10 lines of Python in `workflow.py`.

---

## 3. Workflow-level `publish_root`

**Current state:** Each step specifies the full publish path: `Outputs(publish="azure://rnd/results/project/")`.

**Problem:** Nextflow's `publishDir` and Snakemake's `protected()` are usually relative to a project-level output directory. Repeating the full URI in every step is verbose and error-prone.

**Change:** Add optional `publish_root` parameter to `Workflow()`. When set, `Outputs(publish=True)` publishes to `{publish_root}/{step_name}/{task_tag}/`, and `Outputs(publish="subdir/")` publishes to `{publish_root}/subdir/`.

**Effort:** ~20 lines of Python.

---

## 4. `Param.path()` type with validation

**Current state:** File paths are declared as `Param.string()` and validated separately with `check_if_file()`.

**Problem:** Both Nextflow and Snakemake have path-typed parameters. A dedicated type would auto-validate and display a file picker in the UI.

**Change:** Add `Param.path(required=True, help="...")` that calls `check_if_file()` automatically during `parse()`.

**Effort:** ~15 lines of Python in `params.py`.

---

## 5. `URI.from_glob()` — simpler file discovery

**Current state:** `URI.find()` is powerful but its API (`group_by`, `field_map`, `event_name`) is complex for simple cases.

**Problem:** Nextflow's `Channel.fromPath("data/*.bam")` and Snakemake's `glob_wildcards("data/{sample}.bam")` are one-liners. Converter-generated code using `URI.find()` is verbose.

**Change:** Add a thin wrapper:
```python
# Returns list of URI strings matching the glob
files = URI.glob("azure://bucket/data/*.bam")

# Returns list of (name, [uris]) grouped by wildcard
samples = URI.glob_groups("azure://bucket/data/{sample}/*.fastq.gz")
```

**Effort:** ~30 lines of Python wrapping existing `URI.find()`.

---

## 6. `inputs=` accept mixed list of outputs from different steps

**Current state:** The type signature accepts `Union[str, OutputBase, List[str], List[OutputBase]]` — but mixing `str` and `OutputBase` in the same list may not work cleanly.

**Problem:** Both Nextflow and Snakemake allow a process/rule to consume inputs from multiple upstream steps. This is common:
```python
# Nextflow: process takes reads from TRIM and reference from INDEX
# Snakemake: input: reads="trimmed/{s}.fq", ref="index/ref.fa"
```

**Change:** Ensure `inputs=[trim.output("fastqs"), index.output("ref")]` works correctly — merging dependencies from both steps and concatenating their URIs. Verify and add test.

**Effort:** Verify existing code, possibly ~10 lines fix.

---

## 7. Step-level `resources` default from Workflow

**Current state:** `resources` (reference data, binaries) must be specified per step.

**Problem:** Many pipelines use the same reference data across multiple steps (e.g., genome index). Repeating `resources=[Resource("azure://ref/genome.tgz", action="untar")]` in every step is tedious.

**Change:** Add optional `resources` parameter to `Workflow()`. Each step inherits it unless overridden.

**Effort:** ~10 lines of Python.

---

## 8. Container name normalization helper

**Current state:** Snakemake often uses `conda:` environments instead of containers. The converter needs to produce Docker equivalents.

**Problem:** There's no automated way to find a Docker image for a conda environment.

**Solution:** Use [mkdocker](https://github.com/gmtsciencedev/mkdocker) (open-source, in `othersources/mkdocker/`). mkdocker makes creating Docker images from conda packages trivial — a 3-line Dockerfile:

```docker
FROM gmtscience/mamba
RUN _conda install fastp=0.23.4
#tag 0.23.4
```

**Change:** When the converter encounters a `conda:` directive in a Snakemake rule, it:
1. Parses the conda YAML to extract package names and versions
2. Generates a mkdocker Dockerfile (e.g. `dockers/fastp`)
3. Emits a `container="{registry}/{tool}:{version}"` in the scitq DSL
4. Prints instructions to build with `mkdocker dockers/fastp`

**Effort:** ~30 lines in the converter tool. mkdocker handles all the Docker complexity.

---

## Summary

| # | Change | Where | Effort | Impact |
|---|---|---|---|---|
| 1 | `$MEM` + `$THREADS` env vars | `client/client.go` | Tiny | High — every converted script benefits |
| 2 | Default container on Workflow | `python/.../workflow.py` | Small | High — reduces boilerplate in most conversions |
| 3 | `publish_root` on Workflow | `python/.../workflow.py` | Small | Medium — cleaner output management |
| 4 | `Param.path()` type | `python/.../params.py` | Small | Medium — cleaner param validation |
| 5 | `URI.glob()` shorthand | `python/.../uri.py` | Small | Medium — simpler file discovery |
| 6 | Mixed inputs from multiple steps | `python/.../workflow.py` | Verify | High — required for multi-input steps |
| 7 | Workflow-level resources | `python/.../workflow.py` | Small | Medium — reduces repetition |
| 8 | Conda → Docker via mkdocker | converter tool | Small | Medium — auto-generates Dockerfiles from conda envs |
