# Basic Usage with the CLI

## Login

scitq uses user authentication so you must login (the admin user and password is set in the server configuration, `scitq.yaml`, once logged as admin, see below, you can create other users). 

To manage your authentication it is required to have a token in memory set in a shell variable, `SCITQ_TOKEN`, so the recommended way to do that is to type this each time you restart a session:

```sh
SCITQ_TOKEN=$(scitq login)
```

Once you have the token, you can set it in a shell environment script (like `.profile`), but if your privacy matters to you, this is not recommended, prefer refreshing the token in memory each time.

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

## CLI logic

The CLI use the following logic scheme:

```sh
scitq <object> <action> --option1 ... --option2 ...
```

You can always type `scitq --help` or `scitq <object> --help` or `scitq <object> <action> -help` to have details on any CLI command.

### `task`

#### `create`

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

As you can see `--output` is a folder (it needs not to exist before creating the task), everything that is written to the `/output/` folder will be copied to this location at the end of the task (whether it succeeds or not). `--input` is a file, you can have several of them, and specify a folder as `--input` is possible, and it means all the (recursive) content of the folder. Globing patterns such as `s3://bucket/data/*.dat` are also possible as an input pattern (but not fancy patterns such as curly brace ones, the only special char admitted is `*`). `--output` does not have this flexibility, it is necessarily a folder.

As you can see `--resource` and `--input` are quite similar, both are files, can be specified several times and offer some flexibility by allowing also folders and globing patterns. They differ in two ways:
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

A task may depends on other tasks, this is declared by the `--dependency <task id>` flag which can be repeated. A task created with dependencies will wait (see waiting status below) until all the tasks it depends upon succeed.

#### `list`

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
| **W** | waiting            | Task is not ready to be launched (depends on another task not yet succeeded).                |
| **P** | pending            | Task is ready to be launched.                                                               |
| **A** | assigned           | Task has been assigned to a worker (worker not yet aware).                                  |
| **C** | accepted           | Worker has acknowledged and accepted the task.                                             |
| **D** | downloading        | Task is preparing by downloading inputs, resources, and containers.                         |
| **O** | on hold            | Task is ready but the worker has no available capacity.                                     |
| **R** | running            | Task is currently executing.                                                                |
| **U** | uploading          | Task ran successfully and is uploading output data.                                         |
| **V** | uploading (failed) | Task failed, uploading output or logs.                                                     |
| **S** | succeeded          | Task completed successfully and upload finished.                                           |
| **F** | failed             | Task failed during execution or upload.                                                    |
| **Z** | suspended          | Task is paused and can resume later.                                                       |
| **X** | canceled           | Task was canceled before running.                                                          |
| **I** | inactive           | Task is defined but waiting for the workflow to be finalized before becoming pending.      |

- `--worker-id` filter tasks associated to this worker (minimal status `A`)
- `--step-id` filter tasks related to this step (see step below)
- `--workflow-id` filter tasks related to this workflow (see workflow below)
- `--command` filter tasks containing this substring in their command (use of % is possible as a widecar for globbing)
- `--show-hidden` display hidden tasks.

About hidden tasks: A task transmitted to a worker cannot be changed. Thus if the task fails to run for any reason (like if the worker disappear, or if the command simply failed), it won't be modified to remember what happened. It can be retried, though, either automatically in workflows or manually when created with the CLI. When retried, the old task is hidden and a new one is created. The reason for that is that in most cases, what matters is the latest temptative of the task, especially if the outcome is different (e.g. it eventually succeeds). So by default, previous attempts are hidden and not displayed. Showing hidden tasks, however, permits to see previous failure to understand what happened. 

#### `retry`

As shown just above a failed task can be retried: e.g. the previous (failed) task is hidden and a new pending task is created:

```sh
scitq task retry --id <task id>
```

The action optionnaly permits to place an auto-retry on task, so to retry the task and let retry three more times if needed:

```sh
scitq task retry --id <task id> --retry 3
```

#### `output`

This action enables to see the lines printed by the task (stdout/stderr).

```sh
scitq task output --id <task id>
```

### `flavor`

`flavor` is the term coined by Openstack to describe a type of instance, scitq kept it.

#### `list`

`list` is the only available action for `flavor` objects. It lists instance types or server models. It takes two option:
- `--limit` default to 10, list the cheapest flavors matching the filtering criteria (see below),
- `--filter` apply a filter on listed flavors.

