#!/usr/bin/env nextflow

/*
 * Metagenomics pipeline - assembled from nf-core modules style
 * Tests: complex containers, Groovy conditionals in script, meta.id,
 * multiple emit outputs, label directives, multi-input processes
 */

params.reads = "data/*_{R1,R2}.fastq.gz"
params.host_index = "ref/chm13"
params.kraken_db = "ref/kraken2_db"
params.outdir = "results"
params.skip_host_removal = false
params.depth = null

process FASTP {
    tag "$meta.id"
    label 'process_medium'

    container "${ workflow.containerEngine == 'singularity' ?
        'https://depot.galaxyproject.org/singularity/fastp:0.23.4--hb7a2d85_2' :
        'community.wave.seqera.io/library/fastp:1.1.0--08aa7c5662a30d57' }"

    input:
    tuple val(meta), path(reads)

    output:
    tuple val(meta), path("*.fastp.fastq.gz"), emit: reads
    tuple val(meta), path("*.json")          , emit: json
    tuple val(meta), path("*.html")          , emit: html
    tuple val(meta), path("*.log")           , emit: log

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    if (meta.single_end) {
        """
        fastp \\
            --in1 ${reads[0]} \\
            --out1 ${prefix}.fastp.fastq.gz \\
            --json ${prefix}.fastp.json \\
            --html ${prefix}.fastp.html \\
            --thread $task.cpus \\
            2>| >(tee ${prefix}.fastp.log >&2)
        """
    } else {
        """
        fastp \\
            --in1 ${reads[0]} --in2 ${reads[1]} \\
            --out1 ${prefix}_R1.fastp.fastq.gz \\
            --out2 ${prefix}_R2.fastp.fastq.gz \\
            --json ${prefix}.fastp.json \\
            --html ${prefix}.fastp.html \\
            --detect_adapter_for_pe \\
            --thread $task.cpus \\
            2>| >(tee ${prefix}.fastp.log >&2)
        """
    }
}

process BOWTIE2_HOST_REMOVAL {
    tag "$meta.id"
    label 'process_high'

    container 'community.wave.seqera.io/library/bowtie2_samtools:edeb13799090a2a6'

    input:
    tuple val(meta), path(reads)
    path index

    output:
    tuple val(meta), path("*.fastq.gz"), emit: reads
    tuple val(meta), path("*.log")     , emit: log

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    INDEX=`find -L ./ -name "*.rev.1.bt2" | sed "s/\\.rev.1.bt2\$//"`

    bowtie2 \\
        -x \$INDEX \\
        -1 ${reads[0]} -2 ${reads[1]} \\
        --threads $task.cpus \\
        --reorder \\
        2> ${prefix}.bowtie2.log \\
    | samtools fastq -@ 2 -f 12 -F 256 \\
        -1 ${prefix}_host_removed_R1.fastq.gz \\
        -2 ${prefix}_host_removed_R2.fastq.gz \\
        -0 /dev/null -s /dev/null
    """
}

process SEQTK_SAMPLE {
    tag "$meta.id"
    label 'process_single'

    conda "bioconda::seqtk=1.4"

    input:
    tuple val(meta), path(reads)

    output:
    tuple val(meta), path("*.fastq.gz"), emit: reads

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    seqtk sample -s42 ${reads[0]} ${params.depth} | gzip > ${prefix}_sub_R1.fastq.gz
    seqtk sample -s42 ${reads[1]} ${params.depth} | gzip > ${prefix}_sub_R2.fastq.gz
    """
}

process KRAKEN2 {
    tag "$meta.id"
    label 'process_high'

    container 'community.wave.seqera.io/library/kraken2:920ecc6b96e2ba71'

    input:
    tuple val(meta), path(reads)
    path db

    output:
    tuple val(meta), path("*report.txt")  , emit: report
    tuple val(meta), path("*.fastq.gz")   , emit: classified, optional: true

    script:
    def prefix = task.ext.prefix ?: "${meta.id}"
    """
    kraken2 \\
        --db $db \\
        --threads $task.cpus \\
        --report ${prefix}.kraken2.report.txt \\
        --gzip-compressed \\
        --paired ${reads[0]} ${reads[1]} \\
        --output /dev/null
    """
}

process MERGE_REPORTS {
    label 'process_single'

    publishDir "${params.outdir}/kraken2", mode: 'copy'

    container 'ubuntu:latest'

    input:
    path reports

    output:
    path "combined_report.tsv", emit: report

    script:
    """
    head -1 \$(ls *.report.txt | head -1) > combined_report.tsv
    for f in *.report.txt; do tail -n+2 \$f >> combined_report.tsv; done
    """
}

workflow {
    Channel
        .fromFilePairs(params.reads, checkIfExists: true)
        .set { reads }

    FASTP(reads)

    if (!params.skip_host_removal) {
        host_index = file(params.host_index)
        BOWTIE2_HOST_REMOVAL(FASTP.out.reads, host_index)
        cleaned_reads = BOWTIE2_HOST_REMOVAL.out.reads
    } else {
        cleaned_reads = FASTP.out.reads
    }

    if (params.depth) {
        SEQTK_SAMPLE(cleaned_reads)
        final_reads = SEQTK_SAMPLE.out.reads
    } else {
        final_reads = cleaned_reads
    }

    kraken_db = file(params.kraken_db)
    KRAKEN2(final_reads, kraken_db)
    MERGE_REPORTS(KRAKEN2.out.report.collect())
}
