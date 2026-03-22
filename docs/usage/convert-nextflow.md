# Converting Nextflow pipelines to scitq

scitq includes an experimental converter that translates Nextflow DSL2 workflows (`.nf` files) into scitq Python DSL templates. The generated code is a working starting point that needs manual review and adjustment.

## Usage

```sh
python -m scitq2.convert.nextflow pipeline.nf -o pipeline.py
```

Or without `-o` to print to stdout:

```sh
python -m scitq2.convert.nextflow pipeline.nf
```

## What it does

The converter:

- Parses Nextflow `process` definitions (directives, inputs, outputs, scripts)
- Parses the `workflow {}` block to determine process wiring
- Generates a complete scitq Python DSL template with `Workflow`, `Step`, `Params`, `Outputs`, `TaskSpec`

### Automatic translations

| Nextflow | scitq |
|---|---|
| `process` | `workflow.Step()` |
| `container` directive | `container=` |
| `conda` directive | `container=` + mkdocker Dockerfile on stderr |
| `cpus` / `memory` | `TaskSpec(cpu=, mem=)` |
| `publishDir` | `Outputs(publish=True)` |
| `input: tuple val(id), path(reads)` | Per-sample loop with `tag=sample.sample_accession` |
| `input: path(file)` | One-off step outside the loop |
| `.collect()` | `.output("name", grouped=True)` (fan-in, after the loop) |
| `Channel.fromFilePairs()` | `URI.find()` (with TODO for adjustment) |
| `params.x` | `Params` class with `ParamSpec` |
| `${task.cpus}` | `${{CPU}}` |
| `${task.memory}` | `${{MEM}}` |
| `${params.x}` | `{params.x}` |
| `${sample_id}` | `{sample.sample_accession}` |

### Conda environments

When a process uses `conda:` instead of `container:`, the converter:

1. Generates a [mkdocker](https://github.com/gmtsciencedev/mkdocker) Dockerfile (printed to stderr)
2. Uses `{registry}/{package}:{version}` as the container name in the DSL

For example, `conda 'bioconda::salmon=1.10.3'` produces:

```docker
FROM gmtscience/mamba
RUN _conda install salmon=1.10.3
#tag 1.10.3
#registry gmtscience
```

Build the image with `mkdocker dockers/salmon` before running the workflow.

## Example

Given this Nextflow pipeline:

```groovy
params.reads = "data/*_{1,2}.fastq.gz"
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
        --out2 ${sample_id}.2.trimmed.fastq.gz \
        --thread ${task.cpus}
    """
}

process MERGE {
    container 'alpine'
    publishDir "${params.outdir}", mode: 'copy'

    input:
    path(all_results)

    output:
    path("merged.txt"), emit: merged

    script:
    """
    cat *.txt > merged.txt
    """
}

workflow {
    reads = Channel.fromFilePairs(params.reads)
    FASTP(reads)
    MERGE(FASTP.out.fastqs.collect())
}
```

The converter produces:

```python
from scitq2 import *

class Params(metaclass=ParamSpec):
    reads = Param.string(default="data/*_{1,2}.fastq.gz", help="reads")
    outdir = Param.string(default="results", help="outdir")
    location = Param.provider_region(required=True, help="Provider and region")

def converted_workflow(params: Params):

    workflow = Workflow(
        name="converted-pipeline",
        description="Converted from Nextflow",
        version="1.0.0",
        language=Shell("bash"),
        worker_pool=WorkerPool(
            W.cpu >= 4,
            W.mem >= 8,
            max_recruited=10,
        ),
        provider=params.location.provider,
        region=params.location.region,
    )

    # TODO: adjust sample discovery to your data source
    samples = URI.find(params.input_dir, group_by="folder", filter="*.f*q.gz",
        field_map={"sample_accession": "folder.name", "fastqs": "file.uris"})

    for sample in samples:

        fastp = workflow.Step(
            name="fastp",
            tag=sample.sample_accession,
            container="staphb/fastp:1.0.1",
            command=fr"""
            fastp --in1 /input/*_1.f*q.gz --in2 /input/*_2.f*q.gz \
                --out1 {sample.sample_accession}.1.trimmed.fastq.gz \
                --out2 {sample.sample_accession}.2.trimmed.fastq.gz \
                --thread ${{CPU}}
            """,
            inputs=sample.fastqs,
            outputs=Outputs(fastqs="*.trimmed.fastq.gz"),
            task_spec=TaskSpec(cpu=4, mem=8.0),
        )

    merge = workflow.Step(
        name="merge",
        container="alpine",
        command=fr"""
        cat *.txt > merged.txt
        """,
        inputs=fastp.output("fastqs", grouped=True),
        outputs=Outputs(merged="merged.txt", publish=True),
    )

run(converted_workflow)
```

## What needs manual review

The generated code includes `TODO` comments for items requiring attention:

- **Sample discovery**: `Channel.fromFilePairs()` is translated to a generic `URI.find()` call that may need adjustment for your data layout
- **Output paths**: commands may reference `${sample_id}.bam` which should become `/output/{sample.sample_accession}.bam` in scitq
- **Resources vs inputs**: reference data (genomes, indexes) may need to be `resources=` instead of `inputs=`
- **Container selection**: verify that generated mkdocker containers match required tool versions

## Limitations

The converter handles common DSL2 patterns. It does not support:

- Nextflow DSL1 (legacy syntax)
- Complex Groovy expressions in `script:` blocks (if/else, variable assignments)
- Channel operators: `.map {}`, `.combine()`, `.cross()`, `.join()` — these are flagged with TODO
- Subworkflows (`include` from other `.nf` files) — merge manually before converting
- Nextflow configs (`nextflow.config`) — translate provider/executor settings to `scitq.yaml` manually
