"""
MEGAHIT — megahit

Converted from nf-core/modules (megahit) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def megahit(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/megahit_pigz:87a590163e594224"):
    """MEGAHIT step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: contigs="*.contigs.fa.gz", k_contigs="intermediate_contigs/k*.contigs.fa.gz", addi_contigs="intermediate_contigs/k*.addi.fa.gz", local_contigs="intermediate_contigs/k*.local.fa.gz", kfinal_contigs="intermediate_contigs/k*.final.contigs.fa.gz", log="*.log"
    """
    return workflow.Step(
        name="megahit",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        megahit \\
            ${args} \\
            -t ${{CPU}} \\
            ${reads_command} \\
            --out-prefix {sample.sample_accession}

        pigz \\
            --no-name \\
            -p ${{CPU}} \\
            ${args2} \\
            megahit_out/*.fa \\
            megahit_out/intermediate_contigs/*.fa

        mv megahit_out/* .
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(contigs="*.contigs.fa.gz", k_contigs="intermediate_contigs/k*.contigs.fa.gz", addi_contigs="intermediate_contigs/k*.addi.fa.gz", local_contigs="intermediate_contigs/k*.local.fa.gz", kfinal_contigs="intermediate_contigs/k*.final.contigs.fa.gz", log="*.log"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
