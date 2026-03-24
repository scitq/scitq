"""
SOURMASH_GATHER — sourmash_gather

Converted from nf-core/modules (sourmash_gather) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def sourmash_gather(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          database_resource=None,
          container="biocontainers/sourmash:4.9.4--hdfd78af_0"):
    """SOURMASH_GATHER step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: result="*.csv.gz", unassigned="*_unassigned.sig.zip", matches="*_matches.sig.zip", prefetch="*_prefetch.sig.zip", prefetchcsv="*_prefetch.csv.gz"
    """
    resources = [r for r in [database_resource] if r is not None]
    return workflow.Step(
        name="sourmash_gather",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        sourmash gather \\
            $args \\
            --output {sample.sample_accession}.csv.gz \\
            ${unassigned} \\
            ${matches} \\
            ${prefetch} \\
            ${prefetchcsv} \\
            /input/signature \\
            /input/database

        cat <<-END_VERSIONS > versions.yml
        "${task.process}":
            sourmash: \$(echo \$(sourmash --version 2>&1) | sed 's/^sourmash //' )
        END_VERSIONS
        """,
        inputs=inputs or sample.fastqs,
        resources=resources,
        outputs=Outputs(result="*.csv.gz", unassigned="*_unassigned.sig.zip", matches="*_matches.sig.zip", prefetch="*_prefetch.sig.zip", prefetchcsv="*_prefetch.csv.gz"),
        task_spec=None,
        worker_pool=worker_pool,
    )