Filters are a column `:` separated list of simple requirements of the form:
- `cpu>32` : flavor with strictly more than 32 vcpu,
- `mem>=10` : flavor with 10Gb or more memory (e.g. RAM, not disks, see below),
- `disk<1000` : flavor with less than 1000Gb disks.

So for instance:
```sh
scitq flavor list --filter 'cpu>32:mem>=10:disk<1000'
```

Filters can contain other criteria:
- `provider~%azure%` : the provider name must contain azure (providers are always in small caps),
- `region~%swed%` : the region must contain swed (swedencentral is a great Azure region),
- `eviction<50` : the eviction(*) stats (not always reliable) must be below 50% (which is very high). If you say nothing, by default a filter `eviction<=5` is applied - which is a sound default (and Azure minimal stat) since a high eviction rate will make workers very inefficients.
- `gpumem>=10` : the instance has more than 10Gb of GPU memory.
- `gpu~%Tesla%` : the description of the GPU contains Tesla.

(*) eviction is the Azure expression for reclaiming an instance (only spot instances are reclaimed, but they are very cheap and used by default in scitq). A reclaimed instance is killed. You can revive it but it is generally not efficient so the current strategy is to delete it and redeploy.

NB: if a `scitq flavor list` without filter gives you an empty list, it's likely that the providers updates are not properly configured in `scitq.yaml`, see [configuration](../reference/configuration.md).

### `worker`

Workers are the compute units that actually execute tasks. They can be deployed automatically by the recruiter engine or manually through the CLI.  
Each worker is associated with a **provider** (e.g. Azure, OpenStack), an optional **region**, and optionally a **step** it serves in a workflow.  

#### `list`

Lists all workers known to the scheduler.

```sh
scitq worker list
```

Displays for each worker its ID, name, status, concurrency, prefetch value, IP addresses, flavor, provider, and region.

#### `deploy`

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

#### `delete`

Deletes a worker by its ID.

```sh
scitq worker delete --worker-id <id>
```

The command sends a deletion order to the provider.  
Use with care, since ongoing tasks will be interrupted.

#### `stats`

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

#### `list`

Lists all workflows currently registered on the server.

```sh
scitq workflow list
```

Displays for each workflow its ID, name, run strategy, and maximum allowed workers (if any).

#### `create`

Creates a new workflow.

```sh
scitq workflow create --name <workflow_name> [--run-strategy <strategy>] [--maximum-workers <n>]
```

Options:

- `--name` (required): human-readable name of the workflow.  
- `--run-strategy`: controls how tasks are distributed between workers.  
  - `B`: **Batch-wise** (default) — workers focus on completing one step before moving to the next.  
  - `T`: **Thread-wise** — workers advance tasks as far as possible across steps.  
  - `D`: **Debug** mode.  
  - `Z`: **Suspended** — workflow is created but not yet launched.  
- `--maximum-workers`: caps the total number of workers the workflow can use.

Example:

```sh
scitq workflow create --name myworkflow --run-strategy B --maximum-workers 50
```

#### `delete`

Deletes a workflow by ID.

```sh
scitq workflow delete --id <workflow_id>
```

The workflow and its associated steps and tasks will be removed.  
Use with care, as this cannot be undone.

### `step`

A step is a subdivision of a workflow that groups tasks requiring similar execution environments (same container, resources, or logic).  
Steps generally represent the order: tasks in step *N* usually depend on successful completion of step *N−1*. But dependancies are implemented at task level, so the real logic may be very different from that simple rule.

#### `list`

Lists all steps belonging to a given workflow.

```sh
scitq step list --workflow-id <workflow_id>
```

Displays for each step its ID and name.

#### `create`

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

#### `delete`

Deletes a step by ID.

```sh
scitq step delete --id <step_id>
```

Removes the step and its task associations.

#### `stats`

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

#### `list`

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

#### `create`

Creates a new recruiter.

```sh
scitq recruiter create --step-id <id> --filter <expr> [--rank <n>] [--concurrency <n>] [--prefetch <n>] [--prefetch-percent <n>] [--cpu-per-task <n>] [--memory-per-task <gb>] [--disk-per-task <gb>] [--concurrency-min <n>] [--concurrency-max <n>] [--max-workers <n>] [--rounds <n>] [--timeout <s>]
```

- `--filter`: selection rule for compatible flavors (e.g. `"cpu>=8:mem>=16:region~%central%"`) (see flavor list above for a complete description)

