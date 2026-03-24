"""
PICARD_MARKDUPLICATES — picard_markduplicates

Converted from nf-core/modules (picard_markduplicates) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def picard_markduplicates(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/picard:3.4.0--e9963040df0a9bf6"):
    """PICARD_MARKDUPLICATES step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: bam="*.bam", bai="*.bai", cram="*.cram", metrics="*.metrics.txt"
    """
    return workflow.Step(
        name="picard_markduplicates",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        picard \\
            -Xmx${avail_mem}M \\
            MarkDuplicates \\
            ${args} \\
            --INPUT /input/reads \\
            --OUTPUT {sample.sample_accession}.${suffix} \\
            ${reference} \\
            --METRICS_FILE {sample.sample_accession}.metrics.txt
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(bam="*.bam", bai="*.bai", cram="*.cram", metrics="*.metrics.txt"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
