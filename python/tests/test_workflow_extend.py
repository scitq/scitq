"""Unit tests for the declarative "extend an existing workflow" reconcile
(see specs/workflow_extend.md).

The reconcile logic lives entirely in the DSL `Workflow.compile` /
`Step.compile` / `Task.compile` orchestration of client RPCs, so it is exercised
here against a fake in-memory client that records calls and serves the
"existing workflow" state on the second (extend) pass. No server needed.

Covered:
- (A) new task (tag) added to an existing step
- (B) new step added to an existing workflow
- (C) task edition on command drift, with cascade (default)
- (C) task edition restricted to failed tasks, no cascade (retry_failed_only)
"""
from types import SimpleNamespace

from scitq2.workflow import Workflow


class FakeClient:
    """Minimal in-memory stand-in for Scitq2Client.

    Models workflows/steps/tasks and records the calls compile makes so tests
    can assert *what* was created / referenced / edited.
    """

    def __init__(self):
        self.workflows = {}   # id -> name
        self.steps = {}       # id -> (workflow_id, name)
        self.tasks = {}       # id -> {step_id, task_name, command, status, hidden}
        self._next = 0
        self.calls = []       # ("op", *args)

    def _id(self):
        self._next += 1
        return self._next

    # --- methods compile() may call -------------------------------------
    def get_workspace_root(self, provider, region):  # pragma: no cover - unused (provider=None)
        return None

    def create_workflow(self, name, **kw):
        i = self._id()
        self.workflows[i] = name
        self.calls.append(("create_workflow", name))
        return i

    def create_step(self, workflow_id, name, quality_definition=None):
        i = self._id()
        self.steps[i] = (workflow_id, name)
        self.calls.append(("create_step", name))
        return i

    def create_recruiter(self, **kw):  # pragma: no cover - no worker_pool in tests
        self.calls.append(("create_recruiter", kw.get("step_id")))

    def update_template_run(self, **kw):  # pragma: no cover
        pass

    def update_workflow_status(self, **kw):  # pragma: no cover
        self.calls.append(("update_workflow_status", kw.get("status")))

    def submit_task(self, *, step_id, command, task_name=None, status="P", **kw):
        i = self._id()
        self.tasks[i] = dict(step_id=step_id, task_name=task_name, command=command,
                             status=status, hidden=False)
        self.calls.append(("submit_task", task_name, command))
        return i

    def edit_and_retry_task(self, task_id, command):
        old = self.tasks[task_id]
        old["hidden"] = True
        i = self._id()
        self.tasks[i] = dict(step_id=old["step_id"], task_name=old["task_name"],
                             command=command, status="P", hidden=False)
        self.calls.append(("edit_and_retry", old["task_name"], command))
        return i

    # --- queries the extend path uses -----------------------------------
    def list_workflows(self, **kw):
        return [SimpleNamespace(workflow_id=i, name=n) for i, n in self.workflows.items()]

    def list_steps(self, workflow_id):
        return [SimpleNamespace(step_id=sid, workflow_id=wf, name=nm,
                                workflow_name=self.workflows.get(wf))
                for sid, (wf, nm) in self.steps.items() if wf == workflow_id]

    def list_tasks(self, workflow_id=None, show_hidden=False, status=None):
        out = []
        for tid, t in self.tasks.items():
            if t["hidden"] and not show_hidden:
                continue
            wf, _ = self.steps[t["step_id"]]
            if workflow_id is not None and wf != workflow_id:
                continue
            out.append(SimpleNamespace(task_id=tid, step_id=t["step_id"],
                                       task_name=t["task_name"], command=t["command"],
                                       status=t["status"]))
        return out

    # --- test helpers ----------------------------------------------------
    def set_status(self, task_name, status):
        for t in self.tasks.values():
            if t["task_name"] == task_name and not t["hidden"]:
                t["status"] = status

    def ops(self, op):
        return [c for c in self.calls if c[0] == op]

    def reset_calls(self):
        self.calls = []


def _wf():
    # provider=None -> no workspace lookup; container default avoids per-step container.
    return Workflow(name="wf", version="1.0.0", container="img")


def test_extend_adds_new_task_to_existing_step():
    """(A) qc.S1 already exists -> referenced; qc.S2 is new -> submitted; step reused."""
    c = FakeClient()

    wf1 = _wf()
    wf1.Step(name="qc", command="run S1", tag="S1")
    wf1.compile(c)
    wid = wf1.workflow_id

    c.reset_calls()
    wf2 = _wf()
    wf2.Step(name="qc", command="run S1", tag="S1")  # unchanged, exists
    wf2.Step(name="qc", command="run S2", tag="S2")  # new tag
    wf2.compile(c, extend_workflow_id=wid)

    assert c.ops("create_step") == [], "existing step must be reused, not recreated"
    submitted = {name for _, name, _ in c.ops("submit_task")}
    assert submitted == {"qc.S2"}, "only the new tag is submitted"
    assert c.ops("edit_and_retry") == [], "unchanged existing task must not be edited"


