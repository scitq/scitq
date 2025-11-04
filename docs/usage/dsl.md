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

The last two options in the WorkerPool are `max_recruited`, this is the maximum for this workflow, and `task_batches`. `task_batches` is the target number of batches each worker must do to complete the total number of tasks of each type the workflow. So if each worker can do 2 tasks in the meantime (concurrency=2) and `task_batches` is 2, it means each worker should do 2*2 = 4 tasks total in two rounds. So it there are 40 tasks to do, we need 10 workers.

Note that we did not specify the region and provider in the WorkerPool like we did before and we are going to see why just now.

#### provider and region in a real world workflow

Provider and region are specified but this time not in the WorkerPool itself. The reason is that it is used for two things:
- the WorkerPool will look for the Worker provider and region setting if has none of its own (so there no difference at WorkerPool level where you define them),
- the Workflow also uses that to chose a local workspace.

If you look in [configuration](../reference/configuration.md) and in example configurations, you'll see that each region may have a workspace. The reason is the same than we've seen before: transfer fees. Since it costly to move data out of a region, it is essential that working data stay in the region. For this reason, Workflow objects have an underlying workspace that is regionalized, and which is chosen using the region and the configuration settings.



## Reference

see [DSL Reference](../reference/python_dsl.md)





