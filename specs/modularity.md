# Modularity — Reusable Step Definitions

## Problem

Every real nf-core pipeline is modular:

```groovy
include { FASTP } from './modules/nf-core/fastp/main'
include { BOWTIE2_ALIGN } from './modules/nf-core/bowtie2/align/main'
```

The converter can't follow these imports today. But the deeper issue is that scitq itself has no modularity concept — every workflow template is a monolithic script. This means:

1. **Converter limitation**: can't import nf-core modules without first merging them into one file
2. **DSL limitation**: common steps (fastp, bowtie2, kraken2...) are copy-pasted between templates
3. **Maintenance burden**: a fix to a step definition must be replicated in every template that uses it

Nextflow solved this with nf-core/modules. Snakemake solved it with snakemake-wrappers. scitq needs an equivalent.

## Design

### Principle

A **StepModule** is a reusable function that adds one or more steps to a workflow. It's just a Python function — no new DSL construct needed. Modules are plain `.py` files that can be imported with standard Python `import`.

### What a module looks like

```python
# scitq2_modules/fastp.py
from scitq2 import *

def fastp(workflow, sample, *, paired=True, worker_pool=None):
    """Quality trimming with fastp."""
    return workflow.Step(
        name="fastp",
        tag=sample.sample_accession,
        container="staphb/fastp:1.0.1",
        command=fr"""
        . /builtin/std.sh
        fastp \
            --in1 /input/*_1.f*q.gz --in2 /input/*_2.f*q.gz \
            --out1 /output/{sample.sample_accession}.1.fastq.gz \
            --out2 /output/{sample.sample_accession}.2.fastq.gz \
            --thread ${{CPU}} \
            --json /output/{sample.sample_accession}.json
        """,
        inputs=sample.fastqs,
        outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
        task_spec=TaskSpec(cpu=4, mem=8),
        worker_pool=worker_pool,
    )
```

### How a workflow uses modules

```python
from scitq2 import *
from scitq2_modules.fastp import fastp
from scitq2_modules.bowtie2 import bowtie2_host_removal
from scitq2_modules.kraken2 import kraken2

class Params(metaclass=ParamSpec):
    input_dir = Param.string(required=True)
    kraken_db = Param.path(required=True)
    location = Param.provider_region(required=True)

def metagenomics(params: Params):
    workflow = Workflow(
        name="metagenomics",
        version="1.0.0",
        description="Metagenomic classification pipeline",
        language=Shell("bash"),
        worker_pool=WorkerPool(W.cpu >= 8, W.mem >= 30),
        provider=params.location.provider,
        region=params.location.region,
    )

    samples = URI.find(params.input_dir, group_by="folder", filter="*.f*q.gz",
        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})

    for sample in samples:
        qc = fastp(workflow, sample)
        clean = bowtie2_host_removal(workflow, sample, inputs=qc.output("fastqs"))
        kraken2(workflow, sample, inputs=clean.output("fastqs"), db=params.kraken_db)

run(metagenomics)
```

### Key observations

- A module is **just a Python function** that takes `workflow` and returns a `Step`
- No new DSL constructs — uses the existing `workflow.Step()` API
- Standard Python imports — works with packages, relative imports, pip-installable collections
- The `sample` object is passed explicitly — no implicit channel magic
- Extra options (paired, worker_pool, custom container) are keyword arguments

### This is the recommended standard

**All new workflows should use this pattern.** Step logic should be written as importable functions so it can be reused across templates. Inline `workflow.Step(...)` with long shell commands should be reserved for one-off steps that are truly specific to a single workflow.

This convention is what makes inter-workflow imports possible: a fastp module written once can be shared across every metagenomics, genomics, or transcriptomics template.

The only infrastructure needed is:

1. A standard package for shared modules (`scitq2_modules`, shipped with scitq)
2. A convention for module function signatures
3. Server-side support for importing modules in uploaded templates

## Implementation

### Phase 1: Convention + starter modules (no engine changes)

Define the convention and create a `scitq2_modules` package with common steps:

```
python/src/scitq2_modules/
    __init__.py
    fastp.py
    bowtie2.py
    samtools.py
    kraken2.py
    metaphlan.py
    seqtk.py
    multiqc.py
```

Each module follows the signature pattern:

```python
def tool_name(workflow, sample, *, inputs=None, worker_pool=None, **kwargs) -> Step:
```

