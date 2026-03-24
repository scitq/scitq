"""
Taxonomic classification with Kraken2.

Based on nf-core/modules (kraken2) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond


def kraken2(workflow, sample, *, inputs=None, paired=True, db_resource=None,
            worker_pool=None, container="staphb/kraken2:2.1.3"):
    """Classify reads with Kraken2.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step
        paired: whether data is paired-end
        db_resource: URI to the Kraken2 database (mounted in /resource/)
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: report="*.report", output="*.kraken2.out"
    """
    resources = [db_resource] if db_resource else []

    return workflow.Step(
        name="kraken2",
        tag=sample.sample_accession,
        container=container,
        command=cond(
            (paired,
                fr"""
                . /builtin/std.sh

                kraken2 --db /resource/ \
                    --threads ${{CPU}} \
                    --report /output/{sample.sample_accession}.kraken2.report \
                    --gzip-compressed --paired \
                    /input/{sample.sample_accession}.1.fastq.gz \
                    /input/{sample.sample_accession}.2.fastq.gz \
                    --output /output/{sample.sample_accession}.kraken2.out
                """),
            default=
                fr"""
                . /builtin/std.sh

                kraken2 --db /resource/ \
                    --threads ${{CPU}} \
                    --report /output/{sample.sample_accession}.kraken2.report \
                    --gzip-compressed \
                    /input/{sample.sample_accession}.fastq.gz \
                    --output /output/{sample.sample_accession}.kraken2.out
                """
        ),
        inputs=inputs,
        resources=resources,
        outputs=Outputs(report="*.report", output="*.kraken2.out"),
        task_spec=TaskSpec(cpu=8, mem=60),
        worker_pool=worker_pool,
    )
