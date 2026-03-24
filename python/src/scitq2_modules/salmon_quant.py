"""
SALMON_QUANT — salmon_quant

Converted from nf-core/modules (salmon_quant) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def salmon_quant(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/salmon:1.10.3--h6dccd9a_2"):
    """SALMON_QUANT step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: results="$prefix", json_info="*info.json", lib_format_counts="*lib_format_counts.json"
    """
    return workflow.Step(
        name="salmon_quant",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        salmon quant \\
            --geneMap ${gtf} \\
            --threads ${{CPU}} \\
            --libType=${strandedness} \\
            ${reference} \\
            ${input_reads} \\
            ${args} \\
            -o {sample.sample_accession}

        if [ -f {sample.sample_accession}/aux_info/meta_info.json ]; then
            cp {sample.sample_accession}/aux_info/meta_info.json "{sample.sample_accession}_meta_info.json"
        fi
        if [ -f {sample.sample_accession}/lib_format_counts.json ]; then
            cp {sample.sample_accession}/lib_format_counts.json "{sample.sample_accession}_lib_format_counts.json"
        fi
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(results="$prefix", json_info="*info.json", lib_format_counts="*lib_format_counts.json"),
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
