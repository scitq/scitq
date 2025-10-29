# Basic Usage with the CLI

## Login

scitq uses user authentication so you must login (the admin user and password is set in the server configuration, `scitq.yaml`, once logged as admin, see below, you can create other users). 

To manage your authentication it is required to have a token in memory set in a shell variable, `SCITQ_TOKEN`, so the recommanded way to do that is to type this each time you restart a session:

```sh
SCITQ_TOKEN=$(scitq login)
```

Once you have the token, you can set it in a shell environment script (like `.profile`), but if your privacy matters to you, this is not recommanded, prefer refreshing the token in memory each time.

NB: if you forget to login, the CLI will recall that to you.

If this does not work, and you get a message like this:
```
2025/10/29 17:58:22 login failed: rpc error: code = Unavailable desc = connection error: desc = "transport: Error while dialing: dial tcp [::1]:50051: connect: connection refused" (xxxxx)
```

It means that the server is not running localy. In that case you need either to start the server... (the CLI requires a running server), or to tell the CLI where the server resides (the CLI believe it to be local by default), which is done by setting in your environment the address of the server:

```sh
SCITQ_SERVER=<server_fqdn>:<port>
```

(both items are configuration entries in `scitq.yaml`).

This can safely be added in your shell environment script (and maybe at server level in `/etc/profile` since it is not specific to a user).

If you continue to have connection errors while this is correctly set and the server is running, it's likely a network issue (firewall, routing). Check with a specific software like nmap: `nmap <server_fqdn> -p<port>` (which will tell you if the port is open or not).

If your password or login is wrong you'll get this message.
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

Create a new task in scitq. This task will be handled by the first worker that can be affected to it. Task and Worker are assigned to small work groups called steps. Only workers belonging to your task step will be able to handle it. To keep things simple, we will first stay in the default situation where there are no steps for either tasks or workers.

The minimal command to create a task is:

```sh
scitq task create --container <docker container name> --command '<a shell command to run (executed in docker entry context)>'
```

So for instance, this is the hello world command:
```sh
scitq task create --container alpine --command 'echo "hello world"'
```

Here the command is very simple so it can be executed directly, if your command need some piping or shell specificity, you'll need to tell scitq to execute it in a shell:
```sh
scitq task create --container alpine --shell sh --command 'echo "hello world"|tr "h" "H"'
```

You can also pass some python code thus:
```sh
scitq task create --container python:3.12 --shell python --command "print('Hello world')
print('Python rules!')"
```

##### resources

