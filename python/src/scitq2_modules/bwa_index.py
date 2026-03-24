"""
BWA_INDEX — bwa_index

Converted from nf-core/modules (bwa_index) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def bwa_index(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/bwa_htslib_samtools:83b50ff84ead50d0"):
    """BWA_INDEX step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: index="bwa"
    """
    return workflow.Step(
        name="bwa_index",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        mkdir bwa
        bwa \\
            index \\
            $args \\
            -p bwa/{sample.sample_accession} \\
            /input/fasta
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(index="bwa"),
        task_spec=None,
        worker_pool=worker_pool,
    )
