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