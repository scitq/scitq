"""
SALMON_INDEX — salmon_index

Converted from nf-core/modules (salmon_index) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def salmon_index(workflow, *, inputs=None, worker_pool=None,
          container="biocontainers/salmon:1.10.3--h6dccd9a_2"):
    """SALMON_INDEX step (non-per-sample).

    Args:
        workflow: the Workflow object
        inputs: input files
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: none defined
    """
    return workflow.Step(
        name="salmon_index",
        container=container,
        command=fr"""
        if [ -n '$genome_fasta' ]; then
            grep '^>' $genome_fasta | cut -d ' ' -f 1 | cut -d \$'\\t' -f 1 | sed 's/>//g' > decoys.txt
            cat $transcript_fasta $genome_fasta > $fasta
        fi

        salmon \\
            index \\
            --threads ${{CPU}} \\
            -t $fasta \\
            $decoys \\
            $args \\
            -i salmon
        """,
        inputs=inputs,
        outputs=None,
        task_spec=TaskSpec(cpu=6),
        worker_pool=worker_pool,
    )
