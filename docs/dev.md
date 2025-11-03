# Dev

## Code Structure
- **server/**: Handles gRPC requests and task management.
- **client/**: Implements worker logic.
- **cli/**: Provides command-line management tool.
- **fetch/**: Contain rclone integration part.
- **cmd/**: Provides binaries for server, client, cli
- **gen/**: Go gRPC generated stubs
- **internal/**: Misc shared code for version display
- **lib/**: Shared low level connexion system used in server/client/cli.
- **proto/**: gRPC proto file
- **python/**: DSL Python package
- **sample_files/**: Example production configuration and service definition
- **tests/**: Integration tests
- **utils/**: Misc go snippets used in several places

- **docs/**: Documentation
- **tools/**: Misc codes used for docs.

## Task State Transitions
- **I** → Inactive
- **W** → Waiting
- **P** → Pending
- **A** → Assigned
- **C** → Accepted
- **D** → Downloading
- **O** → On hold
- **R** → Running
- **U**/**V** → Uploading (when succeeded/failed)
- **S** → Succeeded
- **F** → Failed
- **X** → Canceled
- **Z** → Suspended

## Running Tests
```sh
make integration-test
```
Or force non utilisation of cache:
```sh
make fresh-integration-test
```

## Updating proto
When the .proto file is changed, you must re-generate the stubs for Go, Svelte.js and Python:

```sh
make proto-all
```

## Updating python doc

You'll need to definde a python venv for that but the code will be installed and updated in it. 
I use a venv folder within the python subfolder. 

```sh
make dsl-doc VENV=./python/venv
```

