"""
SAMTOOLS_SORT — samtools_sort

Converted from nf-core/modules (samtools_sort) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def samtools_sort(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/htslib_samtools:1.23.1--5b6bb4ede7e612e5"):
    """SAMTOOLS_SORT step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: bam="$prefix.bam", cram="$prefix.cram", sam="$prefix.sam", index="$prefix.$extension.crai,csi,bai"
    """
    return workflow.Step(
        name="samtools_sort",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        ${pre_command}samtools sort \\
            ${args} \\
            -T {sample.sample_accession} \\
            --threads ${{CPU}} \\
            ${reference} \\
            -o ${output_file} \\
            ${write_index} \\
            ${input_source}
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(bam="$prefix.bam", cram="$prefix.cram", sam="$prefix.sam", index="$prefix.$extension.crai,csi,bai"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
