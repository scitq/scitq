# Quality scoring and optimization

## Motivation

Many scientific workflows involve optimization: finding the best hyperparameters, the best model configuration, or the best processing strategy. Today, scitq executes tasks and reports success/failure, but has no notion of *how well* a task performed. Users must manually inspect stdout, extract metrics, and decide what to try next.

This spec introduces three layered features:

1. **Quality scoring**: extract numeric metrics from task output and compute a quality score
2. **Live DSL mode**: keep the DSL running to observe results and submit new tasks dynamically
3. **Optuna integration**: use quality scores to drive hyperparameter optimization with parallel trials, fan-out evaluation, and early pruning

Each layer builds on the previous. Quality scoring is useful on its own (monitoring, filtering). Live mode enables any reactive workflow. Optuna integration is the primary use case that motivates both.

## 1. Quality scoring

### Concept

A **quality variable** is a named float extracted from a task's stdout or stderr via a regex with one capture group. A **quality formula** combines one or more quality variables into a single **quality score** (float, higher is better).

Quality variables are defined at the **step level** — all tasks in a step share the same extraction rules.

### Data model

```sql
-- Step-level quality definition
ALTER TABLE step ADD COLUMN quality_definition JSONB NULL;
-- Example: {"variables": {"accuracy": {"pattern": "accuracy: ([0-9.]+)", "source": "stdout"}},
--           "formula": "accuracy"}

-- Task-level results
ALTER TABLE task ADD COLUMN quality_score FLOAT NULL;
ALTER TABLE task ADD COLUMN quality_vars JSONB NULL;
-- Example: {"accuracy": 0.847}
```

### Extraction

Quality variables are extracted from task logs. There are two modes:

1. **Final extraction** (on task success): the server runs the regexes against the stored stdout/stderr and captures the **last match** (since iterative programs output metrics repeatedly). The quality formula is evaluated and `quality_score` is set.

2. **Live extraction** (during execution): the server monitors the streaming stdout/stderr and updates `quality_vars` and `quality_score` as new matches arrive. This enables pruning (see section 3). Live extraction uses the same regexes as final extraction — the score at any point is computed from the latest captured values.

### Formula syntax

The formula is a simple arithmetic expression over quality variable names:

- `"accuracy"` — single variable, score = accuracy
- `"accuracy - loss"` — difference
- `"(precision + recall) / 2"` — F1-like
- `"1 - error_rate"` — inversion

Supported operators: `+`, `-`, `*`, `/`, parentheses, float literals. No function calls (keep it simple, extend later if needed).

### DSL integration

```python
step = workflow.Step(
    name="train",
    quality=Quality(
        variables={
            "accuracy": r"accuracy: ([0-9.]+)",
            "loss": r"final loss: ([0-9.]+)",
        },
        formula="accuracy",
    ),
    ...
)
```

### YAML integration

```yaml
steps:
  - name: train
    quality:
      variables:
        accuracy: "accuracy: ([0-9.]+)"
        loss: "final loss: ([0-9.]+)"
      score: "accuracy"
```

### API

- `ListTasks` response includes `quality_score` and `quality_vars` when present
- New filter: `task list --min-quality 0.8` to filter by score
- MCP: `list_tasks` returns quality fields

## 2. Live DSL mode

### Concept

Today the DSL is a **blueprint**: it defines the full task graph, submits everything, and exits. Live mode keeps the DSL process **running** as a continuous workflow administration script that can:

- Wait for tasks to complete and read their quality scores
- Submit new tasks based on results
- Kill tasks based on intermediate quality (pruning)

### Execution model

