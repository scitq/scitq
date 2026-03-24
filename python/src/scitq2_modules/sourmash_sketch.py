"""
SOURMASH_SKETCH — sourmash_sketch

Converted from nf-core/modules (sourmash_sketch) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def sourmash_sketch(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="biocontainers/sourmash:4.9.4--hdfd78af_0"):
    """SOURMASH_SKETCH step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: signatures="*.sig"
    """
    return workflow.Step(
        name="sourmash_sketch",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        sourmash sketch \\
            $args \\
            --merge '{sample.sample_accession}' \\
            --output '{sample.sample_accession}.sig' \\
            /input/sequence

        cat <<-END_VERSIONS > versions.yml
        "${task.process}":
            sourmash: \$(echo \$(sourmash --version 2>&1) | sed 's/^sourmash //' )
        END_VERSIONS
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(signatures="*.sig"),
        task_spec=None,
        worker_pool=worker_pool,
    )
