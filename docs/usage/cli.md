# CLI Reference

## Login

scitq uses user authentication so you must login (the admin user and password is set in the server configuration, `scitq.yaml`, once logged as admin, see below, you can create other users). 

To manage your authentication it is required to have a token in memory set in a shell variable, `SCITQ_TOKEN`, so the recommended way to do that is to type this each time you restart a session:

```sh
export SCITQ_TOKEN=$(scitq login)
```

Once you have the token, you can set it in a shell environment script (like `.profile`), but if your privacy matters to you, this is not recommended, prefer refreshing the token in memory each time.

Alternatively, pass `--server` and `--token` directly on each command (useful for scripting and AI agents — see [Global flags](#global-flags)):

```sh
TOKEN=$(scitq login --user admin --password mypass --server myserver:443 --json | jq -r .token)
scitq template list --server myserver:443 --token $TOKEN --json
```

NB: if you forget to login, the CLI will recall that to you.

If this does not work, and you get a message like this:
```
2025/10/29 17:58:22 login failed: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp [::1]:50051: connect: connection refused" (xxxxx)
```

It means that the server is not running locally. In that case you need either to start the server... (the CLI requires a running server), or to tell the CLI where the server resides (the CLI believe it to be local by default), which is done by setting in your environment the address of the server:

```sh
SCITQ_SERVER=<server_fqdn>:<port>
```

(both items are configuration entries in `scitq.yaml`).

This can safely be added in your shell environment script (and maybe at server level in `/etc/profile` since it is not specific to a user).

If you continue to have connection errors while this is correctly set and the server is running, it's likely a network issue (firewall, routing). Check with a specific software like nmap: `nmap <server_fqdn> -p<port>` (which will tell you if the port is open or not).

If your password or login is wrong, you'll get this message.
```
2025/10/29 17:04:02 login failed: rpc error: code = Unauthenticated desc = invalid credentials (xxxxx)
```

## Global flags

These flags apply to all commands:

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--server`, `-s` | `SCITQ_SERVER` | `localhost:50051` | gRPC server address |
| `--token`, `-T` | `SCITQ_TOKEN` | (empty) | Authentication token |
| `--timeout`, `-t` | | `300` | Timeout in seconds |
| `--json` | | `false` | Output in JSON format |

The `--server` and `--token` flags allow fully stateless usage without environment variables, which is useful for scripting and AI agents:

```sh
# Login and capture token in one step
TOKEN=$(scitq login --user admin --password mypass --server beta2.gmt.bio:443 --json | jq -r .token)

# Use token directly — no env vars needed
scitq template list --server beta2.gmt.bio:443 --token $TOKEN --json
scitq template run --name biomscope --param 'bioproject=PRJEB6070' --server beta2.gmt.bio:443 --token $TOKEN --json
```

The `--json` flag makes every command output structured JSON instead of human-readable text. This enables:
- **Scripting**: pipe output through `jq` or similar tools
- **AI agents**: parse structured data without regex
- **Automation**: integrate scitq into CI/CD pipelines

## CLI logic

The CLI use the following logic scheme:

```sh
scitq <object> <action> --option1 ... --option2 ...
```

You can always type `scitq --help` or `scitq <object> --help` or `scitq <object> <action> -help` to have details on any CLI command.

### `task`

#### `task create`

Create a new task in scitq. This task will be handled by the first worker that can be assigned to it. Task and Worker are assigned to small work groups called steps. Only workers belonging to your task step will be able to handle it. To keep things simple, we will first stay in the default situation where there are no steps for either tasks or workers.

The minimal command to create a task is:

```sh
scitq task create --container <docker container name> --command '<a shell command to run (executed in docker entry context)>'
```

So for instance, this is the hello world command:
```sh
scitq task create --container alpine --command 'echo "hello world"'
```

Here the command is very simple so it can be executed directly, if your command needs some piping or shell specificity, you'll need to tell scitq to execute it in a shell:
```sh
scitq task create --container alpine --shell sh --command 'echo "hello world"|tr "h" "H"'
```

You can also pass some python code thus:
```sh
scitq task create --container python:3.12 --shell python --command "print('Hello world')
print('Python rules!')"
```

##### resources

If you need to pass a binary command of your own, you must have a storage setup in your `scitq.yaml` `rclone` section. All [rclone supported storages](https://rclone.org/overview/) are supported by scitq. Say you have an AWS S3 storage defined as `s3`.

So first you need to copy the binary to the storage:
```sh
scitq file copy mybinary s3://bucket/resource/mybinary
```

Then you can specify the task to use this binary as a resource:
```sh
scitq task create --resource s3://bucket/resource/mybinary --container alpine --command "/resource/mybinary"
```

As you can see, resources are mounted in `/resource` folder in the docker container you've chosen. 

##### input and output

This is not the only folder that is mounted in the docker container, there are also the `/input` and `/output` folders, which are used to respectively provide some input data to the task and retrieve output data from the task. Say `mybinary` takes an input file with `-i` and produce an output file specified by `-o`. Let's pretend our input data is a file available on our `s3://bucket`: `s3://bucket/data/input1.dat`.

```sh
scitq task create --resource s3://bucket/resource/mybinary --input s3://bucket/data/input1.dat --output s3://bucket/results/mybinary-input1/ --container alpine --command "/resource/mybinary -i /input/input1.dat -o /output/output.dat"
```

Contrarily to `--resource`/`--input`, `--output` must be a folder (it needs not to exist before creating the task) and can only be specified once, everything that is written to the `/output/` folder will be copied to this location at the end of the task (whether it succeeds or not). `--input` is a generally speaking a file, you can have several of them, and specify a folder as `--input` is possible, and it means all the (recursive) content of the folder. Glob patterns such as `s3://bucket/data/*.dat` are also possible as an input pattern (but not fancy patterns such as curly brace ones, the only special char admitted is `*`). `--output` does not have this flexibility, it is necessarily a folder.

At a first glance, `--resource` and `--input` seem similar, both are files, can be specified several times and offer some flexibility by allowing also folders and glob patterns. They differ in two ways:
- first they are made available to tasks through two different folders,
- second, resources are shared between tasks, while inputs are unique. For this reason `/resource` is a read-only folder contrarily to `/input`. For a binary that is not modified during execution, it is best brought to the task as a resource. It is the same for a reference data. Processed data are typically inputs and not resources.

##### URI action

The specification of input, resource or output uses a specific syntax that is called a URI for Uniform Resource Identifyer. It resembles the internet URL except it is more general since unusual protocols such as `s3://` or `azure://` or `gcp://` can be accepted (provided an rclone resource named s3, azure or gcp exists in `scitq.yaml` rclone section). 

An URI action is an extension of a resource or an input URI that allows a transformation of the data before being mounted into the task. It is mostly used for resources since as being read only they cannot be modified by the task. If a task uses a complex resource that is made of several files, it is most convenient to distribute it as a TAR archive that is decompressed. This is done simply by adding `|untar` to the resource specification. If the resource is a collection of files present in `reference_data.tgz`, one can write:

```sh
scitq task create --resource s3://bucket/resource/mybinary --resource 's3://bucket/resource/reference_data.tgz|untar' --input s3://bucket/data/input1.dat --output s3://bucket/results/mybinary-input1/ --container alpine --command "/resource/mybinary -i /input/input1.dat -o /output/output.dat"
```

Docker containers are better when slim, which is why large reference data files are better distributed out of the container, and scitq resource system is intended for that, with or without action.

There are three possible actions, `|untar`, `|gunzip` and `|mv:...`:
- `untar` dearchive TAR archives (compressed or not, .tgz or tar.bz2 archives, or even .zip files are handled gracefully). Contrarily to `tar xf ...`, the untar action consumes the archive which is destroyed and replace by its content,
- `gunzip` decompress a gzipped file.  Like the `gunzip` command the `untar` action, it consumes the compressed file which is destroyed and replace by its content,
- `mv:...` is an action that moves the downloaded file or folder to a subfolder within `/input` or `/resource`. Technically it is not downloaded then moved, it is directly downloaded to the final destination (thus it would not overwrite a file with the same name at the root of `/input` or `/resource`). The final destination does not need to exist before, if it is missing it is created. Relative destinations like `..` are forbidden.

Note: while mainly used for resources, URI action also works for inputs. They are only forbidden for output.


##### Dependencies

A task may depend on other tasks, this is declared by the `--dependency <task id>` flag which can be repeated. A task created with dependencies will wait (see waiting status below) until all the tasks it depends upon succeed.

#### `task list`

List action exists for almost all objects and list this kind of object.

```sh
scitq task list
```

Typically list all the tasks. The different options for this action are filters :
- `--limit` limits the number of displayed tasks which default to 20. The tasks are displayed most recent task first, so by default `scitq task list` displays the last 20 tasks created.
- `--offset` is a pagination option and allow to display the number of tasks defined by `--limit` but starting from this position in the list,
- `--status <status>` display only the tasks with this status.

Each status is defined by a letter, and there are 4 primary statuses that are very important in scitq, the (P)ending, (R)unning and (S)ucceeded or (F)ailed status. However, there is a lot of subtlety in the way tasks are handled in scitq, so here are all the status more or less in their progression order:

##### Task status codes

| Letter | Status             | Description                                                                                  |
|--------|--------------------|----------------------------------------------------------------------------------------------|
| **W** | waiting            | The task is not ready to be launched (depends on another task not yet succeeded).                |
| **P** | pending            | The task is ready to be launched.                                                               |
| **A** | assigned           | The task has been assigned to a worker (worker not yet aware).                                  |
| **C** | accepted           | A worker has acknowledged and accepted the task.                                             |
| **D** | downloading        | The task is preparing by downloading inputs, resources, and containers.                         |
| **O** | on hold            | The task is ready but the worker has no available capacity.                                     |
| **R** | running            | The task is currently executing.                                                                |
| **U** | uploading          | The task ran successfully and is uploading output data.                                         |
| **V** | uploading (failed) | The task failed, uploading output or logs.                                                     |
| **S** | succeeded          | The task was completed successfully and upload finished.                                           |
| **F** | failed             | The task failed during execution or upload.                                                    |
| **Z** | suspended          | The task is paused and can resume later.                                                       |
| **X** | canceled           | The task was canceled before running.                                                          |
| **I** | inactive           | The task is defined but waiting for the workflow to be finalized before becoming pending.      |

- `--worker-id` filter tasks associated to this worker (minimal status `A`)
- `--step-id` filter tasks related to this step (see step below)
- `--workflow-id` filter tasks related to this workflow (see workflow below)
- `--command` filter tasks containing this substring in their command (use of % is possible as a wide car for globbing)
- `--show-hidden` display hidden tasks.

About hidden tasks: A task transmitted to a worker cannot be changed. Thus if the task fails to run for any reason (like if the worker disappear, or if the command simply failed), it won't be modified to remember what happened. It can be retried, though, either automatically in workflows or manually when created with the CLI. When retried, the old task is hidden and a new one is created. The reason for that is that in most cases, what matters is the latest attempt of the task, especially if the outcome is different (e.g., it eventually succeeds). So by default, previous attempts are hidden and not displayed. Showing hidden tasks, however, permits to see previous failure to understand what happened. 

#### `task retry`

As shown just above a failed task can be retried: e.g., the previous (failed) task is hidden and a new pending task is created:

```sh
scitq task retry --id <task id>
```

The action optionally permits placing an auto-retry on the task, so to retry the task and let retry three more times if needed:

```sh
scitq task retry --id <task id> --retry 3
```

#### `task stdout`

Streams the standard output (stdout) of a task.

```sh
scitq task stdout --id <task id>
```

#### `task stderr`

Streams the standard error (stderr) of a task.

```sh
scitq task stderr --id <task id>
```

#### `task logs`

Streams both stdout and stderr of a task, with colors (blue for stdout, red for stderr).

```sh
scitq task logs --id <task id>
```

#### `task kill`

Sends a kill signal to a running task. The signal is delivered on the next worker ping cycle (typically within 5 seconds) and the task's Docker container is killed.

```sh
scitq task kill --id <task id>
```

The task will transition to failed (F) status after being killed. If the task has retries remaining, it will be retried automatically.

#### `task stop`

Sends a graceful stop signal (SIGTERM) to a running task. The container's process receives SIGTERM and has a grace period to exit cleanly before being killed with SIGKILL. Useful for Optuna-style pruning of unpromising trials.

```sh
scitq task stop --id <task id> [--grace <seconds>]
```

Options:
- `--grace`: seconds the container has to exit after SIGTERM before SIGKILL (default: 10). Increase for programs that need time to save state (e.g. `--grace 60`).

#### `task edit`

Edits a failed task's command and retries it (hides the old task and creates a new one with the modified command):

```sh
scitq task edit --id <task id> --command "fixed_command ..."
```

#### Duration in task list

The `task list` output includes duration information:

- **Completed tasks** (S or F): shows the total run duration (e.g. `Duration: 4m32s`)
- **Running tasks** (R): shows the elapsed time since execution started (e.g. `Running: 2m15s`)

### `flavor`

`flavor` is the term coined by Openstack to describe a type of instance, scitq kept it.

#### `flavor list`

`list` is the only available action for `flavor` objects. It lists instance types or server models. It takes two options:
- `--limit` default to 10, list the cheapest flavors matching the filtering criteria (see below),
- `--filter` apply a filter on listed flavors.

Filters are a column `:` separated list of simple requirements of the form:
- `cpu>32` : flavor with strictly more than 32 vcpu,
- `mem>=10` : flavor with 10Gb or more memory (e.g. RAM, not disks, see below),
- `disk<1000` : flavor with a total disk space below 1000Gb.

So for instance:
```sh
scitq flavor list --filter 'cpu>32:mem>=10:disk<1000'
```

Filters can contain other criteria:
- `provider~%azure%` : the provider name must contain azure (providers are always in small caps),
- `region~%swed%` : the region must contain swed (swedencentral is a great Azure region),
- `eviction<50` : the eviction(*) stats (not always reliable) must be below 50% (which is very high). If you say nothing, by default a filter `eviction<=5` is applied - which is a sound default (and Azure minimal stat) since a high eviction rate will make workers very inefficient.
- `gpumem>=10` : the instance has more than 10Gb of GPU memory.
- `gpu~%Tesla%` : the description of the GPU contains Tesla.

(*) eviction is the Azure expression for reclaiming an instance (only spot instances are reclaimed, but they are very cheap and used by default in scitq). A reclaimed instance is killed. You can revive it but it is generally not efficient so the current strategy is to delete it and redeploy.

NB: if a `scitq flavor list` without filter gives you an empty list, it's likely that the providers’ updates are not properly configured in `scitq.yaml`, see [configuration](../reference/configuration.md).

### `worker`

Workers are the compute units that actually execute tasks. They can be deployed automatically by the recruiter engine or manually through the CLI.  
Each worker is associated with a **provider** (e.g. Azure, OpenStack), an optional **region**, and optionally a **step** it serves in a workflow.  

#### `worker list`

Lists all workers known to the scheduler.

```sh
scitq worker list
```

Displays for each worker its ID, name, status, concurrency, prefetch value, IP addresses, flavor, provider, and region.

#### `worker deploy`

Deploys a new worker manually.  
This command queries available flavors to match the specified provider, region, and flavor name, then creates the requested number of instances.

```sh
scitq worker deploy --provider <provider.config> --flavor <flavor_name> [--region <region>] [--count <n>] [--step <step_id>] [--concurrency <n>] [--prefetch <n>]
```

Options:

- `--provider` (required): provider and configuration name, e.g. `azure.primary`.  
- `--flavor` (required): flavor name identifying the instance type.  
- `--region`: optional region (defaults to the provider’s default).  
- `--count`: number of workers to deploy (default: `1`).  
- `--step`: optional step ID to attach the worker to.  
- `--concurrency`: number of tasks the worker can run in parallel (default: `1`).  
- `--prefetch`: number of tasks pre-fetched by the worker (default: `0`).  

Example:

```sh
scitq worker deploy --provider azure.primary --region swedencentral --flavor Standard_D8s_v3 --count 2 --step 14 --concurrency 4
```

This deploys two workers on Azure, each able to execute four tasks concurrently for step ID 14.

#### `worker delete`

Deletes a worker by its ID.

```sh
scitq worker delete --worker-id <id>
```

The command sends a deletion order to the provider.  
Use with care, since ongoing tasks will be interrupted.

#### `worker stats`

Displays live resource usage for one or several workers.

```sh
scitq worker stats --worker-id <id1> [--worker-id <id2> ...]
```

Shows, for each worker:
- CPU and memory usage
- system load and I/O wait
- disk usage and read/write rates
- network input/output throughput

Example:

```sh
scitq worker stats --worker-id 12 --worker-id 13
```


### `workflow`

Workflows are structured collections of steps, each step grouping related tasks.  
They define the logical flow of computation — for example, preprocessing → alignment → analysis.

#### `workflow list`

Lists all workflows currently registered on the server.

```sh
scitq workflow list
```

Displays for each workflow its ID, name, run strategy, and maximum allowed workers (if any).

#### `workflow create`

Creates a new workflow.

```sh
scitq workflow create --name <workflow_name> [--run-strategy <strategy>] [--maximum-workers <n>]
```

Options:

- `--name` (required): human-readable name of the workflow.  
- `--run-strategy`: controls how tasks are distributed between workers.  
  - `B`: **Batch-wise** (default) — workers focus on completing one step before moving to the next.  
  - `T`: **Thread-wise** — workers advance tasks as far as possible across steps.  
  - `D`: **Debug** mode — tasks are not assigned automatically; use the DSL `--debug` flag to enter interactive task selection. Recruitment is limited to 1 worker. When exiting debug to normal execution, the original maximum workers setting is restored.
  - `Z`: **Suspended** — workflow is created but not yet launched.  
- `--maximum-workers`: caps the total number of workers the workflow can use.
- `--live`: live mode — prevents the workflow from auto-completing when all tasks finish. Used for optimization loops where new tasks are submitted dynamically. Close with `workflow update --status S` when done.

Example:

```sh
scitq workflow create --name myworkflow --run-strategy B --maximum-workers 50
scitq workflow create --name optuna_search --live    # for optimization loops
```

#### `workflow delete`

Deletes a workflow by ID.

```sh
scitq workflow delete --id <workflow_id>
```

The workflow and its associated steps and tasks will be removed.  
Use with care, as this cannot be undone.

### `step`

A step is a subdivision of a workflow that groups tasks requiring similar execution environments (same container, resources, or logic).  
Steps generally represent the order: tasks in step *N* usually depend on successful completion of step *N−1*. But dependencies are implemented at task level, so the real logic may be very different than this simple rule suggests.

#### `step list`

Lists all steps belonging to a given workflow.

```sh
scitq step list --workflow-id <workflow_id>
```

Displays for each step its ID and name.

#### `step create`

Creates a new step within a workflow.

```sh
scitq step create --workflow-id <workflow_id> --name <step_name>
```

Options:

- `--workflow-id`: numeric ID of the workflow to attach this step to.  
- `--workflow-name`: alternative to `--workflow-id` (resolves the ID automatically).  
- `--name`: required, name of the step.

Example:

```sh
scitq step create --workflow-name myworkflow --name quality_control
```

#### `step delete`

Deletes a step by ID.

```sh
scitq step delete --id <step_id>
```

Removes the step and its task associations.

#### `step stats`

Displays detailed runtime statistics for one or several steps.

```sh
scitq step stats [--workflow-id <workflow_id>] [--workflow-name <workflow_name>] [--step-id <id1>,<id2>...] [--task-name <substring>] [--totals]
```

Options:

- `--workflow-id` or `--workflow-name`: filter stats by workflow.  
- `--step-id`: specific step IDs to include (comma-separated).  
- `--task-name`: only include steps containing tasks with this substring in their name.  
- `--totals`: adds a totals line summarizing cumulative execution times.

The output includes, per step:
- number of tasks per status (waiting, running, succeeded, failed, etc.)
- average and total durations of each execution phase
- start and end timestamps
- cumulative totals if `--totals` is used

Example:

```sh
scitq step stats --workflow-name myworkflow --totals
```

### `recruiter`

Recruiters are the components that automatically manage worker deployment and recycling for workflow steps.  
Each recruiter applies a filter to select valid flavors and providers, defines concurrency and prefetch rules, and determines how many workers to maintain.

#### `recruiter list`

Lists recruiters registered on the server.

```sh
scitq recruiter list [--step-id <id>]
```

Displays for each recruiter:
- step ID and rank
- filter expression
- concurrency (fixed or adaptive)
- prefetch or prefetch percentage
- adaptive parameters (CPU, memory, disk per task)
- number of rounds, timeout, and maximum workers

Example output:

```
Step 12 | Rank 1 | Filter cpu>=8:mem>=30 | Concurrency=4 Prefetch=1 Rounds=3 Timeout=10 Maximum Workers=50
```

#### `recruiter create`

Creates a new recruiter.

```sh
scitq recruiter create --step-id <id> --filter <expr> [--rank <n>] [--concurrency <n>] [--prefetch <n>] [--prefetch-percent <n>] [--cpu-per-task <n>] [--memory-per-task <gb>] [--disk-per-task <gb>] [--concurrency-min <n>] [--concurrency-max <n>] [--max-workers <n>] [--rounds <n>] [--timeout <s>]
```

- `--filter`: selection rule for compatible flavors (e.g. `"cpu>=8:mem>=16:region~%central%"`) (see flavor list above for a complete description)

Options allow both **fixed concurrency** and **adaptive concurrency** modes:
**Fixed concurrency** means any recruited worker will have the same concurrency and prefetch will have the same number of concurrent task executions allowed:
- `--concurrency`: fixed number of concurrent tasks per worker
- `--prefetch`: task prefetching configuration

**Adaptative concurrency** means the number of concurrent task executions allowed depends on the worker capacity and the task requirement (if the cpu is limiting, then cpu requirement dictates the concurrency, if the memory is limiting it's the memory, etc.): 
- `--cpu-per-task`, `--memory-per-task`, `--disk-per-task`: adaptive concurrency resource scaling
- `--concurrency-min`, `--concurrency-max`: adaptive concurrency bounds
- `--prefetch-percent`: express prefetch as a percentage of concurrency, thus 50 means when the adaptative concurrency sets to 10, prefetch sets to 5.

- `--max-workers`: upper limit of workers managed by this recruiter
- `--rounds`: number of execution rounds to tarket to complete the workload
- `--timeout`: seconds between recruitment cycles

The round setting is a key tactical number for a recruitment rule. If a step has 100 tasks to do, and each worker ends up with a concurrency of 10. If rounds is set to 1 (its default), it means recruit 10 workers to execute all the tasks as quickly as possible. If rounds is set to 2, then only 5 workers will be recruited.

If your task requires a significant amount of preparation (lots of download, long loading time), then increasing the rounds paramater is likely to be more efficient in terms of costs (at the price of a little more delay).

Note: deployment is also governed by the **quotas** defined in `scitq.yaml` for each provider and region (CPU, memory, and instance count limits). If the quota is reached, recruiters will wait until workers are deleted before deploying new ones. See the [configuration reference](../reference/configuration.md#quota-per-region-resource-limits) for details.

Examples:

```sh
scitq recruiter create --step-id 14 --filter 'cpu>=8:mem>=30' --concurrency 4 --prefetch 1 --max-workers 50
```

```sh
scitq recruiter create --step-id 14 --filter 'cpu>=8:mem>=30' --cpu-per-task 2 --prefetch-percent 50 --max-workers 50
```


#### `recruiter delete`

Deletes a recruiter by its step and rank.

```sh
scitq recruiter delete --step-id <id> --rank <n>
```

Example:

```sh
scitq recruiter delete --step-id 14 --rank 1
```

Removes the recruiter from the scheduler.  
Existing workers remain active, but no new workers will be recruited for that step.

### `template`

Templates, also called workflow templates, provide reusable workflow definitions, which can be uploaded once and executed with different parameter sets. They can be written in the [Python DSL](dsl.md) or as [YAML templates](yaml-templates.md).

#### `template upload`

Uploads a new template file to the server.

```sh
scitq template upload --path <file> [--force]
```

Registers a template script and makes it available for later runs.

Options:
- `--path` (required): path to the local template script (`.py` or `.yaml`).
- `--force`: overwrite an existing template that has the same name/version.

Examples:

```sh
scitq template upload --path qc.py --force
scitq template upload --path biomscope.yaml
```

#### `template run`

Runs a previously uploaded template. You can select it by name+version or by ID. Parameters are passed as comma‑separated `key=value` pairs.

```sh
scitq template run [--id <template_id> | --name <template_name>] [--version <ver>] [--param "k1=v1,k2=v2,..."] [--no-recruiters]
```

Options:
- `--id`: template ID to execute.
- `--name`: template name (alternative to `--id`).
- `--version`: optional version (defaults to latest when omitted with `--name`).
- `--param`: comma‑separated key=value pairs (client converts them to JSON for the server).
- `--no-recruiters`: create the workflow without deploying any workers (useful for testing or manual worker assignment).

Examples:

```sh
scitq template run --name qc_step --version 1.2.0 --param "sample=A12,threads=8"
scitq template run --name biomscope --param "bioproject=PRJEB6070,location=openstack.ovh:GRA11" --no-recruiters
```

#### `template list`

Lists uploaded templates. You can filter by name and/or version.

```sh
scitq template list [--name <pattern>] [--version <ver>] [--latest]
```

- `--name`: filter by template name (supports wildcards like `meta%`).
- `--version`: filter by version; with `--latest`, shows only the most recent version per name.

#### `template detail`

Shows a template’s metadata and parameters (including names, types, defaults, and help text). You can target it by ID or by name+version.

```sh
scitq template detail [--id <template_id> | --name <template_name>] [--version <ver>]
```

#### `template download`

Downloads a template script from the server.

```sh
scitq template download [--id <template_id> | --name <template_name>] [--version <ver>] [-o <output_file>]
```

Options:
- `--id`: template ID to download.
- `--name`: template name (alternative to `--id`).
- `--version`: optional version (defaults to latest).
- `-o` / `--output`: save to file instead of printing to stdout.

Examples:

```sh
scitq template download --name biomscope -o biomscope.yaml
scitq template download --id 42
```

### `module`

Modules are reusable YAML step definitions used by [YAML templates](yaml-templates.md). They live in a server-side **versioned library** shared between bundled modules (shipped with scitq) and user-uploaded modules. Templates reference a module with `import: <path>[@<version>]`. See the [Module Library reference](../reference/module-library.md) for the full model.

#### `module upload`

Uploads a YAML module to the server library. The YAML content must carry a top-level `version:` field.

```sh
scitq module upload --path <file> [--as <namespace/path>] [--force]
```

Options:
- `--path` (required): local path to the module YAML file.
- `--as`: server-side namespace path (e.g. `internal/my_alignment`). Defaults to the filename without extension.
- `--force`: overwrite an existing row at the same `(path, version)`. A `bundled` row overwritten this way becomes `forked`.

Examples:

```sh
# Flat path (derived from filename)
scitq module upload --path modules/biomscope_align.yaml

# Namespaced path
scitq module upload --path modules/biomscope_align.yaml --as internal/biomscope_align

# In-place edit of an existing (path, version)
scitq module upload --path modules/biomscope_align.yaml --as internal/biomscope_align --force
```

#### `module list`

Lists modules in the library. Output includes an origin marker per row (📚 bundled, 👤 local, 🍴 forked).

```sh
scitq module list [--tree] [--versions <path>] [--latest]
```

Options:
- `--tree`: group rows by folder prefix.
- `--versions <path>`: show every version at a single path.
- `--latest`: show only the highest version per path.

Examples:

```sh
scitq module list
scitq module list --tree
scitq module list --versions genomics/fastp
scitq module list --latest
```

#### `module download`

Fetches module content by reference. Prints to stdout unless `-o` is given.

```sh
scitq module download --name <ref> [-o <output_file>]
```

`<ref>` is one of:
- `genomics/fastp` — highest version at this path
- `genomics/fastp@latest` — same, explicit
- `genomics/fastp@1.0.0` — exact version

Examples:

```sh
scitq module download --name internal/biomscope_align
scitq module download --name genomics/fastp@1.0.0 -o /tmp/fastp.yaml
```

#### `module origin`

Prints provenance for a module version.

```sh
scitq module origin <ref>
```

Output shows the origin (`bundled` / `local` / `forked`), content SHA, bundled SHA (on forks), description, uploader, and flags a fork as outdated if a newer `bundled` row has shipped since the fork.

```sh
scitq module origin genomics/fastp
scitq module origin genomics/fastp@1.0.0-site
```

#### `module fork` (admin)

Clones a module row into a new `(path, version)` with `origin=forked`. Typical flow: fork → download → edit → upload `--force`.

```sh
scitq module fork <ref> --new-version <version>
```

Example:

```sh
scitq module fork genomics/fastp@1.0.0 --new-version 1.0.0-site
```

#### `module upgrade` (admin)

Seeds or updates bundled rows in the library from the installed `scitq2_modules` package. Dry-run by default; `--apply` commits.

```sh
scitq module upgrade [--apply]
```

Output diff columns: `<path> <version> <action> <detail>` where action is one of `bundled (new)`, `bundled  up-to-date`, `forked   keep (local edits detected)`, `CONFLICT …`, `skipped (no version)`.

Examples:

```sh
# Review pending changes
scitq module upgrade

# Commit
scitq module upgrade --apply
```


### `file`

File commands let you list or copy files between local paths and remote storages configured via `rclone` in `scitq.yaml`. Arguments are **positional** unless noted.


#### `file list`

Lists files and directories using the client helper with the server’s rclone config.

```sh
scitq file list <src>
```

- `<src>`: source URI or local path to list.

Examples:

```sh
scitq file list aznorth://rnd/test/
scitq file list /data/results/
```

#### `file copy`

Copies data between locations. **Both arguments are positional.**

```sh
scitq file copy <src> <dst>
```

- `<src>`: source URI or local path.
- `<dst>`: destination URI or local path.

Examples:

```sh
scitq file copy /tmp/output/ azure://rnd/results/
scitq file copy azure://rnd/input.fastq.gz s3://results/tmp/input.fastq.gz
```

Notes:
- Supported schemes include:
  - rclone entries in server configuration `scitq.yaml`, followed by `://`, likely `azure://`, `swift://`, `s3://`, 
  - Regular filesystem paths, classical http/https/ftp URLs, 
  - Specific protocols such as `asp://` for Aspera,
  - Specific genetic entries for public databases like `run+fastq://<run accession>` (FASTQ files are DNA sequence files and a run is a sequencing experiment performed with a sequencer)


#### `file remote-list`

Lists files and directories on a remote **from the server side** (useful when the client cannot reach the storage directly). Not commonly used, prefer `list` which is more efficient.

```sh
scitq file remote-list <uri> [--timeout <seconds>]
```

- `<uri>`: remote URI to list (e.g. `aznorth://rnd/project/`).
- `--timeout`: optional, defaults to `30` seconds.

Example:

```sh
scitq file remote-list aznorth://rnd/test/ --timeout 30
```

### run

Operations on **template runs** (executions started via `scitq template run`).

#### run list

Lists previous template runs. You can filter by template ID.

```sh
scitq run list [--template-id <id>]
```

Shows for each run: run ID, template name/version, workflow name, creation time, status, and user.

#### run delete

Deletes a template run by ID.

```sh
scitq run delete --id <run_id>
```


### `user`

User commands manage accounts authorized to use the scitq server. Apart for the user list command, you must have the admin status to use these commands.

#### user list

Lists all registered users.

```sh
scitq user list
```

Shows: user ID, username, email, and whether the user is an admin.

#### user create

Creates a new user account. 

```sh
scitq user create --username <name> --email <email> --password <password> [--admin]
```

Options:
- `--username` (required): login name.
- `--email` (required)
- `--password` (required)
- `--admin`: grant admin rights.

Example:

```sh
scitq user create --username alice --email alice@example.org --password 'S3cure!' --admin
```

#### user update

Updates an existing user.

```sh
scitq user update --id <user_id> [--username <name>] [--email <email>] [--admin | --no-admin]
```

Options:
- `--id` (required): user ID to update.
- `--username`: new login name.
- `--email`: new email.
- `--admin`: grant admin rights.
- `--no-admin`: remove admin rights.

Examples:

```sh
scitq user update --id 12 --username alice2
scitq user update --id 12 --no-admin
```

#### user delete

Deletes a user account by ID.

```sh
scitq user delete --id <user_id>
```

Example:

```sh
scitq user delete --id 12
```

#### user change-password

Interactively changes a user’s password (prompts for current and new password).

```sh
scitq user change-password --username <name>
```

- Prompts for current password, then for the new password twice.
- Uses the server set by `SCITQ_SERVER` (defaults to `localhost:50051`).

### `worker-event`

Worker event commands let you inspect, delete, or prune event logs emitted by workers.  
Events include informational, warning, error, and debug messages related to worker lifecycle and execution.

#### worker-event list

Lists recorded events. You can filter by worker ID, severity level, or class.

```sh
scitq worker-event list [--worker-id <id>] [--level <D|I|W|E>] [--class <name>] [--limit <n>]
```

Options:
- `--worker-id`: show only events from this worker.
- `--level`: filter by severity level (`D`: Debug, `I`: Info, `W`: Warning, `E`: Error).
- `--class`: restrict to a specific event class (e.g. `phase`, `runtime`, `trace`, `task`, `diagnostics`, etc.).
- `--limit`: maximum number of events to show (default: 20).

Example:

```sh
scitq worker-event list --worker-id 12 --level E
```

#### worker-event delete

Deletes a worker event by ID.

```sh
scitq worker-event delete --id <event_id>
```

Use this to remove obsolete or duplicate logs.

Example:

```sh
scitq worker-event delete --id 42
```

#### worker-event prune

Deletes multiple worker events matching filters or older than a given age.

```sh
scitq worker-event prune --older-than <age> [--level <D|I|W|E>] [--class <name>] [--worker-id <id>] --dry-run]
```

Options:
- `--older-than`: duration before now (e.g. `7d`, `12h`, `30m`) — required unless `--dry-run` is used.
- `--level`: optional severity filter.
- `--class`: optional class filter.
- `--worker-id`: restrict pruning to a specific worker.
- `--dry-run`: count matching events without deleting them.

Examples:

```sh
scitq worker-event prune --older-than 7d
scitq worker-event prune --older-than 24h --level W --class recruitment --dry-run
```


### `hashpassword`

Hashes a password using bcrypt, suitable for inclusion in configuration files.

```sh
scitq hashpassword <password>
```

This produces a secure bcrypt hash that can be used, for example, to set the admin password in `scitq.yaml`.

Example:

```sh
scitq hashpassword MySecret123
```

Output:

```
$2a$10$Ff...ZqNQvJfFzJ7rO9I1KOdwb.x7y7Kz7JZkX8ZyF4jO
```

### `config`

Configuration-related utilities for importing or generating configuration fragments.

#### `config import-rclone`

Imports an existing `rclone.conf` file and prints a corresponding YAML fragment suitable for inclusion in `scitq.yaml`.

```sh
scitq config import-rclone [<path>]
```

- `<path>`: optional path to an existing `rclone.conf` (defaults to `~/.config/rclone/rclone.conf`).

Example:

```sh
scitq config import-rclone
```

The command will print a YAML fragment that can be copied at the end of your `scitq.yaml` configuration.