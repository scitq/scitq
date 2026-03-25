# Modularity — Reusable Step Definitions

## Problem

Every real nf-core pipeline is modular. scitq workflow templates today are monolithic scripts. This causes:

1. **Copy-paste between templates**: common steps (fastp, bowtie2, kraken2...) are duplicated
2. **Maintenance burden**: a fix to a step must be replicated everywhere
3. **Converter limitation**: can't import nf-core modules without merging them first
4. **No private module sharing**: teams can't share internal tools (hermes, biomscope) across templates

## Design

### Principle

A module is a **Python function** that calls `workflow.Step()` and returns the Step. Standard Python imports. No new DSL constructs.

```python
def fastp(workflow, sample, *, inputs=None, paired=True, worker_pool=None) -> Step:
    return workflow.Step(name="fastp", tag=sample.sample_accession, ...)
```

### Standard signature

All modules follow this pattern:

```python
def tool_name(workflow, sample, *, inputs=None, worker_pool=None, **kwargs) -> Step:
```

- `workflow`: the Workflow object
- `sample`: sample object (has `.sample_accession`, `.fastqs`)
- `inputs`: from previous step (default: `sample.fastqs`)
- `worker_pool`: optional override
- `**kwargs`: tool-specific options (depth, reference, etc.)
- Returns: the Step (for chaining with `.output()`)

Fan-in modules (after the sample loop) drop the `sample` parameter:

```python
def multiqc(workflow, *, inputs, worker_pool=None) -> Step:
```

### Usage in Python DSL

```python
from scitq2 import *
from scitq2_modules.fastp import fastp
from gmt_modules.hermes import hermes   # private module

for sample in samples:
    qc = fastp(workflow, sample)
    clean = hermes(workflow, sample, inputs=qc.output("fastqs"))
```

### Usage in YAML pipelines

```yaml
steps:
  - module: fastp
  - module: gmt_modules.hermes
    inputs: fastp.fastqs
```

The YAML runner resolves module names via Python's standard import system.

## Three tiers of modules

### Tier 1: Core modules (`scitq2_modules`)

**What**: Common bioinformatics tools. Ships embedded in the server binary (as part of the Python DSL tgz).

**Where**: `python/src/scitq2_modules/` in the scitq source tree.

**Always available**: Installed automatically when the server bootstraps its venv. Version always matches the server's proto and DSL — no compatibility issues.

**Current catalog**: fastp, bowtie2, bwa, samtools, kraken2, seqtk, multiqc, salmon, star, minimap2, etc. (28 modules).

### Tier 2: Private module packages (`extra_packages`)

**What**: Organization-specific tools. Distributed as pip-installable Python packages from private Git repos.

**Where**: Defined in `scitq.yaml`:

```yaml
scitq:
  extra_packages:
    - "git+ssh://git@git.gmt.bio/gmt-scitq-modules.git"
    - "git+ssh://git@git.gmt.bio/client-specific-modules.git@v2.1"
```

**How it works**:

1. The server bootstrap installs `scitq2` (embedded, as today)
2. Then installs each `extra_packages` entry via pip into the same venv
3. Templates can `from gmt_modules.hermes import hermes`
4. Locally, developers do `pip install -e /path/to/gmt-scitq-modules` in their dev venv

**Package structure** (example):

```
gmt-scitq-modules/
    pyproject.toml
    src/
        gmt_modules/
            __init__.py
            hermes.py
            biomscope.py
            nsat.py
```

**Key rule**: private module packages must **not** depend on `scitq2` in their `pyproject.toml`. The server provides `scitq2` — the module just imports from it. This is the "host plugin" pattern.

```toml
# gmt-scitq-modules/pyproject.toml
[project]
name = "gmt-scitq-modules"
version = "1.0.0"
dependencies = []   # no scitq2 dependency — provided by the server
```

