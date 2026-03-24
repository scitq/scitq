"""
CUTADAPT — cutadapt

Converted from nf-core/modules (cutadapt) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def cutadapt(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/cutadapt:5.2--py311haab0aaa_0"):
    """CUTADAPT step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: reads="*.trim.fastq.gz", log="*.log"
    """
    return workflow.Step(
        name="cutadapt",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        cutadapt \\
            --cores ${{CPU}} \\
            $args \\
            $trimmed \\
            /input/reads \\
            > {sample.sample_accession}.cutadapt.log
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(reads="*.trim.fastq.gz", log="*.log"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
