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

If you need to pass a binary command of your own, you must have a storage setup in your `scitq.yaml` `rclone` section. All [rclone supported storage](https://rclone.org/overview/) are supported by scitq. Say you have an AWS S3 storage defined as `s3`.

So first you need to copy the binary to the storage:
```sh
scitq file copy mybinary s3://bucket/resource/mybinary
```

Then you can specify the task to use this binary as a resource:
```sh
scitq task create --resource s3://bucket/resource/mybinary --container alpine --command "/resource/mybinary"
```

Which means you can basically execute anything.




If your task has no step, only a worker with no steop can handle it (this is the default situation). If your task has a step then only a worker belonging to this step can handle it. To affect the task to a specific step, this is done by specifying `--step-id` when creating the task, which means you need to create a step before, see below.
