"""
FASTQC — fastqc

Converted from nf-core/modules (fastqc) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def fastqc(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/fastqc:0.12.1--hdfd78af_0"):
    """FASTQC step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: html="*.html", zip="*.zip"
    """
    return workflow.Step(
        name="fastqc",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        printf "%s %s\\n" ${rename_to} | while read old_name new_name; do
            [ -f "\${new_name}" ] || ln -s \$old_name \$new_name
        done

        fastqc \\
            ${args} \\
            --threads ${{CPU}} \\
            ${fastqc_memory_arg} \\
            ${renamed_files}
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(html="*.html", zip="*.zip"),
        task_spec=None,
        worker_pool=worker_pool,
    )
