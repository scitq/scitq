"""
SEQTK_SEQ — seqtk_seq

Converted from nf-core/modules (seqtk_seq) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def seqtk_seq(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/seqtk:1.4--he4a0461_1"):
    """SEQTK_SEQ step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: fastx="*.gz"
    """
    return workflow.Step(
        name="seqtk_seq",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        seqtk \\
            seq \\
            $args \\
            /input/fastx | \\
            gzip -c > {sample.sample_accession}.seqtk-seq.${extension}.gz
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(fastx="*.gz"),
        task_spec=None,
        worker_pool=worker_pool,
    )
