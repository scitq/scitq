"""
STAR_ALIGN — star_align

Converted from nf-core/modules (star_align) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec, cond
from typing import Optional

def star_align(workflow, sample, *, inputs=None, paired=True, worker_pool=None,
          container="community.wave.seqera.io/library/htslib_samtools_star_gawk:ae438e9a604351a4"):
    """STAR_ALIGN step.

    Args:
        workflow: the Workflow object
        sample: sample object (needs .sample_accession)
        inputs: input from previous step (default: sample.fastqs)
        paired: whether data is paired-end
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: log_final="*Log.final.out", log_out="*Log.out", log_progress="*Log.progress.out", bam="*d.out.bam", bam_sorted="$prefix.sortedByCoord.out.bam", bam_sorted_aligned="$prefix.Aligned.sortedByCoord.out.bam", bam_transcript="*toTranscriptome.out.bam", bam_unsorted="*Aligned.unsort.out.bam", fastq="*fastq.gz", tab="*.tab", spl_junc_tab="*.SJ.out.tab", read_per_gene_tab="*.ReadsPerGene.out.tab", junction="*.out.junction", sam="*.out.sam", wig="*.wig", bedgraph="*.bg"
    """
    return workflow.Step(
        name="star_align",
        tag=sample.sample_accession,
        container=container,
        command=fr"""
        STAR \\
            --genomeDir /input/index \\
            --readFilesIn ${reads1.join(",")} ${reads2.join(",")} \\
            --runThreadN ${{CPU}} \\
            --outFileNamePrefix $prefix. \\
            $out_sam_type \\
            $ignore_gtf \\
            $attrRG \\
            $args

        $mv_unsorted_bam

        if [ -f {sample.sample_accession}.Unmapped.out.mate1 ]; then
            mv {sample.sample_accession}.Unmapped.out.mate1 {sample.sample_accession}.unmapped_1.fastq
            gzip {sample.sample_accession}.unmapped_1.fastq
        fi
        if [ -f {sample.sample_accession}.Unmapped.out.mate2 ]; then
            mv {sample.sample_accession}.Unmapped.out.mate2 {sample.sample_accession}.unmapped_2.fastq
            gzip {sample.sample_accession}.unmapped_2.fastq
        fi
        """,
        inputs=inputs or sample.fastqs,
        outputs=Outputs(log_final="*Log.final.out", log_out="*Log.out", log_progress="*Log.progress.out", bam="*d.out.bam", bam_sorted="$prefix.sortedByCoord.out.bam", bam_sorted_aligned="$prefix.Aligned.sortedByCoord.out.bam", bam_transcript="*toTranscriptome.out.bam", bam_unsorted="*Aligned.unsort.out.bam", fastq="*fastq.gz", tab="*.tab", spl_junc_tab="*.SJ.out.tab", read_per_gene_tab="*.ReadsPerGene.out.tab", junction="*.out.junction", sam="*.out.sam", wig="*.wig", bedgraph="*.bg"),
        task_spec=TaskSpec(cpu=12),
        worker_pool=worker_pool,
    )
