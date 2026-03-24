"""
scitq2_modules — Reusable step definitions for scitq workflows.

Convention: each module exports a function with this signature:

    def tool_name(workflow, sample, *, inputs=None, worker_pool=None, **kwargs) -> Step

Where:
- workflow: the Workflow object
- sample: the current sample (has .sample_accession, .fastqs, etc.)
- inputs: input from previous step (default: sample.fastqs for first step)
- worker_pool: optional override (default: workflow's pool)
- Returns: the Step object (for chaining with .output())
"""
