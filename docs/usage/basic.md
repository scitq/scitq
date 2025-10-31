# Basic usage

scitq enables you to distribute tasks to a group of workers.


## Defining a task

A task is defined as a command in a Docker container. The command is either the call of an executable, a shell instruction or a series of lines in an interpreter (like python). Detailed examples are provided in [CLI usage](cli.md#task-create).

Tasks can either do something, in which case you may just want to know that it succeeded or not and look at its output flow (stdout), or produce something and you will want to recover an output file. Similarly, your task may require input provided via the command line or an input file. This is also detailed in previous link, and scitq will help you cover these different use case.


## Defining workers

There are two kinds of workers:
- manually deployed workers: these are servers on which scitq client was installed, see [install](../install.md#installing-manually-a-worker),
- automatically deployed workers: these are created and installed by scitq itself, they require configured instance providers. Look in [server config example](../../sample_files/scitq.yaml) how it is done.


## Managing which workers execute which tasks

By default all workers do all tasks. 

More precisely, workers and tasks are grouped by "Steps". By default, they all belong to the "no-step" group. By [defining a step](cli.md#step-create) (which requires a [workflow](cli.md#workflow-create)), you will be able to address [tasks](cli.md#task-create) to [workers](cli.md#worker-deploy).


## Automate

One of the strengths of scitq is its ability to manage workers automatically. You can set up a rule on how to deploy new workers for a given step: this is called a [recruiter](cli.md#recruiter-create). Steps within a given workflow will happily share workers, so that no workers stay idle for long. If there is nothing that the worker can do, scitq will delete it.

NB: manually deployed workers, also called permanent workers, are never deleted.


## Complex setups

There is no limit to what you can do with the CLI. However, it can become tedious when there are lots of different tasks with numerous dependencies. For this reason, scitq provides you with a [DSL](dsl.md) that will help you define complex workflows using Python. These workflow templates will then become available as standard commands in scitq so that end-level users can trigger complex distributed analyses with the tip of their fingers.


## Using the UI

scitq [UI](ui.md) also enables you to use the defined workflow templates, but its main purpose is not to launch tasks. It is designed to monitor workflows in real time, inspect worker activity, and fine-tune execution live. scitq keeps real tasks close to you — showing live output and errors with sub-second latency and allowing you to adjust behavior on the fly.
