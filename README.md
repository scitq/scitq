# README.md

## scitq

A lightweight and efficient distributed task queue with gRPC and PostgreSQL.

### Quick Start
```sh
make install
#scitq-server --config=config.yaml &
scitq-server &
scitq-client --concurrency 1 & 
scitq-cli worker list
scitq-cli task create --command "ls -la" --container ubuntu --shell bash
scitq-cli task list
scitq-cli task output --id 1
```



### Architecture
- **Server**: Manages tasks, workers, and logs (PostgreSQL-backed).
- **Clients**:
  - **Python producer**: Submits tasks.
  - **Go worker client**: Executes tasks.
  - **CLI**: Provides manual interaction.

### API
Refer to [`docs/api.md`](docs/api.md) for gRPC API documentation.

### Contributing
For development details, see [`docs/dev.md`](docs/dev.md).
