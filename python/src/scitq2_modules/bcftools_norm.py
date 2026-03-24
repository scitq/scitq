"""
BCFTOOLS_NORM — bcftools_norm

Converted from nf-core/modules (bcftools_norm) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def bcftools_norm(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/bcftools_htslib:0a3fa2654b52006f"):
    """BCFTOOLS_NORM step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: vcf="*.vcf,vcf.gz,bcf,bcf.gz", tbi="*.tbi", csi="*.csi"
    """
    return workflow.Step(
        name="bcftools_norm",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        bcftools norm \\
            --fasta-ref /input/fasta \\
            --output {sample.sample_accession}.${extension} \\
            ${args} \\
            --threads ${{CPU}} \\
            /input/vcf
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(vcf="*.vcf,vcf.gz,bcf,bcf.gz", tbi="*.tbi", csi="*.csi"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
