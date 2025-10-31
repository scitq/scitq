# Welcome to scitq

**scitq** is a distributed task queue written in Go, with:
- a SvelteKit web interface
- a Python DSL for workflow orchestration
- a gRPC + HTTP API
- a comprehensive CLI

scitq focuses on:
- **End-to-end solidity** — reliable task and worker management without reliance on heavy frameworks.  
- **Minimal abstraction** — a thin, explicit layer above each task for predictable behavior.  
- **Live adaptability** — sub-second feedback on task output and minute-scale responsiveness to resource adjustments.

For an overview of design philosophy, see [Motivation](motivation.md).

## Quick links
- [Installation guide](install.md)
- [Basic usage](usage/basic.md)
- [CLI reference](usage/cli.md)
- [Using the UI](usage/ui.md)
- [Writing real world workflows (Python DSL usage)](usage/dsl.md)
- [Understanding scitq model (db schema and memory representation of the workload)](model.md)

- [Troubleshooting](troubleshooting.md)

## For developers
- [Development](dev.md)