"""Smoke tests for the `run_strategy` workflow constructor and YAML
plumbing. The actual sticky/thread *scheduling* behavior is server-side
and not covered here — these tests only check the wire path from YAML →
Workflow → gRPC request.
"""
import pytest

from scitq2.workflow import Workflow, _normalise_run_strategy


def test_normalise_accepts_letter_codes():
    assert _normalise_run_strategy("B") == "B"
    assert _normalise_run_strategy("T") == "T"
    assert _normalise_run_strategy("D") == "D"
    assert _normalise_run_strategy("Z") == "Z"


def test_normalise_accepts_friendly_words():
    assert _normalise_run_strategy("batch") == "B"
    assert _normalise_run_strategy("thread") == "T"
    assert _normalise_run_strategy("debug") == "D"
    assert _normalise_run_strategy("suspended") == "Z"


def test_normalise_is_case_insensitive_and_stripped():
    assert _normalise_run_strategy("  Thread  ") == "T"
    assert _normalise_run_strategy("BATCH") == "B"


def test_normalise_none_passes_through():
    assert _normalise_run_strategy(None) is None


def test_normalise_unknown_raises():
    with pytest.raises(ValueError):
        _normalise_run_strategy("sticky")


def test_workflow_constructor_stores_normalised_strategy():
    wf = Workflow(name="t", version="1.0.0", run_strategy="thread")
    assert wf.run_strategy == "T"


def test_workflow_constructor_default_is_none():
    wf = Workflow(name="t", version="1.0.0")
    assert wf.run_strategy is None