This is critical: it means the server can freely evolve its proto and DSL. Private modules call the Python API (`workflow.Step()`, `Outputs`, etc.) which stays stable. The proto wire format is the server's internal concern.

**Versioning**: Pin versions in `extra_packages` when needed:

```yaml
extra_packages:
  - "git+ssh://git@git.gmt.bio/gmt-scitq-modules.git@v1.2.0"
```

### Tier 3: Inline modules (template-local)

**What**: One-off step definitions specific to a single template. Not importable by other templates.

**Where**: Defined directly in the Python template or as a custom step in YAML.

**Python**:
```python
def my_custom_analysis(workflow, sample, *, inputs=None):
    return workflow.Step(
        name="custom",
        tag=sample.sample_accession,
        command=fr"...",
        inputs=inputs,
        ...
    )
```

**YAML**:
```yaml
steps:
  - name: custom_analysis
    container: ubuntu:latest
    command: |
      cat /input/*.txt > /output/merged.txt
    inputs: kraken2.report
    grouped: true
```

No packaging, no imports. For truly single-use steps.

## Version compatibility

### The core constraint

`scitq2` (the Python DSL) is **embedded in the server binary**. It is installed from a tgz bundled at compile time. This guarantees:
- The DSL always matches the proto
- The DSL always matches the server's gRPC API
- No version mismatch possible between server and DSL

This is intentional and must not change. Publishing `scitq2` as a standalone pip package would break this guarantee.

### How private modules stay compatible

Private modules depend on the **Python-level API**, not the proto:

| API surface | Stability | Used by modules |
|---|---|---|
| `workflow.Step(name, command, container, ...)` | Stable | Yes |
| `Outputs(name="glob")` | Stable | Yes |
| `TaskSpec(cpu, mem)` | Stable | Yes |
| `cond((bool, value), default=value)` | Stable | Yes |
| Proto field numbers, gRPC wire format | Changes freely | No |
| `Scitq2Client` internal methods | Changes freely | No |

As long as the `Workflow.Step()` signature and `Outputs`/`TaskSpec` constructors remain backward-compatible (adding optional parameters is fine, removing/renaming is not), private modules work across server upgrades without changes.

**Rule**: If a server upgrade changes the module-facing API (Step signature, Outputs, etc.), all `extra_packages` must be updated. This should be rare and announced in release notes.

### Local development

For local development and testing (outside the server):

```sh
# Create a venv with the matching DSL version
make venv VENV=/path/to/venv

# Install your private modules (editable for development)
source /path/to/venv/bin/activate
pip install -e /path/to/gmt-scitq-modules

# Test
python my_pipeline.py --values '...' --dry-run
# or
python -m scitq2.yaml_runner pipeline.yaml --values '...' --dry-run
```

The `make venv` command installs `scitq2` from the current source tree. The private modules are installed alongside it. Both environments (local venv and server venv) run the same DSL code.

## Server bootstrap changes

### Current flow

```
server starts
  → creates venv at script_venv path
  → pip install scitq2 from embedded tgz
  → imports scitq2 to verify
  → ready
```

### New flow

```
server starts
  → creates venv at script_venv path
  → pip install scitq2 from embedded tgz
  → for each entry in scitq.extra_packages:
      pip install <entry>
  → imports scitq2 to verify
  → ready
```

### Configuration

```yaml
scitq:
  # ... existing settings ...
  extra_packages:         # optional, default: empty
    - "git+ssh://git@git.gmt.bio/gmt-scitq-modules.git"
    - "/opt/local-modules"    # local path, pip install -e
```

### Implementation

In the server's Python bootstrap code (Go side), after installing scitq2:

```go
for _, pkg := range cfg.Scitq.ExtraPackages {
    cmd := exec.Command(venvPip, "install", pkg)
    if err := cmd.Run(); err != nil {
        log.Printf("⚠️ Failed to install extra package %s: %v", pkg, err)
        // Warning, not fatal — the server can still run without private modules
    }
}
```

