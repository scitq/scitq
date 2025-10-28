from scitq2 import *

class Params(metaclass=ParamSpec):
    name = Param.string(required=True, help="State your name here")
    how_many = Param.integer(default=1, help="How many times to say hello")

def helloworld(params: Params):

    workflow = Workflow(
        name="helloworld-multi",
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
            command=fr"echo 'Hello {params.name}'",
            container="alpine",
            task_spec=TaskSpec(concurrency=params.how_many)
        )

    
run(helloworld)