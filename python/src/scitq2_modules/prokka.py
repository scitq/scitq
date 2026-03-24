"""
PROKKA — prokka

Converted from nf-core/modules (prokka) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def prokka(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/prokka_openjdk:10546cadeef11472"):
    """PROKKA step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: gff="$prefix/*.gff", gbk="$prefix/*.gbk", fna="$prefix/*.fna", faa="$prefix/*.faa", ffn="$prefix/*.ffn", sqn="$prefix/*.sqn", fsa="$prefix/*.fsa", tbl="$prefix/*.tbl", err="$prefix/*.err", log="$prefix/*.log", txt="$prefix/*.txt", tsv="$prefix/*.tsv"
    """
    return workflow.Step(
        name="prokka",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        ${decompress}

        prokka \\
            ${args} \\
            --cpus ${{CPU}} \\
            --prefix {sample.sample_accession} \\
            ${proteins_opt} \\
            ${prodigal_tf_in} \\
            ${input}

        ${cleanup}

        cat <<-END_VERSIONS > versions.yml
        "${task.process}":
            prokka: \$(echo \$(prokka --version 2>&1) | sed 's/^.*prokka //')
        END_VERSIONS
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(gff="$prefix/*.gff", gbk="$prefix/*.gbk", fna="$prefix/*.fna", faa="$prefix/*.faa", ffn="$prefix/*.ffn", sqn="$prefix/*.sqn", fsa="$prefix/*.fsa", tbl="$prefix/*.tbl", err="$prefix/*.err", log="$prefix/*.log", txt="$prefix/*.txt", tsv="$prefix/*.tsv"),
        task_spec=None,
        worker_pool=worker_pool,
    )
