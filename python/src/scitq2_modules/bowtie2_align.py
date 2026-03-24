"""
Alignment with bowtie2.

Based on nf-core/modules (bowtie2/align) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond


def bowtie2_align(workflow, sample, *, inputs=None, paired=True, index_resource=None,
                  worker_pool=None, container="gmtscience/bowtie2:2.5.4"):
    """Align reads with bowtie2.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        index_resource: URI to the bowtie2 index (mounted in /resource/)
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: bam="*.bam", log="*.log"
    """
    resources = [index_resource] if index_resource else []

    return workflow.Step(
        name="bowtie2_align",
        tag=sample.sample_accession,
        container=container,
        command=cond(
            (paired,
                fr"""
                . /builtin/std.sh

                INDEX=$(find -L /resource/ -name "*.rev.1.bt2" | sed "s/\.rev\.1\.bt2$//" | head -1)
                bowtie2 -p ${{CPU}} --mm -x ${{INDEX}} \
                    -1 /input/{sample.sample_accession}.1.fastq.gz \
                    -2 /input/{sample.sample_accession}.2.fastq.gz \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                | samtools sort -@ 2 -o /output/{sample.sample_accession}.bam
                """),
            default=
                fr"""
                . /builtin/std.sh

                INDEX=$(find -L /resource/ -name "*.rev.1.bt2" | sed "s/\.rev\.1\.bt2$//" | head -1)
                bowtie2 -p ${{CPU}} --mm -x ${{INDEX}} \
                    -U /input/{sample.sample_accession}.fastq.gz \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                | samtools sort -@ 2 -o /output/{sample.sample_accession}.bam
                """
        ),
        inputs=inputs or sample.fastqs,
        resources=resources,
        outputs=Outputs(bam="*.bam", log="*.log"),
        task_spec=TaskSpec(cpu=8, mem=30),
        worker_pool=worker_pool,
    )


def bowtie2_host_removal(workflow, sample, *, inputs=None, paired=True,
                         reference="chm13v2.0", resource_uri=None,
                         worker_pool=None, container="gmtscience/bowtie2:2.5.4"):
    """Remove host reads with bowtie2 alignment.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step
        paired: whether data is paired-end
        reference: name of the bowtie2 index inside /resource/
        resource_uri: URI to the reference resource
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: fastqs="*.fastq.gz", log="*.log"
    """
    resources = [resource_uri] if resource_uri else []

    return workflow.Step(
        name="humanfilter",
        tag=sample.sample_accession,
        container=container,
        command=cond(
            (paired,
                fr"""
                . /builtin/std.sh

                bowtie2 -p $CPU --mm -x /resource/{reference}/{reference} \
                    -1 /input/{sample.sample_accession}.1.fastq.gz \
                    -2 /input/{sample.sample_accession}.2.fastq.gz --reorder \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                | samtools fastq -@ 2 -f 12 -F 256 \
                    -1 /output/{sample.sample_accession}.1.fastq \
                    -2 /output/{sample.sample_accession}.2.fastq \
                    -0 /dev/null -s /dev/null
                for fastq in /output/*.fastq
                do
                    _para pigz -p 4 $fastq
                done
                _wait
                """),
            default=
                fr"""
                . /builtin/std.sh

                bowtie2 -p ${{CPU}} --mm -x /resource/{reference}/{reference} \
                    -U /input/{sample.sample_accession}.fastq.gz --reorder \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                | samtools fastq -@ 2 -f 4 -F 256 \
                    -0 /output/{sample.sample_accession}.fastq -s /dev/null
                pigz /output/*.fastq
                """
        ),
        inputs=inputs or sample.fastqs,
        resources=resources,
        outputs=Outputs(fastqs="*.fastq.gz", log="*.log"),
        task_spec=TaskSpec(cpu=8, mem=30, prefetch="25%"),
        worker_pool=worker_pool,
    )
