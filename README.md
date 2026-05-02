# README.md


## scitq

[![Go Version](https://img.shields.io/github/go-mod/go-version/scitq/scitq)](https://golang.org/)
[![Build](https://github.com/scitq/scitq/actions/workflows/build.yml/badge.svg)](https://github.com/scitq/scitq/actions/workflows/build.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-beta-yellow)]()
<!-- [![Docs](https://img.shields.io/badge/docs-Read%20the%20Docs-blue)](https://scitq.readthedocs.io/) -->


A distributed task queue for scientific and cloud workloads, built in Go with PostgreSQL and gRPC.

### Quick Start

This example demonstrates how to quickly set up and run a minimal instance locally.

```sh
make install

docker run -d \
  --name scitq-db \
  -e POSTGRES_USER=scitq \
  -e POSTGRES_PASSWORD=scitq \
  -e POSTGRES_DB=scitq \
  -p 5432:5432 \
  postgres:16

scitq-server &
scitq-client &
export SCITQ_TOKEN="quickstart" 
scitq worker list
scitq task create --command "ls -la" --container ubuntu --shell bash
scitq task list
scitq task output --id 1
```

### Architecture
The scitq architecture includes a Go backend, declarative YAML workflow templates (with a Python DSL as a lower-level alternative), and a Svelte.js UI for monitoring.

- **Server**: Manages tasks, workers, and logs (PostgreSQL-backed).
- **Client**: Executes tasks.
- **CLI**: Provides manual interactions using command line.
- **UI**: Provides manual interactions using a web interface (in Svelte.js).
- **YAML templates**: Declarative pipeline definition — the recommended way to write workflows. More abstract than the Python DSL, less code to write, fewer foot-guns (no mutable state, no Python execution model to reason about), and naturally readable both by humans and by LLMs (every step, parameter, dependency, and resource is explicit and self-documenting). Uploadable to the server, runnable from the CLI or UI, with parameter forms generated automatically from the template's `params:` block. See [`docs/usage/yaml-templates.md`](docs/usage/yaml-templates.md).
- **DSL**: Python library for workflows that need imperative control flow (loops over computed sets, runtime branching, programmatic step generation). The escape hatch when YAML's declarative model isn't enough. See [`docs/usage/dsl.md`](docs/usage/dsl.md).

### Status
scitq is currently in **beta**. It is feature-complete but under active refinement and testing.

### Documentation
Refer to [`docs/index.md`](docs/index.md) for documentation.

### Contributing
For development details, see [`docs/dev.md`](docs/dev.md).