Failures are warnings, not fatal errors. The server runs without private modules if a package fails to install (e.g., Git SSH key not available).

## YAML runner module resolution

The YAML runner resolves module names through Python's import system:

```yaml
steps:
  - module: fastp                              # → from scitq2_modules.fastp import fastp
  - module: bowtie2_align.bowtie2_host_removal # → from scitq2_modules.bowtie2_align import bowtie2_host_removal
  - module: gmt_modules.hermes                 # → from gmt_modules.hermes import hermes
```

Resolution order (Python's standard `sys.path`):
1. **Current directory** (for template-local modules)
2. **Private packages** installed via `extra_packages`
3. **Core modules** (`scitq2_modules`, shipped with scitq2)

No custom import machinery — standard Python.

## Converter integration

### Module mapping for nf-core

When the converter encounters a known nf-core process, it emits a module import:

```python
NFCORE_MODULE_MAP = {
    'FASTP': 'scitq2_modules.fastp.fastp',
    'BOWTIE2_ALIGN': 'scitq2_modules.bowtie2_align.bowtie2_align',
    'KRAKEN2_KRAKEN2': 'scitq2_modules.kraken2_kraken2.kraken2',
    'SAMTOOLS_SORT': 'scitq2_modules.samtools_sort.samtools_sort',
    ...
}
```

For unknown processes: inline `workflow.Step(...)` as today.

### Pre-merge for unknown includes

When the converter encounters `include { UNKNOWN_TOOL }` with no mapping:

1. Try to find and read the referenced file
2. Merge it into the main file
3. Convert the inlined process as a custom step

## What Nextflow does (for comparison)

- **Central registry**: nf-core/modules on GitHub (800+ modules)
- **CLI**: `nf-core modules install fastp` → copies into `modules/nf-core/fastp/main.nf`
- **Private**: `modules/local/my_tool/main.nf` in the project directory
- **Versioning**: `modules.json` tracks installed versions
- **Runtime**: always local files, no remote imports

scitq's approach differs in using **pip packages** instead of file copying. This is more Pythonic and gives versioning, dependency management, and editability for free. The trade-off is that users need basic Python packaging knowledge for private modules (a `pyproject.toml` with 5 lines).

## Implementation plan

### Phase 1: Core modules ✅ (done)

- `scitq2_modules` package with 28 modules
- Convention established (function signature, naming)
- Ships embedded with the server
- YAML runner resolves modules via import

### Phase 2: Private module packages

1. Add `extra_packages` config field to server config struct
2. Server bootstrap: `pip install` each extra package after scitq2
3. Document the private module package pattern
4. Test with a sample private module package

### Phase 3: Converter integration

1. Module mapping table for nf-core process names
2. Converter emits `from scitq2_modules.x import y` when match found
3. Pre-merge fallback for unknown includes

## FAQ

**Q: Can I use a private module locally without a server?**
A: Yes. `make venv` + `pip install -e /path/to/your-modules`. Then `python template.py --dry-run` or `python -m scitq2.yaml_runner pipeline.yaml --dry-run`.

**Q: What if my private module uses a function that changed in a new server version?**
A: The module-facing API (Step, Outputs, TaskSpec) is stable. If it ever changes, we'll document it. Proto changes don't affect modules.

**Q: Can I have multiple private module packages?**
A: Yes. List them all in `extra_packages`. They're independent pip packages.

**Q: What if two packages define the same module name?**
A: Python's import precedence applies. First match wins. Use distinct package names to avoid collisions (e.g., `gmt_modules`, `client_modules`).

**Q: Do I need to restart the server to update private modules?**
A: Yes. The server installs packages at bootstrap. To update, `pip install --upgrade` in the venv and restart, or just restart (it re-installs from the Git URL).

**Q: Can the YAML format reference inline Python functions?**
A: No. YAML steps are either module references or custom shell commands. For complex logic, use the Python DSL.
