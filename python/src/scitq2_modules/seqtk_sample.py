"""
Read subsampling with seqtk.

Based on nf-core/modules (seqtk/sample) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond


def seqtk_sample(workflow, sample, *, inputs=None, paired=True, depth="10M",
                 seed=42, worker_pool=None, container="gmtscience/seqtk:1.4"):
    """Subsample reads to a fixed depth with seqtk.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step
        paired: whether data is paired-end
        depth: target depth (e.g. "10M", "20M")
        seed: random seed for reproducibility
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: fastqs="*.fastq.gz"
    """
    return workflow.Step(
        name="seqtk_sample",
        tag=sample.sample_accession,
        container=container,
        command=cond(
            (paired,
                fr"""
                . /builtin/std.sh

                _para seqtk sample -s{seed} /input/{sample.sample_accession}.1.fastq.gz {depth} | pigz > /output/{sample.sample_accession}.1.fastq.gz
                _para seqtk sample -s{seed} /input/{sample.sample_accession}.2.fastq.gz {depth} | pigz > /output/{sample.sample_accession}.2.fastq.gz
                _wait
                """),
            default=
                fr"""
                . /builtin/std.sh

                seqtk sample -s{seed} /input/{sample.sample_accession}.fastq.gz {depth} | pigz > /output/{sample.sample_accession}.fastq.gz
                """
        ),
        inputs=inputs,
        outputs=Outputs(fastqs="*.fastq.gz"),
        task_spec=TaskSpec(mem=30, cpu=2, prefetch="50%"),
        worker_pool=worker_pool,
    )
