# scitq2_modules — Reusable Step Definitions

A collection of reusable step functions for scitq workflows, covering common bioinformatics tools. Each module is a Python function following the standard signature:

```python
def tool_name(workflow, sample, *, inputs=None, worker_pool=None, **kwargs) -> Step
```

## Usage

```python
from scitq2 import *
from scitq2_modules.fastp import fastp
from scitq2_modules.bowtie2_align import bowtie2_host_removal

for sample in samples:
    qc = fastp(workflow, sample)
    clean = bowtie2_host_removal(workflow, sample, inputs=qc.output("fastqs"))
```

See [DSL documentation](../../docs/usage/dsl.md#using-modules) for full examples.

## Module catalog

### Quality

| Quality | Meaning |
|---|---|
| **Production** | Hand-written, tested in real GMT Science pipelines (biomscope, hermes, gpredomics). Handles paired/single-end via `cond()`, uses scitq builtins (`_para`, `_wait`, `_find_pairs`). |
| **Good** | Auto-converted from nf-core, reviewed and verified. Shell commands are correct but may lack paired/single-end branching or scitq-specific optimizations. |
| **Draft** | Auto-converted from nf-core, syntactically valid but may contain untranslated Groovy variables (`$args`, `$prefix`). Needs review before production use. |

### Modules

| Module | Function | Tool | Quality | Outputs |
|---|---|---|---|---|
| `fastp` | `fastp()` | [fastp](https://github.com/OpenGene/fastp) | **Production** | `fastqs="*.fastq.gz"`, `json="*.json"` |
| `bowtie2_align` | `bowtie2_align()` | [bowtie2](https://github.com/BenLangmead/bowtie2) | **Production** | `bam="*.bam"`, `log="*.log"` |
| `bowtie2_align` | `bowtie2_host_removal()` | bowtie2 + samtools | **Production** | `fastqs="*.fastq.gz"`, `log="*.log"` |
| `kraken2_kraken2` | `kraken2()` | [Kraken2](https://github.com/DerrickWood/kraken2) | **Production** | `report="*.report"`, `output="*.kraken2.out"` |
| `multiqc` | `multiqc()` | [MultiQC](https://multiqc.info/) | **Production** | `report="multiqc_report.html"` |
| `seqtk_sample` | `seqtk_sample()` | [seqtk](https://github.com/lh3/seqtk) | Good | `reads="*.fastq.gz"` |
| `seqtk_seq` | `seqtk_seq()` | seqtk | Good | `fastx="*.fastq.gz"` |
| `bwa_mem` | `bwa_mem()` | [BWA](https://github.com/lh3/bwa) | Good | `bam="*.bam"` |
| `bwa_index` | `bwa_index()` | BWA | Good | `index="bwa"` |
| `samtools_sort` | `samtools_sort()` | [samtools](https://github.com/samtools/samtools) | Good | `bam="*.bam"`, `csi="*.csi"` |
| `samtools_index` | `samtools_index()` | samtools | Good | `bai="*.bai"`, `csi="*.csi"` |
| `samtools_stats` | `samtools_stats()` | samtools | Good | `stats="*.stats"` |
| `samtools_flagstat` | `samtools_flagstat()` | samtools | Good | `flagstat="*.flagstat"` |
| `fastqc` | `fastqc()` | [FastQC](https://github.com/s-andrews/FastQC) | Good | `html="*.html"`, `zip="*.zip"` |
| `cutadapt` | `cutadapt()` | [Cutadapt](https://github.com/marcelm/cutadapt) | Good | `reads="*.fastq.gz"`, `log="*.log"` |
| `trimmomatic` | `trimmomatic()` | [Trimmomatic](http://www.usadellab.org/cms/?page=trimmomatic) | Good | `trimmed="*.paired.trim*.fastq.gz"`, `log="*.log"` |
| `salmon_index` | `salmon_index()` | [Salmon](https://github.com/COMBINE-lab/salmon) | Good | `index="salmon"` |
| `salmon_quant` | `salmon_quant()` | Salmon | Good | `results="*.sf"` |
| `star_align` | `star_align()` | [STAR](https://github.com/alexdobin/STAR) | Draft | `bam="*Aligned*.bam"`, `log="*Log*.out"` |
| `hisat2_align` | `hisat2_align()` | [HISAT2](https://github.com/DaehwanKimLab/hisat2) | Draft | `bam="*.bam"`, `log="*.log"` |
| `minimap2_align` | `minimap2_align()` | [minimap2](https://github.com/lh3/minimap2) | Good | `bam="*.bam"` |
| `picard_markduplicates` | `picard_markduplicates()` | [Picard](https://broadinstitute.github.io/picard/) | Draft | `bam="*.bam"`, `metrics="*.metrics.txt"` |
| `bcftools_norm` | `bcftools_norm()` | [bcftools](https://github.com/samtools/bcftools) | Draft | `vcf="*.vcf.gz"` |
| `bracken_bracken` | `bracken_bracken()` | [Bracken](https://github.com/jenniferlu717/Bracken) | Good | `results="*bracken*"` |
| `megahit` | `megahit()` | [MEGAHIT](https://github.com/voutcn/megahit) | Draft | `contigs="*.contigs.fa.gz"`, `log="*.log"` |
| `spades` | `spades()` | [SPAdes](https://github.com/ablab/spades) | Draft | `scaffolds="*.fa.gz"`, `contigs="*.fa.gz"`, `log="*.log"` |
| `prokka` | `prokka()` | [Prokka](https://github.com/tseemann/prokka) | Draft | `gff="*.gff"`, `faa="*.faa"`, `gbk="*.gbk"` |
| `sourmash_sketch` | `sourmash_sketch()` | [sourmash](https://github.com/sourmash-bio/sourmash) | Good | `signatures="*.sig"` |
| `sourmash_gather` | `sourmash_gather()` | sourmash | Good | `result="*gather.csv"` |

### Per-sample vs fan-in

Most modules are **per-sample** — called inside a `for sample in samples:` loop with `tag=sample.sample_accession`.

Fan-in modules (called after the loop):
- `multiqc()` — takes grouped inputs, no `sample` parameter

### Common parameters

All per-sample modules accept:
- `inputs=` — input from previous step (default: `sample.fastqs`)
- `paired=True/False` — paired-end or single-end mode (Production modules only)
- `worker_pool=` — optional WorkerPool override
- `container=` — Docker image override

## License

Module code is based on [nf-core/modules](https://github.com/nf-core/modules) (MIT License, Copyright (c) The nf-core team) and adapted for scitq. See individual file headers for attribution.
