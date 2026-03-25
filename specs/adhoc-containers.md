# Ad-hoc Containers — Auto-built Docker Images from YAML

## Goal

Allow YAML pipelines to specify tools by package name instead of Docker image. The system automatically generates a preparation task that builds the Docker image on each worker, cached by the md5 of the install instruction.

## User experience

The user writes:

```yaml
steps:
  - name: count_reads
    conda: bioconda::samtools=1.21
    command: samtools view -c /input/*.bam > /output/count.txt
    inputs: align.bam
```

The YAML runner expands this into two tasks:

1. A **preparation task** (one per worker) that builds the Docker image
2. The **actual task** that uses the built image

The user never writes Dockerfiles. The image is built once per worker and reused for all subsequent tasks.

## How it works

### Step 1: Hash the install instruction

```python
import hashlib
spec = "conda:bioconda::samtools=1.21"
tag = hashlib.md5(spec.encode()).hexdigest()[:12]
image_name = f"scitq_adhoc_{tag}"
# e.g. scitq_adhoc_a1b2c3d4e5f6
```

The same install instruction always produces the same image name. If two workflows both need `samtools=1.21`, they share the same image.

### Step 2: Generate the preparation task

The preparation task runs on the worker with Docker socket access. It checks if the image already exists before building:

```sh
docker image inspect scitq_adhoc_a1b2c3d4e5f6 >/dev/null 2>&1 || \
docker build -t scitq_adhoc_a1b2c3d4e5f6 -f - <<'DOCKERFILE'
FROM gmtscience/mamba
RUN _conda install bioconda::samtools=1.21
DOCKERFILE
```

The `docker image inspect || docker build` pattern means:
- First run on a worker: builds the image (takes a few minutes)
- Subsequent runs on the same worker: image exists, build is skipped (takes <1 second)
- Different worker: builds its own copy (each worker has its own Docker daemon)

### Step 3: The actual task uses the built image

```python
workflow.Step(
    name="count_reads",
    container="scitq_adhoc_a1b2c3d4e5f6",
    command=fr"samtools view -c /input/*.bam > /output/count.txt",
    depends=prepare_step,  # waits for image to be built
    ...
)
```

## Install methods

### conda

```yaml
- name: my_step
  conda: bioconda::samtools=1.21
  command: samtools view -c /input/*.bam
```

Generates:
```dockerfile
FROM gmtscience/mamba
RUN _conda install bioconda::samtools=1.21
```

Uses mkdocker's `_conda` command for clean Conda installs.

Multiple packages:
```yaml
  conda: bioconda::samtools=1.21 bioconda::bedtools=2.31.0
```

### apt

```yaml
- name: my_step
  apt: jq curl
  command: jq '.data' /input/*.json > /output/result.json
```

Generates:
```dockerfile
FROM ubuntu:latest
RUN apt-get update && apt-get install -y jq curl && rm -rf /var/lib/apt/lists/*
```

### binary

```yaml
- name: my_step
  binary: https://github.com/me/tool/releases/download/v1.0/tool-linux-amd64
  command: tool --input /input/* --output /output/result.txt
```

Generates:
```dockerfile
FROM alpine
RUN wget -O /usr/local/bin/tool https://github.com/me/tool/releases/download/v1.0/tool-linux-amd64 && chmod +x /usr/local/bin/tool
```

### pip

```yaml
- name: my_step
  pip: pandas matplotlib
  language: python
  command: |
    import pandas as pd
    df = pd.read_csv('/input/data.csv')
    df.describe().to_csv('/output/summary.csv')
```

Generates:
```dockerfile
FROM python:3.12-slim
RUN pip install pandas matplotlib
```

## Preparation task details

### Container

The preparation task itself runs in `docker:latest` (or `docker:cli`) — a minimal image that contains the Docker CLI. It accesses the host's Docker daemon via the socket.

### Container options

The preparation task needs:
```
container_options: "-v /var/run/docker.sock:/var/run/docker.sock"
```

