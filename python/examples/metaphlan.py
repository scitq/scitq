from scitq2 import Workflow, Param, WorkerPool, W, TaskSpec, Resource, Shell, URI, cond, Outputs, run, ParamSpec
from scitq2.biology import ENA, SRA, S, SampleFilter

class Params(metaclass=ParamSpec):
    data_source = Param.enum(choices=["ENA", "SRA", "URI"], required=True, help="Data source for the samples: ENA, SRA, or URI.")
    identifier = Param.string(required=True)
    depth = Param.string(default="10M")
    version = Param.enum(choices=["4.0", "4.1"], required=True)
    custom_catalog = Param.string(required=False)
    download_locally = Param.boolean(default=False)
    limit = Param.integer(required=False)
    location = Param.provider_region(required=True, help="Provider and region for the workflow execution.")


def MetaPhlAnWorkflow(params: Params):

    workflow = Workflow(
        name="metaphlan4",
        description="Workflow for running MetaPhlAn4 on WGS data from ENA or SRA.",
        version="1.0.0",
        tag=f"{params.identifier}-{params.depth}",
        language=Shell("bash"),
        worker_pool=WorkerPool(
            W.cpu == 32,
            W.mem >= 120,
            W.disk >= 400,
            max_recruited=10,
            task_batches=2
        ),
        provider=params.location.provider,
        region=params.location.region,
    )

    if params.data_source == "ENA":
        samples = ENA(
            identifier=params.identifier,
            group_by="sample_accession",
            filter=SampleFilter(S.library_strategy == "WGS")
        )
    elif params.data_source == "SRA":
        samples = SRA(
            identifier=params.identifier,
            group_by="sample_accession",
            filter=SampleFilter(S.library_strategy == "WGS")
        )
    elif params.data_source == "URI":
        samples = URI.find(params.identifier,
            group_by="folder",
            filter="*.f*q.gz",
            field_map={
                "sample_accession": "folder.name",
                "project_accession": "folder.basename",
                "fastqs": "file.uris",
            }
        )

    for sample in samples:
    
        fastp = workflow.Step(
            name="fastp",
            tag=sample.sample_accession,
            command=cond(
                (sample.library_layout == "PAIRED",
                    fr"""
                    . /builtin/std.sh
                    _para zcat /input/*1.f*q.gz > /tmp/read1.fastq
                    _para zcat /input/*2.f*q.gz > /tmp/read2.fastq
                    _wait
                    fastp \
                    --adapter_sequence AGATCGGAAGAGCACACGTCTGAACTCCAGTCA \
                    --adapter_sequence_r2 AGATCGGAAGAGCGTCGTGTAGGGAAAGAGTGT \
                    --cut_front --cut_tail --n_base_limit 0 --length_required 60 \
                    --in1 /tmp/read1.fastq --in2 /tmp/read2.fastq \
                    --json /output/{sample.sample_accession}_fastp.json \
                    -z 1 --out1 /output/{sample.sample_accession}.1.fastq.gz \
                        --out2 /output/{sample.sample_accession}.2.fastq.gz
                    """),
                default= 
                    fr"""
                    . /builtin/std.sh
                    zcat /input/*.f*q.gz | fastp \
                    --adapter_sequence AGATCGGAAGAGCACACGTCTGAACTCCAGTCA \
                    --cut_front --cut_tail --n_base_limit 0 --length_required 60 \
                    --stdin \
                    --json /output/{sample.sample_accession}_fastp.json \
                    -z 1 -o /output/{sample.sample_accession}.fastq.gz
                    """
            ),
            container="gmtscience/scitq2:0.1.0",
            inputs=sample.fastqs,
            outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
            task_spec=TaskSpec(cpu=5, mem=10, prefetch="100%"),
            worker_pool=workflow.worker_pool.clone_with(max_recruited=5)
        )
        
        humanfilter = workflow.Step(
            name="humanfilter",
            tag=sample.sample_accession,
            command=cond(
                (sample.library_layout == "PAIRED",
                    fr"""
                    . /builtin/std.sh
                    bowtie2 -p $CPU --mm -x /resource/chm13v2.0/chm13v2.0 \
                    -1 /input/{sample.sample_accession}.1.fastq.gz \
                    -2 /input/{sample.sample_accession}.2.fastq.gz --reorder \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                    | samtools fastq -@ 2 -f 12 -F 256 \
                        -1 /output/{sample.sample_accession}.1.fastq \
                        -2 /output/{sample.sample_accession}.2.fastq \
                        -0 /dev/null -s /dev/null
                    for fastq in /output/*.fastq
                    do
                        _para pigz -p 4 $fastq
                    done
                    _wait
                    """),
                default= 
                    fr"""
                    . /builtin/std.sh
                    bowtie2 -p ${{CPU}} --mm -x /resource/chm13v2.0/chm13v2.0 \
                    -U /input/{sample.sample_accession}.fastq.gz --reorder \
                    2> /output/{sample.sample_accession}.bowtie2.log \
                    | samtools fastq -@ 2 -f 4 -F 256 \
                        -0 /output/{sample.sample_accession}.fastq -s /dev/null
                    pigz /output/*.fastq
                    """
            ),
            container="gmtscience/bowtie2:2.5.1",
            inputs=fastp.output("fastqs"),
            outputs=Outputs(fastqs="*.fastq.gz", log="*.log"),
        )

        if params.depth is not None:
            seqtk = workflow.Step(
                name="seqtk",
                tag=sample.sample_accession,
                inputs=humanfilter.output("fastqs"),
                command=cond(
                    (sample.library_layout == "PAIRED",
                        fr"""
                        . /builtin/std.sh
                        _para seqtk sample -s42 /input/{sample.sample_accession}.1.fastq.gz {params.depth} | pigz > /output/{sample.sample_accession}.1.fastq.gz
                        _para seqtk sample -s42 /input/{sample.sample_accession}.2.fastq.gz {params.depth} | pigz > /output/{sample.sample_accession}.2.fastq.gz
                        _wait
                        """),
                    default= 
                        fr"""
                        . /builtin/std.sh
                        seqtk sample -s42 /input/{sample.sample_accession}.fastq.gz {params.depth} | pigz > /output/{sample.sample_accession}.fastq.gz
                        """
                ),
                container="gmtscience/seqtk:1.4",
                task_spec=TaskSpec(mem=60)
            )
        
        metaphlan = workflow.Step(
            name="metaphlan",
            tag=sample.sample_accession,
            inputs=cond(
                (params.depth is not None, seqtk.output("fastqs")),
                default=humanfilter.output("fastqs")
            ),
            command=fr"""
            pigz -dc /input/*.fastq.gz | metaphlan \
                --input_type fastq --no_map --offline \
                --bowtie2db /resource/metaphlan/bowtie2 \
                --nproc $CPU \
                -o /output/{sample.sample_accession}.metaphlan4_profile.txt \
                2>&1 > /output/{sample.sample_accession}.metaphlan4.log
            """,
            container=cond(
                (params.version == "4.0", "gmtscience/metaphlan4:4.0.6.1"),
                default="gmtscience/metaphlan4:4.1"
            ),
            resources=cond(
                (params.custom_catalog is not None, Resource(path=params.custom_catalog, action="untar")),
                (params.version == "4.0", Resource(path="azure://rnd/resource/metaphlan4.0.5.tgz", action="untar")),
                default=Resource(path="azure://rnd/resource/metaphlan/metaphlan4.1.tgz", action="untar")
            ),
            outputs=Outputs(metaphlan="*.txt", logs="*.log"),
            task_spec=TaskSpec(cpu=8),
            worker_pool=workflow.worker_pool.clone_with(max_recruited=5)
        )

    compile_step = workflow.Step(
        name="compile",
        inputs=metaphlan.output("metaphlan",grouped=True),
        command=fr"""
        cd /input
        merge_metaphlan_tables.py *profile.txt > /output/merged_abundance_table.tsv
        """,
        container=metaphlan.container,
        outputs=Outputs(
            abundances="*.tsv",
            logs="*.log",
            publish=f"azure://rnd/results/metaphlan4/{params.identifier}/",
        ),
        task_spec=TaskSpec(cpu=4, mem=10)
    )

    compile_logs = workflow.Step(
        name="compile_logs",
        inputs=[humanfilter.output("logs",grouped=True,move="humanfilter"), 
                metaphlan.output("logs",grouped=True,move="metaphlan")],
        command=fr"""
        . /builtin/std.sh
        cd /input
        catalog=human
        for bowtielog in humanfilter/*; do
            sample=$(basename $bowtielog | sed 's/\.bowtie2\.log//')
            cat "$bowtielog" \
            |perl -ne 'if (/^(\d+) reads/) {{ $reads = $1 }} elsif (/^(\d+\.\d+)% overall alignment rate/) {{ print "reads\toverall_alignment_rate\n$reads\t$1\n" }}' \
            |csvtk -t mutate3 -n sample -e "'${{sample}}'" \
            |csvtk -t cut -f 3,1,2 \
            |csvtk -t rename -f "reads","overall_alignment_rate" -n "${{catalog}}_input_reads","${{catalog}}_overall_align_pc" > /tmp/${{sample}}_stats1.tsv
        done

        csvtk -t concat /tmp/*_stats1.tsv > /output/stats1.tsv
        tar -czf /output/logs.tar.gz metaphlan/*.log
        """,
        container=metaphlan.container,
        outputs=Outputs(
            stats1="*.tsv",
            logs="logs.tar.gz",
            publish=f"azure://rnd/results/metaphlan4/{params.identifier}/",
        ),
        task_spec=TaskSpec(cpu=4, mem=10)
    )

run(MetaPhlAnWorkflow)