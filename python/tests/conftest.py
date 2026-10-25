import itertools

import pytest


@pytest.fixture(autouse=True)
def _never_reach_a_real_server(monkeypatch):
    """Tests run against fakes, never against a real scitq server. A developer
    shell commonly carries SCITQ_SERVER / SCITQ_TOKEN for a production server;
    without this, any test building a real client would act on it."""
    monkeypatch.setenv("SCITQ_SERVER", "127.0.0.1:1")
    monkeypatch.setenv("SCITQ_TOKEN", "test-token")


class _FakeServerClient:
    """In-memory stand-in for Scitq2Client: create/submit calls return fresh
    ids, list calls return nothing, every other call is a no-op."""

    def __init__(self, *args, **kwargs):
        self._ids = itertools.count(1)
        self.calls = []

    def __getattr__(self, name):
        def call(*args, **kwargs):
            self.calls.append(name)
            if name.startswith(("create_", "submit_", "register_")):
                return next(self._ids)
            if name.startswith("list_"):
                return []
            return None
        return call


@pytest.fixture
def fake_server(monkeypatch):
    """Run YAML templates (including dry runs) without a server. The YAML
    runner imports Scitq2Client inside its functions, so the class is
    replaced where it is defined."""
    from scitq2 import grpc_client
    monkeypatch.setattr(grpc_client, "Scitq2Client", _FakeServerClient)
