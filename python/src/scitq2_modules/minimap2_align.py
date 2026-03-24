"""
MINIMAP2_ALIGN — minimap2_align

Converted from nf-core/modules (minimap2_align) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def minimap2_align(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/minimap2_samtools:33bb43c18d22e29c"):
    """MINIMAP2_ALIGN step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: paf="*.paf", bam="*.bam", index="*.bam.$bam_index_extension"
    """
    return workflow.Step(
        name="minimap2_align",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        $samtools_reset_fastq \\
        minimap2 \\
            ${args} \\
            -t ${{CPU}} \\
            ${target} \\
            ${query} \\
            ${cigar_paf} \\
            ${set_cigar_bam} \\
            ${bam_output}
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(paf="*.paf", bam="*.bam", index="*.bam.$bam_index_extension"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
