"""
Report aggregation with MultiQC.

Based on nf-core/modules (multiqc) — MIT License.
Copyright (c) The nf-core team. See https://github.com/nf-core/modules
"""
from scitq2 import Outputs, TaskSpec


def multiqc(workflow, *, inputs, worker_pool=None,
            container="ewels/multiqc:latest"):
    """Aggregate QC reports with MultiQC.

    This is typically a fan-in step (after the sample loop) that collects
    all QC outputs and generates a single report.

    Args:
        workflow: the Workflow object
        inputs: grouped inputs from a previous step (e.g. fastp.output("json", grouped=True))
        worker_pool: optional WorkerPool override
        container: Docker image

    Returns:
        Step with outputs: report="multiqc_report.html"
    """
    return workflow.Step(
        name="multiqc",
        container=container,
        command=fr"""
        cd /input
        multiqc . -o /output/
        """,
        inputs=inputs,
        outputs=Outputs(report="multiqc_report.html"),
        task_spec=TaskSpec(cpu=2, mem=4),
        worker_pool=worker_pool,
    )