def test_extend_adds_new_step_to_existing_workflow():
    """(B) a brand-new step is created; the existing step is reused."""
    c = FakeClient()

    wf1 = _wf()
    wf1.Step(name="qc", command="run S1", tag="S1")
    wf1.compile(c)
    wid = wf1.workflow_id
    c.set_status("qc.S1", "S")

    c.reset_calls()
    wf2 = _wf()
    qc = wf2.Step(name="qc", command="run S1", tag="S1")          # exists, unchanged
    wf2.Step(name="align", command="align S1", tag="S1", inputs=qc.output())  # new step
    wf2.compile(c, extend_workflow_id=wid)

    created_steps = {name for _, name in c.ops("create_step")}
    assert created_steps == {"align"}, "only the new step is created"
    submitted = {name for _, name, _ in c.ops("submit_task")}
    assert submitted == {"align.S1"}, "only the new step's task is submitted"
    assert c.ops("edit_and_retry") == [], "unchanged qc.S1 must be referenced, not edited"


def test_extend_command_drift_cascades_to_dependents():
    """(C default) qc.S1 command drifts -> edit-and-retry; align.S1 command is
    unchanged but its prerequisite changed -> cascade -> also edit-and-retried."""
    c = FakeClient()

    wf1 = _wf()
    qc1 = wf1.Step(name="qc", command="run v1", tag="S1")
    wf1.Step(name="align", command="align v1", tag="S1", inputs=qc1.output())
    wf1.compile(c)
    wid = wf1.workflow_id
    c.set_status("qc.S1", "S")
    c.set_status("align.S1", "S")

    c.reset_calls()
    wf2 = _wf()
    qc2 = wf2.Step(name="qc", command="run v2", tag="S1")              # command CHANGED
    wf2.Step(name="align", command="align v1", tag="S1", inputs=qc2.output())  # command SAME
    wf2.compile(c, extend_workflow_id=wid)

    edited = {name for _, name, _ in c.ops("edit_and_retry")}
    assert "qc.S1" in edited, "drifted command must be edit-and-retried"
    assert "align.S1" in edited, "dependent must cascade-retry even with unchanged command"
    assert c.ops("submit_task") == [], "nothing new to submit"


def test_extend_no_drift_no_action():
    """(C default) identical re-run of a converged workflow touches nothing."""
    c = FakeClient()

    wf1 = _wf()
    qc1 = wf1.Step(name="qc", command="run v1", tag="S1")
    wf1.Step(name="align", command="align v1", tag="S1", inputs=qc1.output())
    wf1.compile(c)
    wid = wf1.workflow_id
    c.set_status("qc.S1", "S")
    c.set_status("align.S1", "S")

    c.reset_calls()
    wf2 = _wf()
    qc2 = wf2.Step(name="qc", command="run v1", tag="S1")
    wf2.Step(name="align", command="align v1", tag="S1", inputs=qc2.output())
    wf2.compile(c, extend_workflow_id=wid)

    assert c.ops("submit_task") == []
    assert c.ops("edit_and_retry") == []
    assert c.ops("create_step") == []


def test_extend_retry_failed_only_no_cascade():
    """(C retry_failed_only) only the failed task is re-run; a succeeded task with
    drifted command is left untouched, and a healthy dependent is NOT cascaded."""
    c = FakeClient()

    wf1 = _wf()
    qc1 = wf1.Step(name="qc", command="run v1", tag="S1")
    wf1.Step(name="qc", command="run v1", tag="S2")
    wf1.Step(name="align", command="align v1", tag="S1", inputs=qc1.output())
    wf1.compile(c)
    wid = wf1.workflow_id
    c.set_status("qc.S1", "F")   # failed
    c.set_status("qc.S2", "S")   # succeeded (healthy)
    c.set_status("align.S1", "S")

    c.reset_calls()
    wf2 = _wf()
    qc2 = wf2.Step(name="qc", command="run v2", tag="S1")   # failed + drifted
    wf2.Step(name="qc", command="run v2", tag="S2")          # succeeded + drifted command
    wf2.Step(name="align", command="align v1", tag="S1", inputs=qc2.output())  # depends on re-run qc.S1
    wf2.compile(c, extend_workflow_id=wid, retry_failed_only=True)

    edited = {name for _, name, _ in c.ops("edit_and_retry")}
    assert edited == {"qc.S1"}, (
        "only the failed task is re-run; the succeeded (drifted) task and the "
        "healthy dependent are left untouched (no cascade)"
    )
    assert c.ops("submit_task") == [], "no new tags"
