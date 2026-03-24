"""
Quality trimming with fastp.

Based on nf-core/modules (fastp) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond


def fastp(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="staphb/fastp:1.0.1"):
    """Quality trimming with fastp.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession and .fastqs)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: fastqs="*.fastq.gz", json="*.json"
    """
    return workflow.Step(
        name="fastp",
        tag=sample.sample_accession,
        container=container,
        command=cond(
            (paired,
                fr"""
                . /builtin/std.sh
                . /builtin/bio.sh

                _find_pairs /input/*.f*q.gz
                _para zcat $READS1 > /tmp/read1.fastq
                _para zcat $READS2 > /tmp/read2.fastq
                _wait
                fastp \
                    --adapter_sequence AGATCGGAAGAGCACACGTCTGAACTCCAGTCA \
                    --adapter_sequence_r2 AGATCGGAAGAGCGTCGTGTAGGGAAAGAGTGT \
                    --trim_poly_g --detect_adapter_for_pe \
                    --cut_front --cut_tail --n_base_limit 0 --length_required 60 \
                    --in1 /tmp/read1.fastq --in2 /tmp/read2.fastq \
                    --json /output/{sample.sample_accession}_fastp.json \
                    -z 1 --out1 /output/{sample.sample_accession}.1.fastq.gz \
                    --out2 /output/{sample.sample_accession}.2.fastq.gz
                """),
            default=
                fr"""
                . /builtin/std.sh

                zcat /input/*.f*q.gz > /tmp/read.fastq
                fastp \
                    --trim_poly_g \
                    --cut_front --cut_tail --n_base_limit 0 --length_required 60 \
                    -i /tmp/read.fastq \
                    --json /output/{sample.sample_accession}_fastp.json \
                    -z 1 -o /output/{sample.sample_accession}.fastq.gz
                """
        ),
        inputs=inputs or sample.fastqs,
        outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
        task_spec=TaskSpec(cpu=5, mem=10, prefetch="100%"),
        worker_pool=worker_pool,
    )
