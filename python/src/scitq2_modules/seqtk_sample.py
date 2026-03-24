"""
SEQTK_SAMPLE — seqtk_sample

Converted from nf-core/modules (seqtk_sample) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def seqtk_sample(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/seqtk:1.4--he4a0461_1"):
    """SEQTK_SAMPLE step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: reads="*.fastq.gz"
    """
    return workflow.Step(
        name="seqtk_sample",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        printf "%s\\n" /input/reads | while read f;
        do
            seqtk \\
                sample \\
                $args \\
                \$f \\
                {sample.sample_accession} \\
                | gzip --no-name > {sample.sample_accession}_\$(basename \$f)
        done
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(reads="*.fastq.gz"),
        task_spec=None,
        worker_pool=worker_pool,
    )