Options allow both **fixed concurrency** and **adaptive concurrency** modes:
**Fixed concurrency** means any recruited worker will have the same concurrency and prefetch will have the same number of concurrent task excutions allowed:
- `--concurrency`: fixed number of concurrent tasks per worker
- `--prefetch`: task prefetching configuration

**Adaptative concurrency** means the number of concurrent task executions allowed depends on the worker capacity and the task requirement (if the cpu is limitating, then cpu requirement dictate the concurrency, if the memory is limitating it's the memory, etc.): 
- `--cpu-per-task`, `--memory-per-task`, `--disk-per-task`: adaptive concurrency resource scaling
- `--concurrency-min`, `--concurrency-max`: adaptive concurrency bounds
- `--prefetch-percent`: express prefetch as a percentage of concurrency, thus 50 means when the adaptative concurrency sets to 10, prefetch sets to 5.

- `--max-workers`: upper limit of workers managed by this recruiter
- `--rounds`: number of recruitment rounds per cycle
- `--timeout`: seconds between recruitment cycles

Examples:

```sh
scitq recruiter create --step-id 14 --filter 'cpu>=8:mem>=30' --concurrency 4 --prefetch 1 --max-workers 50
```

```sh
scitq recruiter create --step-id 14 --filter 'cpu>=8:mem>=30' --cpu-per-task 2 --prefetch-percent 50 --max-workers 50
```


#### `delete`

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

Templates, also called workflow templates, provide reusable workflow definitions, which can be uploaded once and executed with different parameter sets. They use the [python DSL language](dsl.md).

#### `upload`

Uploads a new template file to the server.

```sh
scitq template upload --path <file> [--force]
```

Registers a template script and makes it available for later runs.

Options:
- `--path` (required): path to the local template script (e.g. `qc.py`).
- `--force`: overwrite an existing template that has the same name/version (if applicable).

Example:

```sh
scitq template upload --path qc.py --force
```

#### `run`

Runs a previously uploaded template. You can select it by name+version or by ID. Parameters are passed as comma‑separated `key=value` pairs.

```sh
scitq template run [--id <template_id> | --name <template_name>] [--version <ver>] [--param "k1=v1,k2=v2,..."]
```

Options:
- `--id`: template ID to execute.
- `--name`: template name (alternative to `--id`).
- `--version`: optional version (defaults to latest when omitted with `--name`).
- `--param`: comma‑separated key=value pairs (client converts them to JSON for the server).

Examples:

```sh
scitq template run --name qc_step --version 1.2.0 --param "sample=A12,threads=8"
scitq template run --id 42 --param "input_uri=s3://bucket/x.fastq.gz"
```

#### `list`

Lists uploaded templates. You can filter by name and/or version.

```sh
scitq template list [--name <pattern>] [--version <ver>] [--latest]
```

- `--name`: filter by template name (supports wildcards like `meta%`).
- `--version`: filter by version; with `--latest`, shows only the most recent version per name.

#### `detail`

Shows a template’s metadata and parameters (including names, types, defaults, and help text). You can target it by ID or by name+version.

```sh
scitq template detail [--id <template_id> | --name <template_name>] [--version <ver>]
```


### `file`

File commands let you list or copy files between local paths and remote storages configured via `rclone` in `scitq.yaml`. Arguments are **positional** unless noted.


#### `list`

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

#### `copy`

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


#### `remote-list`

Lists files and directories on a remote **from the server side** (useful when client cannot reach the storage directly). Not commonly used, prefer `list` which is more efficient.

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

#### `list`

Lists previous template runs. You can filter by template ID.

```sh
scitq run list [--template-id <id>]
```

Shows for each run: run ID, template name/version, workflow name, creation time, status, and user.

#### `delete`

Deletes a template run by ID.

```sh
scitq run delete --id <run_id>
```


### `user`

User commands manage accounts authorized to use the scitq server. Appart for the user list command, you must have the admin status to use these commands.

#### `list`

Lists all registered users.

```sh
scitq user list
```

Shows: user ID, username, email, and whether the user is an admin.

#### `create`

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

#### `update`

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

#### `delete`

Deletes a user account by ID.

```sh
scitq user delete --id <user_id>
```

Example:

```sh
scitq user delete --id 12
```

#### `change-password`

Interactively changes a user password (prompts for current and new password).

```sh
scitq user change-password --username <name>
```

- Prompts for current password, then for the new password twice.
- Uses the server set by `SCITQ_SERVER` (defaults to `localhost:50051`).