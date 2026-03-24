"""
SAMTOOLS_FLAGSTAT — samtools_flagstat

Converted from nf-core/modules (samtools_flagstat) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def samtools_flagstat(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/htslib_samtools:1.23.1--5b6bb4ede7e612e5"):
    """SAMTOOLS_FLAGSTAT step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: flagstat="*.flagstat"
    """
    return workflow.Step(
        name="samtools_flagstat",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        samtools \\
            flagstat \\
            --threads ${{CPU}} \\
            /input/bam \\
            > {sample.sample_accession}.flagstat
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(flagstat="*.flagstat"),
        task_spec=None,
        worker_pool=worker_pool,
    )
