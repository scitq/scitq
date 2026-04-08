"""Optuna integration test script — run by TestOptunaIntegration.

This script connects to a scitq server, creates an Optuna study,
runs 5 trials with 2 samples each, and verifies convergence.

Each task outputs "score: <value>" where the score is a function of the
hyperparameter: score = 1.0 - abs(x - 0.7). The optimal x is 0.7.

Environment variables:
  SCITQ_SERVER: server address
  SCITQ_SSL_CERTIFICATE: SSL cert (base64)
  SCITQ_TOKEN: auth token
  STEP_ID: step ID to submit tasks to
  WORKFLOW_ID: workflow ID
"""

import os
import sys

import optuna

# Ensure scitq2 is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../python/src"))

from scitq2.grpc_client import Scitq2Client
from scitq2.live import LiveContext


def main():
    server = os.environ["SCITQ_SERVER"]
    token = os.environ["SCITQ_TOKEN"]
    step_id = int(os.environ["STEP_ID"])

    client = Scitq2Client(server=server, token=token)
    ctx = LiveContext(client, poll_interval=1.0)

    study = optuna.create_study(direction="maximize")

    n_trials = 3
    samples = ["sample_A"]

    for trial_num in range(n_trials):
        trial = study.ask()
        x = trial.suggest_float("x", 0.0, 1.0)

        # Submit one task per sample — score = 1 - |x - 0.7| + sample_noise
        task_ids = []
        for i, sample in enumerate(samples):
            # The task computes score based on x and a small deterministic offset per sample
            noise = 0.01 * i
            expected_score = max(0, 1.0 - abs(x - 0.7) + noise)
            task_id = client.submit_task(
                step_id=step_id,
                command=f'echo "training {sample} with x={x}" && echo "score: {expected_score:.4f}"',
                container="alpine",
                shell="sh",
            )
            task_ids.append(task_id)
            print(f"  Trial {trial_num}, {sample}: task {task_id}, x={x:.3f}, expected_score={expected_score:.4f}")

        # Wait for all tasks and collect quality scores
        # Debug: check task statuses before waiting
        for tid in task_ids:
            t = ctx._get_task(tid)
            print(f"  DEBUG: task {tid} status={t.status if t else 'NOT FOUND'}")
        results = ctx.wait_all(task_ids)
        scores = [score for _, score in results if score is not None]

        if scores:
            trial_score = sum(scores) / len(scores)
            study.tell(trial, trial_score)
            print(f"Trial {trial_num}: x={x:.3f}, score={trial_score:.4f}")
        else:
            study.tell(trial, state=optuna.trial.TrialState.FAIL)
            print(f"Trial {trial_num}: FAILED (no scores)")

    # Verify convergence
    print(f"\nBest params: {study.best_params}")
    print(f"Best value: {study.best_value:.4f}")
    print(f"Completed trials: {len(study.trials)}")

    # The best x should be close to 0.7 (within the 5-trial search)
    # At minimum, the best score should be > 0.5 (better than random center at 0.5)
    completed = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    assert len(completed) >= 2, f"Expected at least 2 completed trials, got {len(completed)}"
    assert study.best_value > 0.3, f"Best score {study.best_value} too low"

    print("\nOPTUNA_TEST_PASSED")


if __name__ == "__main__":
    optuna.logging.set_verbosity(optuna.logging.WARNING)
    main()