This is safe because:
- Workers are trusted (they're our own machines)
- The socket mount only gives Docker build access, not host filesystem access
- This is the standard Docker-in-Docker pattern used by CI systems

### Concurrency

The preparation task should run with `concurrency=1` and `prefetch=0` on each worker. It's lightweight (just a Docker build) but must complete before dependent tasks start.

### One per step, not per sample

The preparation task is **per-step, per-worker**. If a step has 100 tasks across 5 workers, there are 5 preparation tasks (one per worker), not 100.

Implementation: the YAML runner creates one preparation step (with `per_sample: false`) that all per-sample tasks in the actual step depend on.

Actually, even simpler: since the `docker image inspect || docker build` pattern is idempotent, we can run the preparation task once globally (not per worker). Each worker assigned a task from this step will execute it — but the Docker build is a no-op if the image already exists. The overhead of the "no-op" prep task is negligible (<1s).

### Caching across workflows

Because the image name is the md5 of the install instruction:
- Workflow A uses `conda: bioconda::samtools=1.21` → builds `scitq_adhoc_a1b2c3d4e5f6`
- Workflow B uses `conda: bioconda::samtools=1.21` → same hash → image already exists → no build

Workers accumulate a cache of ad-hoc images. Cleanup is the user's responsibility (or a periodic Docker prune).

## YAML runner implementation

When the YAML runner encounters `conda:`, `apt:`, `binary:`, or `pip:` on a step:

```python
def _resolve_adhoc_container(step_def: dict, workflow, registry: str) -> tuple[str, Step]:
    """Generate a preparation step for an ad-hoc container.
    Returns (image_name, preparation_step)."""

    # Determine install spec and Dockerfile content
    if 'conda' in step_def:
        spec = f"conda:{step_def['conda']}"
        dockerfile = f"FROM {registry}/mamba\nRUN _conda install {step_def['conda']}"
    elif 'apt' in step_def:
        spec = f"apt:{step_def['apt']}"
        dockerfile = f"FROM ubuntu:latest\nRUN apt-get update && apt-get install -y {step_def['apt']} && rm -rf /var/lib/apt/lists/*"
    elif 'binary' in step_def:
        spec = f"binary:{step_def['binary']}"
        name = step_def['binary'].rsplit('/', 1)[-1]
        dockerfile = f"FROM alpine\nRUN wget -O /usr/local/bin/{name} {step_def['binary']} && chmod +x /usr/local/bin/{name}"
    elif 'pip' in step_def:
        spec = f"pip:{step_def['pip']}"
        dockerfile = f"FROM python:3.12-slim\nRUN pip install {step_def['pip']}"

    # Deterministic image name
    tag = hashlib.md5(spec.encode()).hexdigest()[:12]
    image_name = f"scitq_adhoc_{tag}"

    # Build command (idempotent)
    build_cmd = f"""docker image inspect {image_name} >/dev/null 2>&1 || \\
docker build -t {image_name} -f - <<'DOCKERFILE'
{dockerfile}
DOCKERFILE"""

    # Create preparation step
    prep_step = workflow.Step(
        name=f"_prepare_{image_name}",
        container="docker:cli",
        container_options="-v /var/run/docker.sock:/var/run/docker.sock",
        command=build_cmd,
        task_spec=TaskSpec(concurrency=1, prefetch=0),
    )

    return image_name, prep_step
```

The actual task then:
```python
workflow.Step(
    name=step_def['name'],
    container=image_name,
    command=step_def['command'],
    depends=prep_step,
    ...
)
```

## Security considerations

- Docker socket access is restricted to preparation tasks only (not user tasks)
- The base images (`gmtscience/mamba`, `ubuntu`, `alpine`, `python`) are controlled
- The install instructions come from the YAML template, which is uploaded by trusted users
- Worker-local images are isolated to each worker's Docker daemon

## Cleanup

Ad-hoc images accumulate on workers. Options:

- **Manual**: `docker image prune` or `docker rmi scitq_adhoc_*`
- **Automated**: the watchdog could prune unused `scitq_adhoc_*` images older than N days
- **On worker destruction**: automatically cleaned up (worker is deleted)

For automatically deployed workers (from providers), this is a non-issue — workers are temporary.

For permanent workers, a periodic cleanup may be needed. Not in scope for initial implementation.

## Example: complete YAML with ad-hoc container

```yaml
name: mixed-pipeline
version: 1.0.0
description: Pipeline mixing standard modules with ad-hoc tools

params:
  input_dir:
    type: string
    required: true
  location:
    type: provider_region
    required: true

samples:
  source: uri
  uri: "{params.input_dir}"
  group_by: folder
  filter: "*.f*q.gz"

worker_pool:
  cpu: ">= 8"
  mem: ">= 30"
  max_recruited: 5

steps:
  # Standard module (pre-built container)
  - module: fastp

  # Ad-hoc conda tool (built on first use)
  - name: classify
    conda: bioconda::kraken2=2.1.3
    command: |
      kraken2 --db /resource/kraken2_db \
          --threads ${THREADS} \
          --report /output/${SAMPLE}.report \
          /input/*.fastq.gz
    inputs: fastp.fastqs

  # Ad-hoc apt tool
  - name: summarize
    apt: jq
    command: jq -s 'add' /input/*.report > /output/summary.json
    inputs: classify.report
    grouped: true
```

The YAML runner generates 5 steps from this: fastp (module), _prepare_kraken2 (build image), classify (use image), _prepare_jq (build image), summarize (use image).
