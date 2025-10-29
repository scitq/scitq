# Motivation and design principles

scitq is meant to distribute tasks that can be expressed as Unix command line instructions. The initial idea of scitq came from [Celery,](https://github.com/celery/celery) which is really an excellent project with a much wider audience than this one. However in our particular context, Celery had a lot of quality: ease of deploying, ease of usage, and a very simple model of "concurrency" to handle the optimization of worker workload. Yet Celery does not provide a few things that we needed: 
- Detailed task output (stdout and stderr) - since Celery is not centered on Unix command line.
- manual task management - our tasks tend to have a hard-to-predict failure rate making retry strategies hard to automatize completely,
- worker life cycle (not the goal of Celery but we need that),
- data storage logistic (same, not the goal of Celery but we need to download or upload data from and to a lot of different places)

So scitq is an all-in-one solution to address these different points.

## Architectural choices

The first version of scitq, [scitq v1](https://github.com/gmtsciencedev/scitq), has been developed in python, using REST (with [flask](https://github.com/pallets/flask)), integrating [rclone](https://github.com/rclone/rclone) for data management and relying on [Ansible](https://github.com/ansible/ansible) for worker life cycle. 

We have learned a lot from the v1. It has been very successful in its mission within [GMT Science](https://gmt.bio) for years. Now, when we look back, notably the latest choice, Ansible, was a mistake. Ansible is (probably) a good solution in different contexts but it is not adapted to manage temporary resources, it is very slow in this context and its management of python venv is... terrible, in my opinion. Making Ansible run automatically as part of another system proved quite painful. Ansible is meant to be deployed to manage your resources and is (probably) good at it but trying to embed Ansible in something else is something I strongly advise against. Then also REST suffers some performance issues as soon as you want low latency or do streaming. Also, the v1 UI is relying on old plain JS code with no framework, making it hard to maintain and messy, clearly a youth mistake also.

So for v2 we chose:
- to manage worker life cycle directly using official API from the provider in our choice language,
- use gRPC instead of REST.

These two basic choices plus the fact that we wanted to embed rclone again lead very directly to use go instead of python as the main language for the core scitq engine. Go proved very quickly invaluable for other issues, notably for its parallel execution model and its wide support for a lot of API.

So we could now import rclone code within go, something that was a lot more efficient and enabled a much better control than relying on the command line (which we did in python).

For the UI, we wanted a framework, but something as simple as possible, looked in different directions and ended with [Svelte](https://github.com/sveltejs/svelte). And we are really happy with this choice.

A last important component of scitq is the workflow language, something which was borrowed from other systems (see below), not present in Celery. In scitq v1 we used python, which is good for scripting and overall we had no issues with this system, we tested several ideas but eventually we ended up with a system that is quite similar to the v1, a python library to declare the different steps and tasks to do.

## Comparison with Nextflow

Before going to our own software, we had looked mostly at Nextflow because we're in Bioinformatics and Nextflow is becoming a de facto standard. It is almost impossible to tell anybody in the field that Nextflow is not a good solution and certainly:

- it has a very good abstraction system to declare a workflow,

- it has a large library of ready-made recipes for all kinds of bioinformatics topics. And since the abstraction of the workflow is well made, the reusability of recipes is excellent. 

To be honest, these are what I've seen in others practice rather than mine. We, on the other hand, are keen on being very specific on the stuff we do, and we don't use ready-made recipes, we like to take each piece of software and understand in depth what we do with each. Thus we rewrite things. And doing so, which, we understand, is not the main bioinformatics user needs, we had to write and debug our own Nextflow workflows a lot. Our experience on that score was not very positive. First Nextflow workflows are written in Groovy and Groovy may make sense in Java universe but overall is a relatively obscure scripting language, so whenever you do something slightly non-standard in Nextflow, it gets in your way - at least it felt like this for us who are unfamiliar with Groovy. 

When we deployed Nextflow, we wanted to be able to use both our internal resources and some providers and possibly several of them. The option we found in Nextflow that matches this need is Kubernetes support. So we invested some time in tuning a Kubernetes setup. My personal advice to anyone reading thus far is: don't do that. Just don't. Kubernetes is meant to be deployed in very large setups, trying to manage it oneself is generally speaking a bad idea. So while it was not Nextflow fault per se, the ending result was very unsatisfying. Nextflow dependancy on these heavy systems was clearly what decided us to quit this solution.

Apart from this practical issues we felt that Nextflow abstraction prevented us from fine tuning what was happening at each task level and each worker level, something for which Celery was very good.

So we took the abstract workflow idea from Nextflow, the simplicity of use of Celery, and we focused on a system where you still can get your hands in each task and understand what happen at low level whenever you need to.