The live DSL process runs **externally** (on the user's machine or a dev server, not managed by the scitq server). This simplifies resilience: if the process crashes, the user restarts it. Already-submitted tasks continue running. For Optuna, the study state is persisted in Optuna's own storage (SQLite/PostgreSQL), so the process can resume where it left off.

YAML-driven optimization runs on the server as a long-lived script process. This is more fragile (server restart kills it) but acceptable for many use cases. The YAML runner can detect an existing study and resume.

### DSL primitives

```python
def optimize(workflow, ctx):
    # ctx is a LiveContext providing wait/observe/kill primitives
    
    step = workflow.Step(name="train", ...)
    
    # Submit a task and get a handle
    task = step.task(command="train --lr 0.01")
    
    # Block until task completes, return quality_score (or None if no quality defined)
    score = ctx.wait(task)
    
    # Wait for all tasks in a step
    results = ctx.wait_all(step)  # list of (task, score) pairs
    
    # Observe intermediate quality without blocking (for pruning)
    current_score = ctx.observe(task)  # returns latest quality_score or None
    
    # Kill a task (graceful SIGTERM)
    ctx.stop(task)
    
    # Kill a task (hard SIGKILL)
    ctx.kill(task)

# live=True keeps the process running and provides LiveContext
run(optimize, live=True)
```

### Implementation

- `ctx.wait(task)`: polls `ListTasks` with the task ID until status is S or F. Returns `quality_score` on success, raises on failure.
- `ctx.wait_all(step)`: waits for all non-hidden tasks in the step to reach terminal status.
- `ctx.observe(task)`: single `ListTasks` call, returns current `quality_score` (which updates live during execution).
- `ctx.stop(task)` / `ctx.kill(task)`: calls `SignalTask` with signal `T` (SIGTERM) or `K` (SIGKILL).

The `run(func, live=True)` call:
1. Compiles and submits the initial workflow (if any static steps exist)
2. Creates a `LiveContext` connected to the server
3. Calls `func(workflow, ctx)`
4. The function runs its own loop (e.g., Optuna trials)
5. On return, the workflow is considered complete

### Runner flags

```sh
python -m scitq2.runner my_optimize.py --live
```

## 3. Optuna integration

### Concept

Optuna is a hyperparameter optimization framework. It suggests parameter values, evaluates them, and uses the results to suggest better values. scitq provides the parallel evaluation infrastructure.

The integration works at two levels:
- **DSL**: full Python control over the optimization loop
- **YAML**: declarative `optimize:` block that generates the loop automatically

### Trial model

A **trial** is one set of hyperparameter values to evaluate. Each trial fans out into multiple tasks (one per sample/split), and the trial's score is the **aggregated quality** across all tasks (mean, median, min — configurable).

```
Trial 1: {lr=0.01, batch=32}
    ├── task: train sample_A → quality: 0.82
    ├── task: train sample_B → quality: 0.79
    └── task: train sample_C → quality: 0.85
    Trial score: mean(0.82, 0.79, 0.85) = 0.82

Trial 2: {lr=0.05, batch=64}
    ├── task: train sample_A → quality: 0.91
    ├── task: train sample_B → quality: 0.88
    └── task: train sample_C → quality: 0.90
    Trial score: mean(0.91, 0.88, 0.90) = 0.897
```

### Parallel trials

Optuna supports parallel evaluation natively via `study.ask()` / `study.tell()`. scitq leverages this by running multiple trials concurrently:

```python
# Ask for N trials at once
trials = [study.ask() for _ in range(n_parallel)]

# Submit all tasks for all trials
for trial in trials:
    params = extract_params(trial)
    submit_fan_out_tasks(params)

# Wait for results, report to Optuna
for trial, tasks in trial_tasks.items():
    results = ctx.wait_all(tasks)
    score = aggregate(results)
    study.tell(trial, score)
```

The degree of parallelism is bounded by available workers and the user's choice. Optuna's algorithms (TPE, CMA-ES) handle parallel suggestions correctly.

### Pruning (early stopping)

Pruning kills unpromising trials mid-execution, saving compute. It uses:

1. **Live quality extraction**: as the task outputs metrics (e.g., per-epoch accuracy), scitq updates `quality_score` in real time
2. **Optuna's pruning API**: `trial.should_prune()` based on intermediate values
3. **Graceful task termination**: `ctx.stop(task)` sends SIGTERM via `docker stop`, allowing the process to clean up

The pruning loop for a single trial:

```python
# Submit fan-out tasks for this trial
tasks = submit_tasks(trial_params)

# Monitor intermediate quality
while not all_done(tasks):
    for task in running(tasks):
        intermediate = ctx.observe(task)
        if intermediate is not None:
            trial.report(intermediate, step=current_step(task))
            if trial.should_prune():
                for t in running(tasks):
                    ctx.stop(t)
                raise optuna.TrialPruned()
    time.sleep(poll_interval)
```

### DSL example

```python
from scitq2 import *
import optuna

def optimize(workflow, ctx):
    study = optuna.create_study(
        direction="maximize",
        storage="sqlite:///optuna_study.db",  # persistent, resumable
        study_name="gpredomics_opt",
        load_if_exists=True,
    )
    
    samples = ["sample_A", "sample_B", "sample_C"]
    n_parallel = 5
    
    for batch_start in range(0, 100, n_parallel):
        trials = [study.ask() for _ in range(min(n_parallel, 100 - batch_start))]
        trial_tasks = {}
        
        for trial in trials:
            lr = trial.suggest_float("lr", 1e-4, 1e-1, log=True)
            depth = trial.suggest_int("depth", 1, 10)
            
            step = workflow.Step(
                name=f"trial_{trial.number}",
                container="gpredomics:latest",
                quality=Quality(
                    variables={"score": r"best_score: ([0-9.]+)"},
                    formula="score",
                ),
            )
            
            tasks = []
            for sample in samples:
                t = step.task(
                    command=fr"gpredomics --lr {lr} --depth {depth} /input/{sample}",
                    input=f"azure://data/{sample}/",
                )
                tasks.append(t)
            trial_tasks[trial] = tasks
        
        # Wait for all tasks in this batch
        for trial, tasks in trial_tasks.items():
            scores = [ctx.wait(t) for t in tasks]
            if None in scores:  # some tasks failed
                study.tell(trial, state=optuna.trial.TrialState.FAIL)
            else:
                study.tell(trial, sum(scores) / len(scores))
    
    print(f"Best params: {study.best_params}")
    print(f"Best score: {study.best_value}")

run(optimize, live=True)
```

### YAML integration

```yaml
name: gpredomics_opt
version: 1.0.0
description: Hyperparameter optimization for gpredomics

params:
  data_dir:
    type: string
    required: true
  location:
    type: provider_region
    required: true
  n_trials:
    type: integer
    default: 100
  n_parallel:
    type: integer
    default: 5

iterate:
  name: sample
  source: uri
  uri: "{params.data_dir}"
  group_by: folder

worker_pool:
  provider: "{params.location}"
  cpu: ">= 8"
  mem: ">= 30"

optimize:
  method: optuna
  direction: maximize
  n_trials: "{params.n_trials}"
  n_parallel: "{params.n_parallel}"
  storage: "sqlite:///optuna_{workflow_name}.db"
  aggregation: mean           # mean, median, min, max
  search_space:
    lr:
      type: float
      low: 0.0001
      high: 0.1
      log: true
    depth:
      type: int
      low: 1
      high: 10
    algo:
      type: categorical
      choices: ["rf", "xgb", "svm"]
  pruning:
    enabled: true
    warmup_steps: 5           # don't prune before this many intermediate reports
    poll_interval: 30         # seconds between quality checks
  step: train                 # which step is the optimization target

steps:
  - name: train
    container: gpredomics:latest
    command: "gpredomics --lr {lr} --depth {depth} --algo {algo} /input/{SAMPLE}"
    quality:
      variables:
        score: "best_score: ([0-9.]+)"
      score: "score"
```

The YAML runner generates the Optuna loop: for each batch of `n_parallel` trials, it substitutes suggested params into the `train` step command template for each sample, submits the fan-out, waits for results, aggregates quality, and reports to Optuna.

## 4. Signal enhancement

### Current state

The `signal` column on task supports `K` (kill). The client runs `docker kill <container>` which sends SIGKILL.

### Change

Add `T` (terminate) signal:

| Signal | Docker command | Effect |
|--------|---------------|--------|
| `K` | `docker kill <container>` | SIGKILL — instant death, no cleanup |
| `T` | `docker stop --time <grace> <container>` | SIGTERM → grace period → SIGKILL |

When PID 1 in the container is a shell (`bash -c "gpredomics ..."`), SIGTERM propagates to the child process group. Programs that handle SIGTERM/SIGHUP (like gpredomics) can clean up and exit gracefully.

The pruning system uses `T` (graceful stop). The grace period (seconds before SIGKILL after SIGTERM) is configurable — important for programs like gpredomics that need time to save state:

```sh
scitq task kill --id 123                # SIGKILL (default, backward compatible)
scitq task stop --id 123                # SIGTERM, 10s grace (default)
scitq task stop --id 123 --grace 60     # SIGTERM, 60s grace
```

### Client-side change

```go
for _, tid := range res.KillTasks {
    go func(tid int32, signal string) {
        name := fmt.Sprintf("scitq-task-%d", tid)
        if signal == "T" {
            exec.Command("docker", "stop", name).Run()
        } else {
            exec.Command("docker", "kill", name).Run()
        }
    }(tid, signal)
}
```

The `TaskListAndOther.kill_tasks` field changes from `repeated int32` to a list of `(task_id, signal)` pairs, or we add a parallel `repeated string kill_signals` field.

## 5. Implementation order

1. **Quality scoring**: step-level definition, final extraction on task success, storage, API exposure. No live extraction yet.
2. **Signal enhancement**: add `T` signal, `docker stop`, CLI `task stop`.
3. **Live quality extraction**: server-side regex on streaming stdout, update `quality_score` in real time.
4. **Live DSL mode**: `run(func, live=True)`, `LiveContext` with `wait`/`observe`/`stop`/`kill`.
5. **Optuna DSL integration**: `Quality` class, trial loop patterns, parallel batch submission.
6. **Optuna YAML integration**: `optimize:` block, YAML runner generates the loop.
7. **Pruning**: intermediate quality monitoring + `ctx.stop()` + Optuna pruning API.

Steps 1-2 are standalone and useful immediately. Steps 3-5 deliver the core optimization capability. Steps 6-7 are refinements.

## Design decisions

1. **Quality is step-level, not task-level.** All tasks in a step produce the same kind of output. The regex and formula are shared. Individual task scores vary but are computed with the same rules.

2. **Fan-out is the default trial model.** A trial evaluates parameters across multiple samples. The trial score aggregates task scores. This matches the bioinformatics use case (evaluate across patient cohorts).

3. **External process for live DSL.** The user runs the optimization script on their machine. The script connects to the server, submits tasks, and polls for results. This avoids server-side process management complexity.

4. **Server-side live scripts survive restarts (opt-in).** When a YAML template (or DSL script) runs server-side with `live: true`, the server spawns it as an independent systemd transient unit via `systemd-run`:

   ```
   systemd-run --unit=scitq-live-{run_id} --remain-after-exit \
     /path/to/venv/bin/python -m scitq2.yaml_runner template.yaml --live ...
   ```

   This gives the script its own process tree, independent of the scitq-server process. If the server restarts, the script keeps running. It detects the gRPC disconnection and reconnects with backoff (since `wait()` and `observe()` are already polling, they naturally tolerate transient disconnects). The script appears as a systemd unit with journal logs and can be monitored/stopped via `systemctl`.

   Without `live: true`, the current behavior is preserved: the script runs as a direct subprocess and dies with the server.

   Optuna's persistent storage (SQLite/PostgreSQL) provides a second layer of resilience: even if the script itself crashes, it can be restarted and will resume from the last completed trial (`load_if_exists=True`).

5. **Optuna as a library, not a service.** Optuna runs inside the DSL process, not as a separate server. Its study storage (SQLite or PostgreSQL) provides persistence. This keeps the architecture simple.

6. **Pruning is graceful.** Uses SIGTERM (`docker stop --time <grace>`) so programs can save state and exit cleanly. The grace period defaults to 10 seconds but is configurable per signal (via `--grace` on CLI, `grace_period` in MCP/gRPC). For programs like gpredomics that need more time, set a higher value (e.g. 60s).
