# scitq DSL

## Hello world

```python
from scitq2 import *

def helloworld():

    workflow = Workflow(
        name="helloworld",
        description="Minimal workflow example",
        version="1.0.0",
        language=Shell("sh"),
        worker_pool=WorkerPool(W.provider=="local.local", W.region=="local")
    )

    step = workflow.Step(
        name="hello",
        command=fr"echo 'Hello world'",
        container="alpine"
    )

run(helloworld)
```

scitq DSL uses python (normal python, version 3.8 or above). It is possible to install scitq2 python module outside of scitq server using the code source, simply do:

```sh
make venv VENV=/path/to/my/venv
```

This is useful to get coloration and feedback from IDE environment such as VSC, or to make some explorations of the objects, but you should not run template scripts outside scitq engine, the recommanded way to run them is to upload them in scitq using the [UI](ui.md#template-page) or the [CLI](cli.md#template-upload), and run them through scitq.

Albeit being in python, it is recommended to write template script in standardize way to maximize readability and reusability.

- first, always start by importing scitq2: `from scitq2 import *` is the most idiomatic way to do that,
- second, always create a function that create your workflow, it's a plain function and it must have either no argument, like here or a single argument, a params argument of type Params: `def myworkflow(params: Params)` which we will see just after.
- third, always end the script with a call to scitq2 runner, with your workflow creating function as argument `run(myworkflow)`

In the function, you must instanciate a Workflow object (the function may return this Workflow object but it's not required, and the most idiomatic form is not to return anything).

In order for your template to do anything, you must add at least one step to your workflow, which is done by calling the Workflow.Step() method (which returns a Step object).

### A minimal Worflow

```python
workflow = Workflow(
    name="helloworld",
    description="Minimal workflow example",
    version="1.0.0",
    language=Shell("sh"),
    worker_pool=WorkerPool(W.provider=="local.local", W.region=="local")
)
```


The Workflow() constructor takes several mandatory attributes:
- name : a string which should be unique to this workflow template (you can have several versions of the template, but a entirely different workflow should have a different name in your scitq setup),
- decription : a free string describing the use of the workflow template,
- version : a 'x.y.z' version string,

Here we added two facultative attributes, a default language for the command argument of each Step, here `Shell("sh")` (which will be busybox sh in alpine), and a worker pool definition.

Defining a worker pool is recommanded in all workflows as it creates recruiters automatically for all your steps. Here we define the most simple WorkerPool, the local worker pool: `WorkerPool(W.provider=="local.local", W.region=="local")`, which recruits any manually deployed worker.

### A minimal Step

```python
step = workflow.Step(
    name="hello",
    command=fr"echo 'Hello world'",
    container="alpine"
)
```

The Step constructor take at least three arguments:
- name: naming a step is mandatory in the DSL,
- command: the Unix command line instruction passed to the language interpreter that we defined above,
- container: the name of a Docker container to use to execute the command.

The constructor takes also an optional name which is recommended. 

You may note that the command string is an "fr" string, which is a standard python object, but not useful here, and not commonly used in python. It is because using "fr" string as command is idiomatic in scitq DSL. The reason is that it prevents several errors and help the DSL to detect mistakes. "fr" string makes all shell variable usage look like this `fr"echo '${{HELLOWORLD}}'"`, so if the DSL see something like this: `fr"echo '${HELLOWORLD}'"` it can emit a warning as this look very much like a syntax error. Using an r string makes backslash use `\` less error prone. Not using "fr" string as command will trigger a warning when importing the DSL script.

## A more realistic template

This "hello world" example differs with a real life example in two major ways:
- it takes no parameters: unless the template is used only once, it likely should take a parameter, at least to chose the initial data it needs to start,
- the unique step has only one task: if so given the low complexity, creating directly the task with the CLI is probably a better option.

So let us introduce the "hello goodbye workflow" :

```python
from scitq2 import *

LOREM_IPSUM = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."

class Params(metaclass=ParamSpec):
    name = Param.string(required=True, help="State your name here")
    how_many = Param.integer(default=1, help="How many times to say hello")
    parallel = Param.integer(default=1, help="How many parallel tasks to run")

def helloworld(params: Params):

    workflow = Workflow(
        name="helloworld-multi-long",
        description="Minimal workflow example, with parallel steps",
        version="1.0.0",
        tag=f"{params.name}",
        language=Shell("sh"),
        worker_pool=WorkerPool(W.provider=="local.local", W.region=="local")
    )

    for i in range(params.how_many):
        step = workflow.Step(
            name="hello",
            tag=str(i),
            command=fr"for i in Hello, {params.name} did you know that {LOREM_IPSUM}; do echo $i; sleep 1; done",
            container="alpine",
            task_spec=TaskSpec(concurrency=params.parallel, prefetch=0)
        )
        step2 = workflow.Step(
            name="goodbye",
            tag=str(i),
            command=fr"for i in Goodbye, {params.name} did you know that {LOREM_IPSUM}; do echo $i; sleep 1; done",
            container="alpine",
            task_spec=TaskSpec(concurrency=params.parallel, prefetch=0),
            depends=step,
            inputs=["http://speedtest.tele2.net/100MB.zip"]
        )
        step3 = workflow.Step(
            name="long",
            tag=str(i),
            command=fr"for i in This is a long step, {params.name} did you know that {LOREM_IPSUM}; do echo $i; sleep 1; done",
            container="alpine",
            task_spec=TaskSpec(concurrency=params.parallel, prefetch=0),
            depends=step2
        )
    
    step4 = workflow.Step(
        name="final",
        command=fr"echo All done, {params.name}! Did you know that {LOREM_IPSUM}",
        container="alpine",
        depends=step3.grouped(),
        task_spec=TaskSpec(concurrency=1, prefetch=0)
    )
 
run(helloworld)
```

While the overall structure is the same, there are several differences:
- We have a new Params objects that has a metaclass ParamSpec,
- we use more attributes in the different constructors,
- but most of all this script contains apparently a python error.

In the loop inside the main function, we define step, step2 and step3 several time, overriding them. And its not that we declare more steps but we don't keep the first ones because we don't need them anymore: no, each iteration step is really the same (there are 4 steps in this workflow). The apparent duplication is because Step objects are hybrid, they simultaneously represent the Step and its Tasks. The DSL knows this is the same Step because it has the same name, and it accepts variation in some arguments, like the command or inputs or output, that make sense at Step level. In this example, the only arguments unique to the Step are its name and this new argument, task_spec. 

The rule is simple, only the name and what define the recruiter are unique to Step, the other arguments are passed to the underlying Task object. The DSL won't let you override an attribute that is not supposed to change. 

What would happen if you had all your steps unique with one task? Not much, it would just makes your workflow uselessly complex in apparence for end users, but it would behave the same. The Step/Task duality is a elegant way to mutualize what should be common between similar tasks, and an indication to the scitq engine that these tasks are really similar in nature and that they should be grouped in the UI and to compute stats.

### A first Params class

```python
class Params(metaclass=ParamSpec):
    name = Param.string(required=True, help="State your name here")
    how_many = Param.integer(default=1, help="How many times to say hello")
    parallel = Param.integer(default=1, help="How many parallel tasks to run")
```

This syntax is very similar to Django or SQLAlchemy syntax. It declare parameter as class level variables, specifying their type with Param.string or Param.integer. Appart from the name, it has self explanatory attributes (default, required, help): the help message is used by the UI or the CLI to help users launch the template.

### A more realistic Workflow

```python
workflow = Workflow(
    name="helloworld-multi-long",
    description="Minimal workflow example, with parallel steps",
    version="1.0.0",
    tag=f"{params.name}",
    language=Shell("sh"),
    worker_pool=WorkerPool(W.provider=="local.local", W.region=="local")
)
```

This workflow object has one new attribute, the tag attribute. The tag enable to give a name to each workflow that derives from the template name. If no tag is given, scitq will just add a counter, but a tag associated with one of the main Params usually makes more sense on a user perspective.

### More realistic steps

```python
step = workflow.Step(
    name="hello",
    tag=str(i),
    command=fr"for i in Hello, {params.name} did you know that {LOREM_IPSUM}; do echo $i; sleep 1; done",
    container="alpine",
    task_spec=TaskSpec(concurrency=params.parallel, prefetch=0)
)
```

This step introduces:
- the tag attribute, exactly like the workflow, this enable to name tasks after step name, but adding the tag (which must be specific to each task),
- the task_spec attribute (see below).

NB: Due the `fr""` string, it is clear that the variables between curly braces are interpolated in python, not in the shell (it would have been in the shell with dolar double curly braces or simply dolar like the `$i`).

### TaskSpec object
The task_spec accept a TaskSpec object, an object that defines what are the concurrency and prefetch value for workers assigned to this step.

You may use either the static concurrency, you specify a concurrency and prefetch value as a TaskSpec:

```python
TaskSpec(concurrency=2, prefetch="50%")
```

This works well if the worker pool defined at workflow level hosts very similar instances: in the example above, each worker can execute 2 tasks of this kind and the prepare half of this pool (1 task) in advance so as not wait for nothing when a slot turns free.

However, this is not satisfying if the worker pool include machines of different sizes. In this case, it is more optimal to determine what is limitating for the task. If each task requires 8 cpu, you can say:

```python
TaskSpec(cpu=8, prefetch="50%")
```

If a task require at least 4 cpu and 30 Gb of memory, you can say:
```python
TaskSpec(cpu=4, mem=30.0, prefetch="50%")
```

A TaskSpec can either specify concurrency (e.g. static concurrency) or specify one or several of cpu, mem and disk (disk space required per task) for dynamic concurrency. Prefetch is always specified as a proportion of concurrency.

Note that this does not impact the worker pool itself, just how many tasks each worker within this pool can do.

### depends, inputs

The second step uses two new attributes, depends and inputs:

```python
step2 = workflow.Step(
    name="goodbye",
    tag=str(i),
    command=fr"for i in Goodbye, {params.name} did you know that {LOREM_IPSUM}; do echo $i; sleep 1; done",
    container="alpine",
    task_spec=TaskSpec(concurrency=params.parallel, prefetch=0),
    depends=step,
    inputs=["http://speedtest.tele2.net/100MB.zip"]
)
```

`depends` accepts a Step object or a list of Steps, or an object akin to a list of Steps, a GroupedStep which we'll see later. It enable the simple definition of the task dependancies. Note that in this case, the real dependancy is with the task, not the whole step. The dependancy is created with the latest instance of the Step, it's current value in the loop iteration.

`inputs` accepts a string or a list of string or an Output object which we'll see later. The strings `inputs` accept are the same that are acceptable in [CLI file operations](cli.md#file).

### GroupedStep object

The last step of this template is a *final* step, e.g. a step that:
- is defined out of the main loop, after all other steps have been defined,
- depends on all or a large proportion of the other steps.

```python
step4 = workflow.Step(
    name="final",
    command=fr"echo All done, {params.name}! Did you know that {LOREM_IPSUM}",
    container="alpine",
    depends=step3.grouped(),
    task_spec=TaskSpec(concurrency=1, prefetch=0)
)
```

And for the second condition we see here the call to `step3.grouped()` to define the dependencies. The `grouped()` method of a Step provide a GroupedStep object that is equivalent to a list of all the Tasks belonging to the step. If the .grouped() has been omitted (`depends=step3`) it would have mean: depends on the latest step3 (the last since it is out of the loop). With the GroupedTask is like if we had collected all the steps during the loop and provided this list as depends: it depends on all step3.

## A real life example

scitq can be used in lots of different contexts, but it was invented for Bioinformatics. Most bioinformatics tools are autonomous commands you typically run from the Unix command line. So here we propose the use of a famous aligner command to identify the different species in a metagenomic samples, MetaPhlAn. It is recommanded to prepare the sequences before passing it to MetaPhlAn:
- removing sequencing errors and low quality parts (fastp),
- removing the human part of the DNA (bowtie and samtools),
- normalizing the sample (seqtk),
- and aligning with MetaPhlAn.

You may compile all the results together with a tool provided by the MetaPhlAn team (the Huttenhower Lab). 
And the beauty of the Bioinformatics academic world is that all these tools are free and open source. 

```python
from scitq2 import Workflow, Param, WorkerPool, W, TaskSpec, Resource, Shell, URI, cond, Outputs, run, ParamSpec
from scitq2.biology import ENA, SRA, S, SampleFilter

class Params(metaclass=ParamSpec):
    data_source = Param.enum(choices=["ENA", "SRA", "URI"], required=True, help="Data source for the samples: ENA, SRA, or URI.")
    identifier = Param.string(required=True)
    depth = Param.string(default="10M")
    version = Param.enum(choices=["4.0", "4.1"], required=True)
    custom_catalog = Param.string(required=False)
    paired = Param.boolean(default=False)
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
            W.cpu >= 32,
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
                (params.paired,
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
            container="staphb/fastp:1.0.1",
            inputs=sample.fastqs,
            outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
            task_spec=TaskSpec(cpu=5, mem=10, prefetch="100%"),
            worker_pool=workflow.worker_pool.clone_with(max_recruited=5)
        )
        
        humanfilter = workflow.Step(
            name="humanfilter",
            tag=sample.sample_accession,
            command=cond(
                (params.paired,
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
                    (params.paired,
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

run(MetaPhlAnWorkflow)
```

### Imports

```python
from scitq2 import Workflow, Param, WorkerPool, W, TaskSpec, Resource, Shell, URI, cond, Outputs, run, ParamSpec
from scitq2.biology import ENA, SRA, S, SampleFilter
```

The first line is similar to what we've seen before (a little more explicit if you like it more like that). The second like is specific to biological resources: ENA and SRA are two well known services for genomic sequences publicly available, hosted  respectively by Europe EMBL-EBI institute and by US NIH NCBI institute. We also take a class called a SampleFilter which as you guess help filtering samples and a mysterious S class... S (like the above W) is used in filters, much like in Django or SQLAlchemy.

### Real world params

```python
class Params(metaclass=ParamSpec):
    data_source = Param.enum(choices=["ENA", "SRA", "URI"], required=True, help="Data source for the samples: ENA, SRA, or URI.")
    identifier = Param.string(required=True)
    depth = Param.string(default="10M")
    version = Param.enum(choices=["4.0", "4.1"], required=True)
    custom_catalog = Param.string(required=False)
    paired = Param.boolean(default=False)
    limit = Param.integer(required=False)
    location = Param.provider_region(required=True, help="Provider and region for the workflow execution.")
```

We find the same types of template parameters (Param) that we had before with additions:
- Param.enum() : this is a choice Param, it is displayed as a select,
- Param.provider_region() : this is displayed as a select also but its values are directly provided by the server configuration, it list all the available providers and regions.

This is of major importance for most providers, notably because of transfer fees. Transfer fees are obey an almost universal rule:
- input traffic is free,
- output traffic is paid, and is generally costly, so that you keep the data in the provider infrastructure and all computation occurs there.

So not only you're incitated to stay with the initial provider of a project, but often to stay in the same region (for instance this is the case with Azure, while OVH does not apply fees for inter-regional transfers if you stay in OVH). Bottom line, for Azure at least, it is recommanded to stick in one region in a consistent manner in a given workflow, at least until the data volume decrease significantly. So chosing one region for a workflow is strategical to contains costs. 

It's also good to be able to change regions since another universal rule is that instance quotas are regionalized. Meaning that if you need to maximize workforce in a workflow, you may need to saturate quotas in the region you chose for one workflow. So having other regions at hand enables you to launch several workflows in a concurrent way. This tactical element is particularly important for Azure since spot is used and spot quotas are low (which makes sense since this is a discount offer).

### A Real world workflow

```python
workflow = Workflow(
    name="metaphlan4",
    description="Workflow for running MetaPhlAn4 on WGS data from ENA or SRA.",
    version="1.0.0",
    tag=f"{params.identifier}-{params.depth}",
    language=Shell("bash"),
    worker_pool=WorkerPool(
        W.cpu >= 32,
        W.mem >= 120,
        W.disk >= 400,
        max_recruited=10,
        task_batches=2
    ),
    provider=params.location.provider,
    region=params.location.region,
)
```

We find back the typical attributes we've seen before:
- name: a unique name for the template,
- description: a help introduction for users,
- version: a unique version,
- tag: the extension added to the template name to create each workglow name,
- language: the default language (here shell) in which the different commands are written.

Plus some new comers:
- worker_pool,
- region and provider.

#### A real world WorkerPool

The worker_pool takes a WorkerPool object, which we've seen before, but only for the loca.local provider and its local region. The real world WorkerPool is quite different:

It begins with several filtering expressions using the W. notation. This enable the construction of the flavor filter described in [CLI](cli.md#flavor-list) in a pythonic way:

```python
WorkerPool(
    W.cpu>=32, 
    W.mem>=120,
    W.disk>=400
)
```

Is equivalent to the filter `cpu>=32:mem>=120:disk>=400`, and means 32 cpu at least with at least 120 Gb of memory and at least 400 Gb of disk.

```python
WorkerPool(
    [...]
    max_recruited = 10,
    task_batches = 2
)
```

The last two options in the WorkerPool are `max_recruited`, this is the maximum for this workflow, and `task_batches`. `task_batches` is the target number of batches each worker must do to complete the total number of tasks of each type the workflow. So if each worker can do 10 tasks in the meantime (concurrency=10) and `task_batches` is 2, it means each worker should do 10*2 = 20 tasks total in two rounds. So it there are 40 tasks to do, we need 2 workers. If concurrency is dynamic, the recruiter will recruit different workers up to the required task execution rate.

Note that we did not specify the region and provider in the WorkerPool like we did before and we are going to see why just now.

#### provider and region in a real world workflow

`provider` and `region` are specified but this time not in the WorkerPool itself like previously. The reason is that they are used for two things:
- the WorkerPool will look for the Workflow provider and region setting if has none of its own (so there no difference at WorkerPool level where you define them),
- the Workflow also uses that to chose a local workspace.

If you look in [configuration](../reference/configuration.md) and in example configurations, you'll see that each region may have a workspace. The reason is the same than we've seen before: transfer fees. Since it costly to move data out of a region, it is essential that working data stay in the region. For this reason, Workflow objects have an underlying workspace that is regionalized, and which is chosen using the region and the configuration settings.

### Fetching data in the real world

Our previous examples did not rely on scientific data input, while this is particularly common in the real world.

Here this portion of the code takes two params, params.data_source (`ENA`, `SRA` or `URI`) and params.identifier (a project accession like `PRJEB49516` or a folder URI, like `s3://rnd/data/myproject`). It provides a list of Sample object, each with a `fastqs` field that contain scitq compatible URIs to fetch FASTQ data, the standard genetic format files. scitq contains a specific URI adapted to this kind of resources.

```python
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
```

This part is really highly dependant on the field. Here in the biology field, data is often attached to the notion of sample, notably samples from the public databanks such as ENA or SRA. Samples are collected from a project defined by its project accession (identifier), grouped by sample or run level (defined each time by their field "sample_accession" or "run_accession"). Last, samples may be filtered with a SampleFilter construct, using S fields much like the WorkerPool construct. See [DSL Reference](../reference/python_dsl.md#samplefilter) for details.

The URI construct is a more universal since it regroups files of a certain nature, specified with a globbing pattern in `filter`. It can take advantage of the file hierarchy (`folder.name`, parent folder name as `folder.basename`) to fill in custom field, here to mimick a Sample such as provided by ENA/SRA. See [DSL Reference](../reference/python_dsl.md#uri).

### A first real step

New step in our code is to take advantages of the collected samples to generate a loop, and in this loop a first task, a data curation step typical of bioinformatic workflows.

```python
for sample in samples:
    
    fastp = workflow.Step(
        name="fastp",
        tag=sample.sample_accession,
        command=cond(
            (params.paired,
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
        container="staphb/fastp:1.0.1",
        inputs=sample.fastqs,
        outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
        task_spec=TaskSpec(cpu=5, mem=10, prefetch="100%"),
        worker_pool=workflow.worker_pool.clone_with(max_recruited=5)
    )
```

This first step begins exactly like our previous example with a name and a tag. Here the tag is the official identifier of a biological sample, something real. 

#### `cond` keyword

`command` bears a first specificity, it uses the `cond` keyword, which is very much like an if/then/else structure except it can be used in variable affectation. We could use the pythonic `command=x if y else z` but this is less readable, especially if x and z are large in size and y is very small. In the DSL, using the `cond()` keyword is recommanded for readability. `cond()` takes an arbitrary number of pairs (condition, value) and returns the value associated with the first condition that is true. It may take an optional default value which is returned if all conditions are false.

#### Using builtins

Builtins are specific libraries, dedicated to shell usage. Shell is a choice language when using executable, and performing peripheral actions. However, due to its main usage in interactive environments, and to its long history, there are several issues with shell (hence some innovations like zsh (minor evolution) and fish (major evolution) and other more modern shells): notably error treatment defaults are not appropriate, parallel executions are hard to manage. 

There are lots of solutions to address these issues, notably changing default behavior in shell (like with `set -e` which will make shell behave like most modern language, e.g. stop at the first error), or using power tools such as GNU parallel for parallel tasks execution. However we must also take account of the docker issue: we like to reuse ready made dockers, it saves a lot of time, thus in most cases we would like to avoid specific requirements in containers (like always having bash, always having GNU parallel and pigz, and lots of utility we would love to have). scitq does not tell you what to do on this score, so if you prefer always have your dockers with your requirements, it is perfectly fine and you can do that. However we wanted to provide an alternative light approach, which is the builtins. It's a serie of ready made shell library which you can import with the typical shell dot notation:

```sh
. /builtin/std.sh
```

Builtins are available in `/builtin` folder which is mounted by default in all containers. It may be safely ignored, but you can also import anything in this folder. For now there are two libs, std.sh and bio.sh.

##### std.sh

std.sh is activated by:
```sh
. /builtin/std.sh
```

The first thing it implements is the `_strict` mode which is enforced by default. This sets `set -e` which makes the script fails at first error and the `set -o pipefail` which makes a shell pipe fails if the first command fails:

For instance: this will succeed if `my_failing_command` outputs anything, including if the command fails in the end, this is (alas) standard shell behavior:
```sh
set -e
my_failing_command |gzip
```

Failing if `my_failing_command` fails requires:
```sh
set -e
set -o pipefail
my_failing_command |gzip
```
Which works in bash but not in all shells... 

```sh
. /builtin/std.sh
my_failing_command |gzip>/output/out.dat
```

Will do that and avoid failing if the shell cannot do it. You may also disable this stricter error treatement for one command with the `_canfail` function:

```sh
. /builtin/std.sh
my_important_command |gzip>/output/out.dat
_canfail my_less_important_command |gzip>/output/logs.gz
```

You may also disable the strict mode completely wih `_nostrict` and re-enable it later with `_strict`.

But probably the most interesting function of std.sh is `_para`, it works almost like:
```sh
task1 &
task2 &
wait
```

Except wait won't fail if task1 or task2 fails, whereas this will work as expected:

```sh
. /builtin/std.sh
_para task1
_para task2
_wait
```

_wait will fail if task1 or task2 fails. 

This will also work as expected :

```sh
. /builtin/std.sh
_para zcat /input/*1.f*q.gz > /tmp/read1.fastq
_para zcat /input/*2.f*q.gz > /tmp/read2.fastq
_wait
```

This will work in most shells, like bash, but also alpine sh (busybox) or debian sh (dash) or zsh. You can see the code of `std.sh` in client/helpers.go.

std.sh offers a last keyword, `_retry`. As its name suggests, `_retry` enable to retry several times a certain command. In its simple form it is used like this:

```sh
. /builtin/std.sh
_retry my_unsure_command
```

It will retry the command 5 times with a 1 second delay, and eventually fails if all fail or succeed if the last attempt succeeds. Of course you could write that yourself with an until command, but it's difficult to get it right in standard shells.

```sh
. /builtin/std.sh
_retry 6 my_unsure_command
```
This will retry 6 times.

```sh
. /builtin/std.sh
_retry 6 2 my_unsure_command
```
This will retry 6 times with a 2 second delay between each attempt.


##### bio.sh

This is work in progress, it contains a single command for now: `_find_pairs` which tries its best to find pairs of FASTQ. Genetic sequence files are often "paired" (a DNA fragment may be sequenced from both ends), and depending on researchers practices there are different ways to specify that (ending with `.1.f*q.gz` or `.2.f*q.gz` but sometimes `_1.f*q.gz` and sometimes there are three files... `_find_pairs` will do its best to find the different pairs and store them in `$READ1` and `$READ2`)

```sh
. /builtin/std.sh
. /builtin/bio.sh

_find_pairs /input/*.f*q.gz
_para zcat $READS1 > /tmp/read1.fastq
_para zcat $READS2 > /tmp/read2.fastq
_wait
```

The implementation of _find_pairs is this:
```sh
_find_pairs() {
  # do not assume arrays; keep it POSIX
  for sep in _ . r R ""; do
    r1=""; r2=""; extra=""
    c1=0;  c2=0
    for fq do
      case "$fq" in
        *${sep}1.f*q.gz) r1="${r1}${r1:+ }$fq"; c1=$((c1+1));;
        *${sep}2.f*q.gz) r2="${r2}${r2:+ }$fq"; c2=$((c2+1));;
        *)                extra="${extra}${extra:+ }$fq";;
      esac
    done
    if [ "$c1" -gt 0 ] && [ "$c1" -eq "$c2" ]; then
      READS1=$r1
      READS2=$r2
      EXTRA=$extra
      return 0
    fi
    # else: try next separator
  done
  return 1
}
```

Same spirit as std.sh, shell agnostic, as portable as possible with shell limitations.

Apart from the `builtin/std.sh` and the `cond()` keyword, `command` is like before a shell instruction specified in a `fr""` string. Because it's an fr string, `{sample.sample_accession}` is interpreted in python, not in shell.

#### Real inputs and outputs

`inputs` are taken from our sample item, `sample.fastqs` provides a ready made URI list providing all the FASTQs for that specific sample. Strings object, in the scitq style using rclone components or specific URIs. Here ENA and SRA functions provide ready made specific URI for biological samples.

##### Specific biological URIs

ENA or SRA provides in the `.fastqs` field of the Sample object a list of specific URIs: 

`run+fastq://<run_accession>` 

This will provide the cleaned FASTQs provided by public databanks. The URI accept several options such as the transport option, option in URIs are specified with `@<option-name>`. For instance, one can use `@ena-ftp` transport option:

`run+fastq@ena-ftp://<run_accession>` 

This will force ENA FTP transport option.

The following transport options are available:
- `@ena-ftp` : use ENA FTP,
- `@ena-aspera` : use ENA Aspera (currently not recommanded),
- `@sra-tools` : use SRA tools.

`ENA()` will decorate the URI with the appropriate transport option if `use_ftp` is set to True, or if `use_aspera` is set to True. `SRA()` will use `@sra-tools` by default.

There is a last option, `@only-read1`, that is automatically added if you specify `layout=SINGLE` in `ENA()` or `SRA()` and the sample is detected to be paired. 

In the resulting URI, options can be cumulated:
`run+fastq@ena-ftp@ena-aspera@only-read1://<run_accession>` 

Which means use first ena-ftp, then ena-aspera, and filter for only read1 FASTQ(s).

##### Outputs

The outputs field takes an `Outputs` object. This object enable to define one or several output connectors to the step that can be plugged into other steps inputs. The concept is (shamelessly) borrowed from Nextflow. 

Here we read:

```python
outputs=Outputs(fastqs="*.fastq.gz", json="*.json"),
```

which means, there are two types of output produced in the /output folder, FASTQ files and JSON files, some steps will need the first ones, some other steps will need the second ones. This will also clarify what is needed by each step. This is entirely facultative. 

If outputs are only used as a whole, you don't need to specify outputs at all. Contrarily to what happens in [CLI](cli.md#task-create), an output folder is automatically provided to Tasks provided a Workspace is defined, which is automatically the case if the provider and region defined for the Workflow object are associated with a workspace root.

If a workspace root is correctly defined for the provider/region (see [configuration](../reference/configuration.md)), the automatic output folder of a task is this:
```python
f"{wf.workspace_root}/{wf.full_name}/{task.full_name}/"
```

Note that the default region, the `local` region of `local.local` provider, does not have a `local_workspace` by default, it should be defined in the server YAML configuration if you want the output to be automatically managed even with the local provider:
```yaml
providers:
  local:
    local:
      local_workspaces:
        local: "s3://local/workspace"
```

You may also use a publish clause which allow you to specify any custom path you want:

```python
Step(
    [...]
    outputs=Outputs(publish='azure://results/myproject/')
)
```

There is also a specific meaning attached to this: the default workspace that depends on the workspace root is deemed a temporary workspace, it may be deleted to get some free space or to reduce costs. Published outputs represent data stored specifically in a separate space that is here to stay.

#### Real task_specs

Nothing new here compared to previous examples. Here our TaskSpec object uses dynamic concurrency which is often recommended in production.

#### Real step worker_pool

First it is safe not to specify anything here, including in a production setup. There are two reasons why you would want to specify a worker pool different from the workflow worker pool:
- to restrict the number of workers - which is done here, the workflow worker pool is cloned but with a restriction on the number of workers: this will force the recruiter to reserve some workers for the other steps of the workflow,
- to change the type of workers required: if this particular step requires a different kind of worker, you can change here.

```python
worker_pool=workflow.worker_pool.clone_with(max_recruited=5)
```

It is good practice to use this cloning style with restriction on max_recruited (you cannot enlarge the pool size past the workflow pool size). It maximizes the chance of recycling a worker, something that will save useful computation time and money. Only change the worker pool if the step has strong requirements on specific hardware (like some large disk space or GPU).

### Other steps

```python
humanfilter = workflow.Step(
    name="humanfilter",
    tag=sample.sample_accession,
    command=cond(
        (params.paired,
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
```

There is very little difference with the previous step, except for one thing: the inputs:
```python
inputs=fastp.output("fastqs"),
```
Here we use the outputs system we described previously in the previous step but as an input source for the next step. It may be used with a connector (which requires specifying connectors in the previous step affecting an `Outputs(<connector_name>=<globbing pattern>)` object to outputs) or without (`inputs=fastp.output()`) in which case all output files are taken.

This does two things:
- it set inputs as in [CLI](cli.md#task-create),
- it creates a dependency link between the two tasks.

It is possible to dissociate input and dependency using the `depends` Step attribute in addition to the `inputs`, but it is more idiomatic to use a previous step `output()` clause which generate a specific `Output` object that carries both information.

NB in certain steps below the first one, you'll see `cond()` keyword used for inputs, which enable to have flexible templates with optional steps. This does not introduce any new concept, so we'll pass directly to the last final step.

### Real final step

```python
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
```

We had already introduced the final step concept previously, so you should have no surprises here:
- this step is out of the main loop,
- and in the input we see the `grouped` keyword used, meaning that it does not depends only on the last metaphlan task but on all of them.

One subtlety here though, this is specified differently from what we saw previously:

```python
inputs=metaphlan.output("metaphlan",grouped=True),
```

VS

```python
depends=step3.grouped(),
```

So it does the two-in-one action of specifying an output object (with `.output()`) as inputs, which beside specifying inputs files also carries the dependancy information. And it does not use `metaphlan.grouped()`. Well there is no reason to that, both expressions are correct:

```python
inputs=metaphlan.output("metaphlan",grouped=True),
```

is syntactic sugar for:
```python
inputs=metaphlan.grouped().output("metaphlan"),
```

Use whatever suits you best.

## Reference

see [DSL Reference](../reference/python_dsl.md)