Where:
- `workflow`: the Workflow object
- `sample`: the current sample (has `.sample_accession`, `.fastqs`, etc.)
- `inputs`: input from previous step (default: `sample.fastqs` for first step)
- `worker_pool`: optional override
- `**kwargs`: tool-specific options
- Returns: the Step object (for chaining with `.output()`)

### Phase 2: Server-side module support

Currently, uploaded templates run in an isolated venv. For modules to work in templates uploaded via UI/CLI:

Option A: **Bundle modules with the DSL package** — modules ship as part of `scitq2`. Simple, but limits user customization.

Option B: **Module path in config** — `scitq.module_path` setting pointing to a directory of `.py` modules. The server adds this to `PYTHONPATH` when running templates. Users upload modules separately.

Option C: **Module upload** — extend the template upload to accept module files. Stored in `script_root/modules/` and added to path.

Recommendation: **Option A for common tools + Option B for user modules**. The standard modules are part of `scitq2_modules` (installed with pip). User-specific modules go in a configurable path.

### Phase 3: Converter integration

Update the Nextflow/Snakemake converters to:

1. Recognize common tools (fastp, bowtie2, samtools, kraken2...) and emit module imports
2. Generate `fastp(workflow, sample)` calls instead of inline `workflow.Step(...)` blocks
3. For unknown processes, generate inline steps as today

This makes converted pipelines much shorter and more idiomatic:

```python
# Before (inline, 30 lines per step)
fastp = workflow.Step(name="fastp", tag=..., container=..., command=fr"...", ...)

# After (module, 1 line per step)
fastp = fastp(workflow, sample)
```

## Converter changes: following `include` statements

For the converter to handle nf-core modular pipelines:

### Approach: Pre-merge

Before converting, resolve all `include` statements by reading the referenced files:

```python
def resolve_includes(main_nf: str, base_dir: str) -> str:
    """Recursively resolve include statements and merge into a single text."""
    for match in re.finditer(r"include\s*\{([^}]+)\}\s*from\s*'([^']+)'", main_nf):
        names = [n.strip() for n in match.group(1).split(';')]
        path = match.group(2)
        # Resolve relative path
        module_file = os.path.join(base_dir, path.replace('./', ''), 'main.nf')
        if os.path.exists(module_file):
            module_text = open(module_file).read()
            main_nf = main_nf.replace(match.group(0), module_text)
    return main_nf
```

The converter first merges all includes into a single text, then parses as before.

### Approach: Module mapping

Alternatively, maintain a mapping of known nf-core module names to scitq2_modules:

```python
NFCORE_MODULE_MAP = {
    'FASTP': 'scitq2_modules.fastp.fastp',
    'BOWTIE2_ALIGN': 'scitq2_modules.bowtie2.bowtie2_align',
    'KRAKEN2_KRAKEN2': 'scitq2_modules.kraken2.kraken2',
    'SAMTOOLS_SORT': 'scitq2_modules.samtools.samtools_sort',
    ...
}
```

When the converter sees `include { FASTP }`, it emits `from scitq2_modules.fastp import fastp` instead of trying to parse the module file.

Recommendation: **Both approaches**. Use the module map for known nf-core modules (cleaner output), fall back to pre-merge for unknown modules.

## Module collection — starter list

Based on common bioinformatics tools and real scitq workflows (biomscope, gpredomics, eskrim):

| Module | Tool | Purpose |
|---|---|---|
| `fastp` | fastp | Quality trimming |
| `bowtie2` | bowtie2 + samtools | Alignment / host removal |
| `bwa` | bwa + samtools | Alignment |
| `samtools` | samtools | BAM manipulation |
| `kraken2` | kraken2 | Taxonomic classification |
| `metaphlan` | MetaPhlAn | Species profiling |
| `seqtk` | seqtk | Read subsampling |
| `multiqc` | MultiQC | Report aggregation |
| `hermes` | hermes | Alignment + counting (GMT custom) |
| `gpredomics` | gpredomics | ML classification (GMT custom) |

## Summary

The modularity solution is:
1. **No new DSL constructs** — just Python functions + imports
2. **Standard package** (`scitq2_modules`) with common bioinformatics steps
3. **Server path config** for user-contributed modules
4. **Converter integration** — emit module calls for known tools, pre-merge for unknown
5. **Incremental** — works today with manual imports, formalized with the package
