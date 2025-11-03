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

