# Welcome to scitq

**scitq** is a distributed task queue written in Go, with:
- a SvelteKit web interface
- YAML templates and a Python DSL for workflow orchestration
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
- [Writing workflows with YAML templates](usage/yaml-templates.md) (recommended)
- [Writing workflows with the Python DSL](usage/dsl.md)
- [Understanding scitq model (db schema and memory representation of the workload)](model.md)

- [Converting from Nextflow](usage/convert-nextflow.md)
- [Converting from Snakemake](usage/convert-snakemake.md)

- [Troubleshooting](troubleshooting.md)

## For developers
- [Development](dev.md)