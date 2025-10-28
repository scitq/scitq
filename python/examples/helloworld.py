from scitq2 import *

class Params(metaclass=ParamSpec):
    name = Param.string(required=True, help="State your name here")

def helloworld(params: Params):

    workflow = Workflow(
        name="helloworld",
        description="Minimal workflow example",
        version="1.0.0",
        tag=f"{params.name}",
        language=Shell("sh"),
        worker_pool=WorkerPool(W.provider=="local.local", W.region=="local")
    )

    step = workflow.Step(
        name="hello",
        command=fr"echo 'Hello {params.name}'",
        container="alpine"
    )

    workflow.Step(
        name="next",
        command=fr'echo "Hello again, {params.name}"',
        depends=step,
        container="alpine"
    )
    
run(helloworld)