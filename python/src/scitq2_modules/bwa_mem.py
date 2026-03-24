"""
BWA_MEM — bwa_mem

Converted from nf-core/modules (bwa_mem) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def bwa_mem(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/bwa_htslib_samtools:83b50ff84ead50d0"):
    """BWA_MEM step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: bam="*.bam", cram="*.cram", csi="*.csi", crai="*.crai"
    """
    return workflow.Step(
        name="bwa_mem",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        INDEX=`find -L ./ -name "*.amb" | sed 's/\\.amb\$//'`

        bwa mem \\
            $args \\
            -t ${{CPU}} \\
            \${{INDEX}} \\
            /input/reads \\
            | samtools $samtools_command $args2 ${reference} --threads ${{CPU}} -o {sample.sample_accession}.${extension} -
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(bam="*.bam", cram="*.cram", csi="*.csi", crai="*.crai"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
