#!/usr/bin/env nextflow

/*
 * Nextflow RNAseq pipeline (merged from nextflow-io/rnaseq-nf)
 */

params.reads = "data/ggal/*_{1,2}.fq"
params.transcriptome = "data/ggal/transcriptome.fa"
params.outdir = "results"

process INDEX {
    tag "${transcriptome.simpleName}"
    conda 'bioconda::salmon=1.10.3'

    input:
    path transcriptome

    output:
    path 'index', emit: index

    script:
    """
    salmon index --threads ${task.cpus} -t ${transcriptome} -i index
    """
}

process FASTQC {
    tag "${id}"
    conda 'bioconda::fastqc=0.12.1'
    publishDir "${params.outdir}/fastqc", mode: 'copy'

    input:
    tuple val(id), path(fastq_1), path(fastq_2)

    output:
    path "fastqc_${id}_logs", emit: logs

    script:
    """
    mkdir fastqc_${id}_logs
    fastqc -o fastqc_${id}_logs -f fastq -q ${fastq_1} ${fastq_2}
    """
}

process QUANT {
    tag "${id}"
    conda 'bioconda::salmon=1.10.3'
    cpus 8
    memory '16 GB'

    input:
    tuple val(id), path(fastq_1), path(fastq_2)
    path index

    output:
    path "quant_${id}", emit: results

    script:
    """
    salmon quant --threads ${task.cpus} --libType=U -i ${index} -1 ${fastq_1} -2 ${fastq_2} -o quant_${id}
    """
}

process MULTIQC {
    conda 'bioconda::multiqc=1.27.1'
    publishDir "${params.outdir}", mode: 'copy'

    input:
    path '*'

    output:
    path 'multiqc_report.html', emit: report

    script:
    """
    multiqc .
    """
}

workflow {
    read_pairs_ch = Channel.fromFilePairs(params.reads, checkIfExists: true, flat: true)
    INDEX(params.transcriptome)
    FASTQC(read_pairs_ch)
    QUANT(read_pairs_ch, INDEX.out.index)
    MULTIQC(FASTQC.out.logs.mix(QUANT.out.results).collect())
}