If you need to pass a binary command of your own, you must have a storage setup in your `scitq.yaml` `rclone` section. All [rclone supported storage](https://rclone.org/overview/) are supported by scitq. Say you have an AWS S3 storage defined as `s3`.

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

This is not the only folder that is mounted in the docker container, there are also the `/input` and `/output` folders, which usage are used to respectively provide some input data to the task and retrive output data from the task. Say `mybinary` takes an input file with `-i` and produce an output file specified by `-o`. Let's pretend our input data is a file available on our `s3://bucket`: `s3://bucket/data/input1.dat`.

```sh
scitq task create --resource s3://bucket/resource/mybinary --input s3://bucket/data/input1.dat --output s3://bucket/results/mybinary-input1/ --container alpine --command "/resource/mybinary -i /input/input1.dat -o /output/output.dat"
```

As you can see `--output` is a folder (it needs not to exist before creating the task), everything that is written to the `/output/` folder will be copied to this location at the end of the task (whether it succeeds or not). `--input` is a file, you can have several of them, and specify a folder as `--input` is possible, and it means all the (recursive) content of the folder. Globing pattern such as `s3://bucket/data/*.dat` are also possible as input pattern (but not fancy patterns such as curly brace ones, the only special char admitted is `*`). `--output` does not have this flexibility, it is necessarily a folder.

As you can see `--resource` and `--input` are quite similar, both are files, can be specified several times and offer some flexibility by allowing also folders and globing patterns. They differ in two ways:
- first they are made available to tasks through two different folders,
- second, resource are shared between tasks, while input are unique. For this reason `/resource` is a read-only folder contrarily to `/input`. For a binary that is not modified during execution, it is best brought to the task as resource. It is the same for a reference data. Processed data are typically inputs and not resources.

##### URI action

The specification of input, resource or output uses a specific syntax that is called a URI for Uniform Resource Identifyer. It ressemble the internet URL except it is more general since unusual protocols such as `s3://` or `azure://` or `gcp://` can be accepted (provided an rclone resource named s3, azure or gcp exists in `scitq.yaml` rclone section). Note that rclone uses a slightly different notation, without the `//` juste after the `:`. 

An URI action is an extension of a resource or an input URI that allow a transformation of the data before being mounted into the task. It is mostly used for resource since as being read only they cannot be modified by the task. If a task use a complexe resource that is made of several files, it is most convenient to distribute it as a TAR archive that is decompressed. This is done simply by adding `|untar` to the resource specification. If the resource is a collection of files present in `reference_data.tgz`, one can write:

```sh
scitq task create --resource s3://bucket/resource/mybinary --resource 's3://bucket/resource/reference_data.tgz|untar' --input s3://bucket/data/input1.dat --output s3://bucket/results/mybinary-input1/ --container alpine --command "/resource/mybinary -i /input/input1.dat -o /output/output.dat"
```

Docker container are better when slim, which is why large reference data files are better distributed out of the container, and scit resource system is intended for that, with or without action.

There are three possible actions, `|untar`, `|gunzip` and `|mv:...`:
- `untar` dearchive TAR archive (compressed or not, .tgz or tar.bz2 archives, or even .zip files are handled gracefully). Contrarily to `tar xf ...`, the untar action consumes the archive which is destroyed and replace by its content at the end of the action,
- `gunzip` decompress a gzipped file.  Like the `gunzip` command the `untar` action, it consumes the compressed file which is destroyed and replace by its content,
- `mv:...` is an action that move the downloaded file or folder to a subfolder within `/input` or `/resource`. Technically it is not downloaded then moved, it is directly downloaded to the final destination (thus it would not overwrite a file with the same name at the root of `/input` or `/resource`). The final destination does not need to exist before, if it is missing it is created. Relative destination like `..` are forbiden.

Note: while mainly used for resource, URI action also works for inputs. They are only forbiden for output.

#### `list`

List action exist for almost all objects and list this kind of object.

```sh
scitq task list
```

Typically list all the tasks. The different options of this action are filters :
- `--limit` limit the number of displayed tasks which default to 20. The tasks are displayed most recent task first, so by default `scitq task list` displays the last 20 tasks created.
- `--offset` is a pagination option and allow to display the number of tasks defined by `--limit` but starting from this position in the list,
- `--status <status>` display only the tasks with this status.

Each status is defined by a letter, and there are 4 primary statuses that are very important in scitq, the (P)ending, (R)unning and (S)ucceeded or (F)ailed status. However, there is a lot of subtlety in the way tasks are handled in scitq, so here are all the status more ore less in their progression order:

| Letter |  Status  |       Description                                                             |
|--------|----------|-------------------------------------------------------------------------------|
|   W    | waiting  | The task is not ready to be launched (it depends on another task not succeeded yet) |
| **P**  | **pending**  | The task is ready to be launched                                              |
|   A    | assigned | The task has been assigned to a worker (but the worker does not know it yet)  |
|   C    | accepted | The task has been accepted by the worker it was assigned to                   |
|   D    | downloading | The task is preparing, it downloads inputs, resources, and containers      |
|   O    | on hold  | The task is ready to be run but the worker does not have the bandwidth to run it right now |
| **R**  | **running**  | The task is running                                                           |
|   U    | uploading | Thet task has run successfully and the content of `/output` is copied to the output URI folder |
|   V    | uploading on failure | Same as above but the task has failed                             |
| **S**  | **succeeded** | The task is successful with its upload finished                              |
| **F**  | **failed**   | The task has failed at any previous step                                      |
|   Z    | suspended | The task is paused, execution is suspended and could resume                  |
|   X    | canceled | For some reason the task was rejected by the worker before it ran.            |
|   I    | inactive | This task is not to be run until some process move it to another status.      |


- `--worker-id` filter tasks associated to this worker (minimal status `A`)
- `--step-id` filter tasks related to this step (see step below)
- `--workflow-id` filter tasks related to this workflow (see workflow below)
- `--command` filter tasks containing this substring in their command (use of % is possible as a widecar for globbing)
- `--show-hidden` display hidden tasks.

About hidden tasks: A task transmitted to a worker cannot be changed. Thus if the task fails to run for any reason (like if the worker disappear, or if the command simply failed), it won't be modified to remember what happened. It can be retried though, either automatically in workflows or manually when created with the CLI. When retried, the old task is hidden and a new one is created. The reason for that is that in most cases, what matters is the latest temptative of the task, especially if the outcome is different (e.g. it eventually succeeds). So by default, previous attempts are hidden and not displayed. Showing hidden tasks however permits to see previous failure to understand what happened. 





If your task has no step, only a worker with no steop can handle it (this is the default situation). If your task has a step then only a worker belonging to this step can handle it. To affect the task to a specific step, this is done by specifying `--step-id` when creating the task, which means you need to create a step before, see below.
