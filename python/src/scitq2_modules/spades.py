"""
SPADES — spades

Converted from nf-core/modules (spades) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def spades(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/spades:4.1.0--77799c52e1d1054a"):
    """SPADES step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: scaffolds="*.scaffolds.fa.gz", contigs="*.contigs.fa.gz", transcripts="*.transcripts.fa.gz", gene_clusters="*.gene_clusters.fa.gz", gfa="*.assembly.gfa.gz", warnings="*.warnings.log", log="*.spades.log"
    """
    return workflow.Step(
        name="spades",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        spades.py \\
            $args \\
            --threads ${{CPU}} \\
            --memory $maxmem \\
            $custom_hmms \\
            $reads \\
            -o ./
        mv spades.log {sample.sample_accession}.spades.log

        if [ -f scaffolds.fasta ]; then
            mv scaffolds.fasta {sample.sample_accession}.scaffolds.fa
            gzip -n {sample.sample_accession}.scaffolds.fa
        fi
        if [ -f contigs.fasta ]; then
            mv contigs.fasta {sample.sample_accession}.contigs.fa
            gzip -n {sample.sample_accession}.contigs.fa
        fi
        if [ -f transcripts.fasta ]; then
            mv transcripts.fasta {sample.sample_accession}.transcripts.fa
            gzip -n {sample.sample_accession}.transcripts.fa
        fi
        if [ -f assembly_graph_with_scaffolds.gfa ]; then
            mv assembly_graph_with_scaffolds.gfa {sample.sample_accession}.assembly.gfa
            gzip -n {sample.sample_accession}.assembly.gfa
        fi

        if [ -f gene_clusters.fasta ]; then
            mv gene_clusters.fasta {sample.sample_accession}.gene_clusters.fa
            gzip -n {sample.sample_accession}.gene_clusters.fa
        fi

        if [ -f warnings.log ]; then
            mv warnings.log {sample.sample_accession}.warnings.log
        fi
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(scaffolds="*.scaffolds.fa.gz", contigs="*.contigs.fa.gz", transcripts="*.transcripts.fa.gz", gene_clusters="*.gene_clusters.fa.gz", gfa="*.assembly.gfa.gz", warnings="*.warnings.log", log="*.spades.log"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
