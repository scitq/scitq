"""Simple RNA-seq Snakemake pipeline for testing the converter."""

configfile: "config.yaml"

SAMPLES = glob_wildcards("data/{sample}_R1.fastq.gz").sample

rule all:
    input:
        expand("results/quant/{sample}/quant.sf", sample=SAMPLES),
        "results/multiqc_report.html"

rule fastp:
    input:
        r1="data/{sample}_R1.fastq.gz",
        r2="data/{sample}_R2.fastq.gz"
    output:
        r1="trimmed/{sample}_R1.fastq.gz",
        r2="trimmed/{sample}_R2.fastq.gz",
        json="trimmed/{sample}_fastp.json"
    threads: 4
    resources:
        mem_mb=8000
    conda:
        "envs/fastp.yaml"
    shell:
        """
        fastp --in1 {input.r1} --in2 {input.r2} \
            --out1 {output.r1} --out2 {output.r2} \
            --thread {threads} \
            --json {output.json}
        """

rule salmon_index:
    input:
        config["transcriptome"]
    output:
        directory("index/salmon")
    threads: 8
    conda:
        "envs/salmon.yaml"
    shell:
        "salmon index --threads {threads} -t {input} -i {output}"

rule salmon_quant:
    input:
        r1="trimmed/{sample}_R1.fastq.gz",
        r2="trimmed/{sample}_R2.fastq.gz",
        index="index/salmon"
    output:
        "results/quant/{sample}/quant.sf"
    threads: 8
    resources:
        mem_mb=16000
    conda:
        "envs/salmon.yaml"
    shell:
        """
        salmon quant --threads {threads} --libType=U \
            -i {input.index} \
            -1 {input.r1} -2 {input.r2} \
            -o results/quant/{wildcards.sample}
        """

rule multiqc:
    input:
        expand("trimmed/{sample}_fastp.json", sample=SAMPLES)
    output:
        "results/multiqc_report.html"
    conda:
        "envs/multiqc.yaml"
    shell:
        "multiqc trimmed/ -o results/"
