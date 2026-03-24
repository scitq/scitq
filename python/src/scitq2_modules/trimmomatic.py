"""
TRIMMOMATIC — trimmomatic

Converted from nf-core/modules (trimmomatic) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def trimmomatic(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/trimmomatic:0.39--hdfd78af_2"):
    """TRIMMOMATIC step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: trimmed_reads="*.paired.trim*.fastq.gz", unpaired_reads="*.unpaired.trim_*.fastq.gz", trim_log="*_trim.log", out_log="*_out.log", summary="*.summary"
    """
    return workflow.Step(
        name="trimmomatic",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        trimmomatic \\
            $trimmed \\
            -threads ${{CPU}} \\
            -trimlog {sample.sample_accession}_trim.log \\
            -summary {sample.sample_accession}.summary \\
            /input/reads \\
            $output \\
            $qual_trim \\
            $args 2>| >(tee {sample.sample_accession}_out.log >&2)
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(trimmed_reads="*.paired.trim*.fastq.gz", unpaired_reads="*.unpaired.trim_*.fastq.gz", trim_log="*_trim.log", out_log="*_out.log", summary="*.summary"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
