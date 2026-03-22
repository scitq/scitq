#!/usr/bin/env nextflow

params.input_dir = "data"
params.reference = "ref/genome.fa"
params.depth = "10M"
params.outdir = "results"

process FASTP {
    container 'staphb/fastp:1.0.1'
    cpus 4
    memory '8 GB'

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.trimmed.fastq.gz"), emit: fastqs
    tuple val(sample_id), path("*_fastp.json"), emit: json

    script:
    """
    fastp \
        --in1 ${reads[0]} --in2 ${reads[1]} \
        --out1 ${sample_id}.1.trimmed.fastq.gz \
        --out2 ${sample_id}.2.trimmed.fastq.gz \
        --thread ${task.cpus} \
        --json ${sample_id}_fastp.json
    """
}

process BOWTIE2 {
    container 'gmtscience/bowtie2:2.5.1'
    cpus 8
    memory '30 GB'

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.fastq.gz"), emit: fastqs
    tuple val(sample_id), path("*.log"), emit: logs

    script:
    """
    bowtie2 -p ${task.cpus} --mm \
        -x ${params.reference} \
        -1 ${reads[0]} -2 ${reads[1]} \
        2> ${sample_id}.bowtie2.log \
    | samtools fastq -@ 2 -f 12 -F 256 \
        -1 ${sample_id}.1.fastq.gz \
        -2 ${sample_id}.2.fastq.gz \
        -0 /dev/null -s /dev/null
    """
}

process SEQTK {
    container 'gmtscience/seqtk:1.4'
    memory '60 GB'

    input:
    tuple val(sample_id), path(reads)

    output:
    tuple val(sample_id), path("*.fastq.gz"), emit: fastqs

    script:
    """
    seqtk sample -s42 ${reads[0]} ${params.depth} | gzip > ${sample_id}.1.fastq.gz
    seqtk sample -s42 ${reads[1]} ${params.depth} | gzip > ${sample_id}.2.fastq.gz
    """
}

process MERGE_RESULTS {
    container 'alpine'

    publishDir "${params.outdir}/merged", mode: 'copy'

    input:
    path(all_logs)

    output:
    path("merged.txt")

    script:
    """
    cat *.log > merged.txt
    """
}

workflow {
    reads = Channel.fromFilePairs("${params.input_dir}/*_{1,2}.fastq.gz")
    FASTP(reads)
    BOWTIE2(FASTP.out.fastqs)
    SEQTK(BOWTIE2.out.fastqs)
    MERGE_RESULTS(BOWTIE2.out.logs.collect())
}
