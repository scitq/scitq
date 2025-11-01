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

The Step constructor take at least two arguments:
- command: the Unix command line instruction passed to the language interpreter that we defined above,
- container: the name of a Docker container to use to execute the command.

The constructor takes also an optional name which is recommended. 

You may note that the command string is an "fr" string, which is a standard python object, but not useful here, and not commonly used in python. It is because using "fr" string as command is idiomatic in scitq DSL. The reason is that it prevents several errors and help the DSL to detect mistakes. "fr" string makes all shell variable usage look like this `fr"echo '${{HELLOWORLD}}'"`, so if the DSL see something like this: `fr"echo '${HELLOWORLD}'"` it can emit a warning as this look very much like a syntax error. Using an r string makes backslash use `\` less error prone. Not using "fr" string as command will trigger a warning when importing the DSL script.
