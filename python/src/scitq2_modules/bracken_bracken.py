"""
BRACKEN_BRACKEN — bracken_bracken

Converted from nf-core/modules (bracken_bracken) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def bracken_bracken(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/bracken:3.1--22a4e66ce04c5e01"):
    """BRACKEN_BRACKEN step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: none defined
    """
    return workflow.Step(
        name="bracken_bracken",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        bracken \\
            ${args} \\
            -d '${database}' \\
            -i '/input/kraken_report' \\
            -o '${bracken_report}' \\
            -w '${bracken_kraken_style_report}'
        """,
        inputs=inputs or sample.fastqs,
        outputs=None,
        task_spec=None,
        worker_pool=worker_pool,
    )
