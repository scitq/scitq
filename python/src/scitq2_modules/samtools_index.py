"""
SAMTOOLS_INDEX — samtools_index

Converted from nf-core/modules (samtools_index) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def samtools_index(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/htslib_samtools:1.23.1--5b6bb4ede7e612e5"):
    """SAMTOOLS_INDEX step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: index="*.bai,csi,crai"
    """
    return workflow.Step(
        name="samtools_index",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        samtools \\
            index \\
            -@ ${{CPU}} \\
            ${args} \\
            /input/input
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(index="*.bai,csi,crai"),
        task_spec=None,
        worker_pool=worker_pool,
    )
