"""
HISAT2_ALIGN — hisat2_align

Converted from nf-core/modules (hisat2_align) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def hisat2_align(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/hisat2_samtools:6ca0ef72b662d5c8"):
    """HISAT2_ALIGN step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: bam="*.bam", summary="*.log", fastq="*fastq.gz"
    """
    return workflow.Step(
        name="hisat2_align",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        INDEX=`find -L ./ -name "*.1.ht2*" | sed 's/\\.1.ht2.*\$//'`
        hisat2 \\
            -x \${{INDEX}} \\
            -1 /input/*_1.f*q.gz \\
            -2 /input/*_2.f*q.gz \\
            $strandedness \\
            $ss \\
            --summary-file {sample.sample_accession}.hisat2.summary.log \\
            --threads ${{CPU}} \\
            $rg \\
            $unaligned \\
            --no-mixed \\
            --no-discordant \\
            $args \\
            | samtools view -bS -F 4 -F 8 -F 256 - > {sample.sample_accession}.bam

        if [ -f {sample.sample_accession}.unmapped.fastq.1.gz ]; then
            mv {sample.sample_accession}.unmapped.fastq.1.gz {sample.sample_accession}.unmapped_1.fastq.gz
        fi
        if [ -f {sample.sample_accession}.unmapped.fastq.2.gz ]; then
            mv {sample.sample_accession}.unmapped.fastq.2.gz {sample.sample_accession}.unmapped_2.fastq.gz
        fi
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(bam="*.bam", summary="*.log", fastq="*fastq.gz"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
