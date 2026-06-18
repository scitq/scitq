from scitq2.validate import validate_shell
from typing import Dict, List, Optional, Union
from scitq2.grpc_client import Scitq2Client
from scitq2.language import Language, Raw, Shell
from scitq2.recruit import WorkerPool
from scitq2.uri import Resource
from scitq2.constants import DEFAULT_TASK_STATUS, ACTIONS
import json
import os
from itertools import count
import sys
from collections.abc import Iterable
from abc import ABC, abstractmethod


def _normalize_uri(uri: str) -> str:
    """Collapse consecutive slashes in a URI's path portion.

    Preserves the `://` scheme separator and an empty authority
    (`file:///path` stays valid). Only touches the path *after* the
    host. So `s3://rnd/results//x/` → `s3://rnd/results/x/`, but
    `file:///path` is untouched (the leading `/` is the start of the
    path, not a host separator).

    Idempotent: normalize_uri(normalize_uri(x)) == normalize_uri(x).
    """
    if not isinstance(uri, str) or "://" not in uri:
        return uri
    scheme_end = uri.index("://") + 3
    rest = uri[scheme_end:]
    if "/" not in rest:
        return uri
    path_start = rest.index("/")
    host = rest[:path_start]
    path = rest[path_start:]
    while "//" in path:
        path = path.replace("//", "/")
    return uri[:scheme_end] + host + path


class Quality:
    """Defines quality variable extraction and scoring for a Step.

    Variables are regex patterns with one capture group, applied to task stdout/stderr.

    Single-objective (backward compatible):
        Quality(variables={"auc": r"auc: ([0-9.]+)"}, formula="auc")

    Multi-objective:
        Quality(
            variables={"train_auc": r"train_auc: ([0-9.]+)", "test_auc": r"test_auc: ([0-9.]+)"},
            objectives=[
                {"formula": "test_auc", "direction": "maximize"},
                {"formula": "test_auc - train_auc", "direction": "maximize"},
            ],
        )
    """
    def __init__(self, variables: Dict[str, str], formula: str = None,
                 objectives: list = None):
        self.variables = variables
        if objectives and formula:
            raise ValueError("Cannot specify both 'formula' and 'objectives'")
        if not objectives and not formula:
            raise ValueError("Must specify either 'formula' or 'objectives'")
        self.formula = formula
        self.objectives = objectives  # list of {"formula": str, "direction": str}

    def to_json(self) -> str:
        d = {"variables": self.variables}
        if self.objectives:
            d["objectives"] = self.objectives
        else:
            d["formula"] = self.formula
        return json.dumps(d)

class Outputs:
    """Represents the declarative outputs of a Step, which can be used in other Steps.
    Note that the publish attribute is attached to Task so it may vary within a Step,
    but not globs (e.g. named output), which should remain consistent for a given Step.

    publish can be:
    - a full URI string (e.g. "azure://rnd/results/project/") — used as-is
    - True — uses the workflow's publish_root with auto-generated subpath
    - a relative path (no "://") — appended to the workflow's publish_root
    """
    def __init__(self, publish: Optional[Union[str, bool]]=None,
                 publish_mode: Optional[str]=None, **kwargs):
        self.globs: Dict[str, str] = kwargs
        self.publish = publish
        # publish_mode: "move" (default — task uploads to publish *instead of*
        # the workspace) or "copy" (uploads to *both*). Spec:
        # addition_from_nextflow.md B. None defers to the server default
        # ("move"); explicit "move" is also accepted.
        if publish_mode is not None and publish_mode not in ("move", "copy"):
            raise ValueError(f"publish_mode must be 'move' or 'copy', got {publish_mode!r}")
        self.publish_mode = publish_mode

        if publish is not None and publish is not True and not isinstance(publish, str):
            raise ValueError("publish must be a string, True, or None")


class OutputBase(ABC):
    @abstractmethod
    def resolve_path(self) -> Union[str, List[str]]: ...
    @abstractmethod
    def resolve_task_id(self) -> List[int]: ...

    def __add__(self, other: Union["OutputBase", List[str]]) -> "CompositeOutput":
        return CompositeOutput.from_parts(self, other)
    def __radd__(self, other: Union["OutputBase", List[str], int]) -> "CompositeOutput":
        # allow sum([o1, o2], 0) usage
        if other == 0:
            return CompositeOutput.from_parts(self)
        return CompositeOutput.from_parts(other, self)

class CompositeOutput(OutputBase):
    def __init__(self, parts: List[Union[OutputBase, Iterable[str]]]):
        self.parts = self._normalize(parts)

    @classmethod
    def from_parts(cls, *parts: Union[OutputBase, Iterable[str]]):
        return cls(list(parts))

    def _normalize(self, parts):
        norm: List[Union[OutputBase, List[str]]] = []
        for p in parts:
            if isinstance(p, CompositeOutput):
                norm.extend(p.parts)
            elif isinstance(p, OutputBase):
                norm.append(p)
            elif isinstance(p, Iterable) and not isinstance(p, (str, bytes)):
                seq = list(p)
                if all(isinstance(x, str) for x in seq):
                    norm.append(seq)
                else:
                    raise TypeError("CompositeOutput parts iterable must yield only strings")
            else:
                raise TypeError(f"Unsupported part for CompositeOutput: {type(p)}")
        return norm

    def resolve_path(self) -> List[str]:
        paths: List[str] = []
        for p in self.parts:
            if isinstance(p, OutputBase):
                rp = p.resolve_path()
                if isinstance(rp, list):
                    paths.extend([x for x in rp if x is not None])
                elif rp is not None:
                    paths.append(rp)
            else:  # list[str]
                paths.extend(p)
        return paths

    def resolve_task_id(self) -> List[int]:
        ids: List[int] = []
        for p in self.parts:
            if isinstance(p, OutputBase):
                ids.extend(p.resolve_task_id())
        # preserve order, remove dupes
        seen = set()
        unique = []
        for i in ids:
            if i not in seen:
                seen.add(i)
                unique.append(i)
        return unique

class Output(OutputBase):
    """Represents a single output of a task, which can be used in other tasks at runtime."""
    def __init__(self, task: "Task", grouped: bool = False, globs: Optional[str]=None,
                 action: Optional[str] = "", move: Optional[str] = None):
        self.task = task
        self.grouped = grouped
        self.globs = globs
        self.action = ""
        if action:
            if action not in ACTIONS:
                if action.startswith('mv'):
                    raise ValueError(f"Use move attribute and not action='mv:...' in output")
                raise ValueError(f"Unsupported action {action} (supported actions are: {','.join(ACTIONS)}).")
            self.action += f"|{action}"
        if move:
            self.action += f"|mv:{move}"

    def __str__(self):
        try:
            return self.resolve_path()
        except ValueError as e:
            return f"Output({self.task.full_name}, grouped={self.grouped}, globs={self.globs}, publish={self.task.publish}), action={self.action}: {e}" 

    def resolve_path(self) -> Union[str, List[str]]:
        """Resolve the output path: publish path if set, otherwise workspace path."""
        wf = self.task.step.workflow

        def build_path(task: "Task") -> Optional[str]:
            if task.publish is not None:
                return task.publish
            elif wf.workspace_root is not None:
                return f"{wf.workspace_root}/{wf.full_name}/{task.full_name}/" + (self.globs or "") + self.action
            else:
                return None

        if self.grouped:
            return [build_path(task) for task in self.task.step.tasks]
        
        return build_path(self.task)

    def resolve_task_id(self) -> List[int]:
        """Resolve the task ID for this output, if available."""
        if self.grouped:
            ids = [t.task_id for t in self.task.step.tasks]
            if any(tid is None for tid in ids):
                raise ValueError(f"Step {self.task.step.name} has some tasks uncompiled yet")
            return ids
        if self.task.task_id is None:
            raise ValueError(f"Task {self.task.full_name} has not been compiled yet")
        return [self.task.task_id]
    
    def __repr__(self):
        return f"<Output of {self.task.full_name if not self.grouped else ('grouped '+self.task.step.name+' tasks')}{(' '+self.globs) if self.globs else ''}>"

    
class GroupedStep:
    """A Step that groups multiple tasks together, allowing for collective task_id resolution."""
    def __init__(self, step: "Step"):
        self.step = step
        
    def task_ids(self) -> List[int]:
        """Return a list of task IDs for all tasks in this grouped step."""
        for task in self.step.tasks:
            if task.task_id is None:
                raise ValueError(f"Step {self.step.name} has some tasks uncompiled yet")
        return [task.task_id for task in self.step.tasks]
    
    def output(self, name: Optional[str] = None, move: Optional[str] = None, action: Optional[str] = "") -> Output:
        """Create an Output object for this grouped step."""
        return self.step.output(name, move=move, action=action, grouped=True)

class Task:
    def __init__(self, tag: str, command: str, container: str,
                 step: "Step",
                 inputs: Optional[Union[str, OutputBase, List[str], List[OutputBase]]] = None,
                 resources: Optional[List[Resource]] = None,
                 language: Optional[Language] = None,
                 depends: Optional[Union[List["Task"],"GroupedStep"]] = None,
                 publish: Optional[str]=None,
                 publish_mode: Optional[str]=None,
                 skip_if_exists: bool = False,
                 retry: Optional[int]=None,
                 accept_failure: bool = False):
        self.tag = tag
        self.command = command
        self.container = container
        self.step = step  # backref to the Step this task belongs to
        self.full_name = self.step.naming_strategy(self.step.name, self.tag) if self.tag else self.step.name
        self.depends = depends
        self.publish = publish
        # Reference tasks (see Task.as_reference) are synthesised by
        # Step.compile during extend to represent existing tasks that
        # weren't in this run's sample list, so grouped/fan-in outputs
        # enumerate the full set. They skip the submit / edit-and-retry
        # path entirely — the task_id is already known from prefetch.
        self.is_reference = False
        # publish_mode: forwarded to TaskRequest.publish_mode; the server
        # persists it on the task row and the worker uploads to both
        # workspace and publish when "copy". Empty / "move" preserves
        # today's exclusive-or behaviour. Spec: addition_from_nextflow.md B.
        self.publish_mode = publish_mode
        self.skip_if_exists = skip_if_exists
        self.accept_failure = accept_failure
        if inputs is None:
            self.inputs = []
        elif isinstance(inputs, list):
            self.inputs = inputs
        elif isinstance(inputs, (str, OutputBase)):
            self.inputs = [inputs]
        else:
            raise ValueError(f"Invalid type for inputs: {type(inputs)}. Expected str, Output, or list of these.")

        self.resources = resources or []
        self.language = language or Raw()
        self.retry = retry
        self.dependency_task_ids: List[int] = []
        self.task_id: Optional[int] = None
        self.reuse_key: Optional[str] = None

    @classmethod
    def as_reference(cls, *, step: "Step", task_name: str, task_id: int, publish: Optional[str]):
        """Build a reference-only Task representing an existing task that
        is NOT being (re)submitted this pass — used by Step.compile on
        extend to widen step.tasks so grouped/fan-in outputs include
        pre-existing samples. The returned task has its task_id already
        set; its compile() is a no-op.

        Why include failed predecessors unconditionally: see
        memory feedback_no_silent_filtering — quietly dropping anomalies
        from aggregation is the worst kind of "safe". This factory does
        not look at status; if a task is in the prefetched set, it is
        included. Operators who want a failed task out of compile delete
        or hide it deliberately.
        """
        ref = cls.__new__(cls)
        ref.tag = None
        ref.command = ""
        ref.container = ""
        ref.step = step
        # The DB-stored task_name IS the full_name (see how active
        # tasks set it from step.naming_strategy). Preserve it verbatim
        # so build_path produces the same workspace path the original
        # task used.
        ref.full_name = task_name
        ref.depends = None
        ref.publish = publish
        ref.publish_mode = None
        ref.skip_if_exists = False
        ref.accept_failure = False
        ref.inputs = []
        ref.resources = []
        ref.language = Raw()
        ref.retry = None
        ref.dependency_task_ids = []
        ref.task_id = task_id
        ref.reuse_key = None
        ref.is_reference = True
        return ref

    def compile(self, client: Scitq2Client, opportunistic: bool = False, untrusted: Optional[List[str]] = None):
        # Reference tasks were synthesised in Step.compile from the
        # extend prefetch. Their task_id is already bound; everything
        # downstream (build_path, resolve_task_id, dependency wiring)
        # works off self.task_id and self.full_name. Nothing to do.
        if self.is_reference:
            return
        # Resolve command using the language's compile_command method
        resolved_command = self.language.compile_command(self.command)
        resolved_shell = self.language.executable()

        # Runtime shell validation (second line of defense) — only for Shell language
        if isinstance(self.language, Shell):
            issues = validate_shell(resolved_command, shell=resolved_shell, allow_source_kw=True)
            errors = [i for i in issues if i.level == "error"]
            for i in issues:
                # Attach task context to messages
                prefix = "❌" if i.level == "error" else "⚠️"
                where = f"{self.step.workflow.full_name or self.step.workflow.name}:{self.full_name}"
                print(f"{prefix} {where}: {i.msg}{(' — ' + i.suggestion) if i.suggestion else ''}", file=sys.stderr)
            if errors:
                raise ValueError(f"Shell validation failed for task {self.full_name}; fix errors above.")

        # Normalize inputs so we can accept a single OutputBase/str or an iterable of them
        if isinstance(self.inputs, (OutputBase, str)) or isinstance(self.inputs, (bytes, bytearray)):
            input_items = [self.inputs]
        elif isinstance(self.inputs, Iterable):
            input_items = list(self.inputs)
        else:
            # Fallback: treat as a single item
            input_items = [self.inputs]

        # Resolve dependencies. Always infer from inputs (data-flow edges) AND
        # from explicit `depends:` (ordering-only edges) — union, not either-or.
        # Previously this was either-or, which silently dropped the data-flow
        # edges whenever a user (or a requires:-injected step) set `depends:`.
        # That broke reuse-hit input redirection for dependents whose edges
        # weren't recorded in task_dependencies.
        resolved_depends = set()
        if self.inputs:
            for input_item in input_items:
                if isinstance(input_item, OutputBase):
                    for task_id in input_item.resolve_task_id():
                        resolved_depends.add(task_id)
        if self.depends is not None:
            if isinstance(self.depends, Iterable):
                for dep in self.depends:
                    if isinstance(dep, Step):
                        if dep.task_id is None:
                            raise ValueError(f'Task {dep.full_name} is not compiled yet cannot build depends.')
                        else:
                            resolved_depends.add(dep.task_id)
                    elif isinstance(dep, GroupedStep):
                        resolved_depends.update(dep.task_ids())
                    elif isinstance(dep, Task):
                        if dep.task_id is None:
                            raise ValueError(f'Task {dep.full_name} is not compiled yet cannot build depends.')
                        else:
                            resolved_depends.add(dep.task_id)
                    else:
                        raise ValueError(f"Depends for task {self.full_name} contains an invalid item of type {type(dep)}; expected Task, Step or GroupedStep.")  
            elif isinstance(self.depends, GroupedStep):
                resolved_depends.update(self.depends.task_ids())
            else:
                raise ValueError(f"Depends for task {self.full_name} is not a list of Task or a GroupedStep")
        # check that there is no None in the dependencies
        if None in resolved_depends:
            raise ValueError("Task dependencies cannot contain None. Ensure all steps are compiled before compiling tasks.")
        self.dependency_task_ids = sorted(resolved_depends)

        # Resolve inputs to Output objects
        resolved_inputs = []
        for input_item in input_items:
            if isinstance(input_item, OutputBase):
                # If it's an Output-like, resolve its path and drop None entries
                resolved_path = input_item.resolve_path()
                if isinstance(resolved_path, list):
                    resolved_inputs.extend([p for p in resolved_path if p is not None])
                elif resolved_path is not None:
                    resolved_inputs.append(resolved_path)
            elif isinstance(input_item, str):
                # If it's a string, treat it as a file path
                resolved_inputs.append(input_item)
            else:
                raise ValueError(f"Invalid input type: {type(input_item)}. Expected str or Output.")
        
        # Resolve output: always use workspace path for worker upload.
        # Publish path (if set) is sent separately — worker copies there on success only.
        wf = self.step.workflow
        resolved_output = None
        resolved_publish = None
        if wf.workspace_root is not None:
            resolved_output = f"{wf.workspace_root}/{wf.full_name}/{self.full_name}/"
        if self.publish is not None:
            resolved_publish = self.publish
            if resolved_output is None:
                # No workspace — publish path doubles as output (legacy behavior)
                resolved_output = resolved_publish
                resolved_publish = None

        # Resolve resources
        resolved_resources = list(map(str, self.resources))

        # Producer side: compute reuse_key whenever the step is reuse-eligible
        # (i.e. not in `untrusted`). The fingerprint becomes available for
        # storage in task_reuse on success regardless of whether *this*
        # workflow consumes from the cache. Consumer-side opt-in is the
        # workflow-level `opportunistic` flag, propagated as `consume_reuse`
        # below — the server's reuse lookup query filters on it.
        computed_reuse_key = None
        if self.step.name not in (untrusted or []):
            computed_reuse_key = self._compute_reuse_key(
                resolved_command, resolved_shell, resolved_resources, input_items
            )
        self.reuse_key = computed_reuse_key

        status = "W" if resolved_depends else DEFAULT_TASK_STATUS
        scitq_auth = bool(getattr(self.step.task_spec, 'scitq_auth', False)) if self.step.task_spec is not None else False
        numa = getattr(self.step.task_spec, 'numa', None) if self.step.task_spec is not None else None
        # Per-task minimum resources from the step's task_spec curve. The
        # initial submission carries curve[0]; the server-side retry-decision
        # logic shifts these to curve[attempt] on edit_and_retry, so the
        # assignment & recruitment paths see the heavier requirement at
        # retry time. None when the step doesn't declare cpu/mem/disk
        # (e.g. NUMA-bound or concurrency-only specs) — task inherits the
        # step's task_spec defaults.
        ts = self.step.task_spec
        if ts is not None:
            min_cpu, min_mem, min_disk = ts.resources_at_attempt(0)
            cpu_curve = ts.cpu_curve
            mem_curve = ts.mem_curve
            disk_curve = ts.disk_curve
        else:
            min_cpu = min_mem = min_disk = None
            cpu_curve = mem_curve = disk_curve = None

        ext = self.step.workflow._extend
        existing = None
        if ext is not None:
            existing = ext.tasks_by_step.get(self.step.step_id, {}).get(self.full_name)

        if existing is None:
            # New tag (or a normal create-new run): submit a fresh task.
            self.task_id = client.submit_task(
                    step_id=self.step.step_id,
                    command=resolved_command,
                    shell=resolved_shell,
                    container=self.container,
                    depends=resolved_depends,
                    inputs=resolved_inputs,
                    output=resolved_output,
                    publish=resolved_publish,
                    resources=resolved_resources,
                    status=status,
                    task_name=self.full_name,
                    skip_if_exists=self.skip_if_exists,
                    retry=self.retry,
                    accept_failure=self.accept_failure,
                    reuse_key=computed_reuse_key,
                    consume_reuse=opportunistic,
                    scitq_auth=scitq_auth,
                    numa=numa,
                    min_cpu=min_cpu,
                    min_mem=min_mem,
                    min_disk=min_disk,
                    cpu_curve=cpu_curve,
                    mem_curve=mem_curve,
                    disk_curve=disk_curve,
                    publish_mode=self.publish_mode,
                )
            if ext is not None:
                ext.changed.add(self.task_id)
        else:
            # Extend reconcile: a task with this (step, tag) already exists.
            # 5-tuple from _build_extend_context; publish is unused here
            # but kept symmetric with the prefetch shape used by Step.compile
            # when synthesising reference tasks.
            existing_id, existing_cmd, existing_container, existing_status, _existing_publish = existing
            # Inputs / resources / depends overrides for the retry clone.
            # In extend mode these always reflect the current template's
            # view — for fan-in / grouped tasks that means the freshly-
            # added samples are wired into the new clone, not silently
            # dropped because retry copies from the parent (see
            # specs/workflow_extend.md fan-in case). Server-side
            # EditAndRetryTask treats present-empty as "clear", so for
            # tasks with no inputs/resources/depends we still pass empty
            # lists rather than nil — the clone matches the template.
            override_inputs = list(resolved_inputs)
            override_resources = list(resolved_resources)
            override_depends = list(resolved_depends)
            # Container drift: if the template now declares a different
            # image, pass it through so the retry runs with the new one.
            # Without this, a fix that involved BOTH a new command and a
            # new container would ship the command change with the old
            # image and fail the same way as before.
            container_override = self.container if self.container != existing_container else None
            if ext.retry_failed_only:
                # Only re-run failed tasks; leave S/R/P untouched. No cascade —
                # a failed task's dependents are blocked in W and unblock when
                # the retry succeeds.
                if existing_status == "F":
                    self.task_id = client.edit_and_retry_task(
                        existing_id, resolved_command,
                        inputs=override_inputs,
                        resources=override_resources,
                        depends=override_depends,
                        container=container_override,
                    )
                    ext.changed.add(self.task_id)
                else:
                    self.task_id = existing_id  # reference, untouched
            else:
                # Default cascade reconcile: re-run if the command drifted,
                # the container drifted, the task failed, OR any prerequisite
                # was re-run this pass (its inputs changed). edit_and_retry
                # rewires dependents to the new clone server-side, so editing
                # in dependency order keeps the chain consistent.
                dep_changed = any(d in ext.changed for d in resolved_depends)
                if (existing_cmd != resolved_command
                        or container_override is not None
                        or existing_status == "F"
                        or dep_changed):
                    self.task_id = client.edit_and_retry_task(
                        existing_id, resolved_command,
                        inputs=override_inputs,
                        resources=override_resources,
                        depends=override_depends,
                        container=container_override,
                    )
                    ext.changed.add(self.task_id)
                else:
                    self.task_id = existing_id  # converged, reference



    def _compute_reuse_key(self, command, shell, resources, input_items):
        """Compute SHA-256 reuse key from task fingerprint + input identities."""
        import hashlib

        # Task fingerprint: what the task *does*
        fp_parts = [
            f"command:{command}",
            f"shell:{shell or ''}",
            f"container:{self.container}",
            f"container_options:",
        ]
        for r in sorted(resources):
            fp_parts.append(f"resource:{r}")
        fingerprint = hashlib.sha256("\n".join(fp_parts).encode()).hexdigest()

        # Input identities: what the task *processes*
        input_identities = []
        for inp in input_items:
            if isinstance(inp, OutputBase):
                ids = self._extract_input_identities(inp)
                if ids is None:
                    return None  # chain broken
                input_identities.extend(ids)
            elif isinstance(inp, str):
                input_identities.append(inp)

        key_parts = [fingerprint] + sorted(input_identities)
        return hashlib.sha256("\n".join(key_parts).encode()).hexdigest()

    def _extract_input_identities(self, output):
        """Extract input identities from an OutputBase for reuse key computation."""
        if isinstance(output, Output):
            if output.grouped:
                ids = []
                for task in output.task.step.tasks:
                    if task.reuse_key is None:
                        return None
                    ids.append(task.reuse_key)
                return ids
            else:
                if output.task.reuse_key is None:
                    return None
                return [output.task.reuse_key]
        return []


class TaskSpec:
    def __init__(self, *, cpu=None, mem=None, disk=None,
                 concurrency: Optional[int]=None, prefetch: Optional[Union[str,int]]=None,
                 scitq_auth: bool=False, numa: Optional[int]=None):
        # cpu / mem / disk: either a scalar or a non-empty monotonically
        # non-decreasing list ("curve") of per-attempt resource requirements
        # (spec: addition_from_nextflow.md A — Retry with resource escalation).
        # A scalar `mem=40` is equivalent to `mem=[40]`. Each TaskSpec retains
        # *both* the original (possibly scalar) form and the canonical
        # curve form so downstream callers that expect a scalar (numa,
        # legacy recruiter call sites, debug stringifier) keep working.
        cpu_curve = self._normalize_curve('cpu', cpu)
        mem_curve = self._normalize_curve('mem', mem)
        disk_curve = self._normalize_curve('disk', disk)

        # numa: pin each task to N NUMA nodes on its worker (docker
        # --cpuset-cpus / --cpuset-mems). Concurrency and per-task CPU/mem
        # are then derived from the worker's NUMA topology rather than
        # from task_spec.cpu/mem, so setting `numa` together with either
        # is overdetermined and rejected here.
        if numa is not None:
            # Accept int-coercible values (e.g. "4" from a YAML
            # interpolation like `numa: "{NUMA}"`). Reject anything that
            # doesn't cleanly parse as an integer.
            try:
                numa = int(numa)
            except (TypeError, ValueError):
                raise ValueError(f"TaskSpec(numa=...) must be a positive integer; got {numa!r}")
            if numa < 1:
                raise ValueError(f"TaskSpec(numa=...) must be a positive integer; got {numa!r}")
            if cpu_curve is not None or mem_curve is not None:
                raise ValueError("TaskSpec: `numa` is mutually exclusive with `cpu` and `mem` — concurrency and per-task budget are derived from the host NUMA topology")
        if concurrency is None and cpu_curve is None and mem_curve is None and numa is None:
            raise ValueError("TaskSpec must define at least one of concurrency, cpu, mem or numa")
        # Canonical curve form (always a list when set) for the curve-aware
        # consumers (workflow.compile, server-side retry-decision).
        self.cpu_curve = cpu_curve
        self.mem_curve = mem_curve
        self.disk_curve = disk_curve
        # Back-compat scalar attributes. When a curve was passed, these are
        # the first element (curve[0]) — what the recruiter quoted as
        # cpu_per_task historically. Callers that need the worst-case for
        # recruitment use task_spec.max_cpu / .max_mem / .max_disk.
        self.cpu = cpu_curve[0] if cpu_curve else None
        self.mem = mem_curve[0] if mem_curve else None
        self.disk = disk_curve[0] if disk_curve else None
        self.concurrency = concurrency
        self.prefetch = self._parse_prefetch(prefetch)
        # scitq_auth: when True, the worker injects SCITQ_SERVER + SCITQ_TOKEN
        # env vars into the task and bind-mounts the worker's scitq CLI into
        # docker containers. Opt-in per step — lets the task call
        # `scitq file copy` for moves that the native publish mechanism can't
        # express (multiple destinations per file, asymmetric paths).
        self.scitq_auth = bool(scitq_auth)
        self.numa = numa

    @staticmethod
    def _normalize_curve(field: str, value):
        """Accept a scalar or list. Return None for unset, else a list of
        floats. Validates non-empty + monotonically non-decreasing + all > 0."""
        if value is None:
            return None
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            curve = [float(value)]
        elif isinstance(value, str):
            # YAML interpolation can hand us numeric-looking strings.
            try:
                curve = [float(value)]
            except ValueError:
                raise ValueError(f"TaskSpec({field}=...) must be a number or list of numbers; got {value!r}")
        elif isinstance(value, (list, tuple)):
            if not value:
                raise ValueError(f"TaskSpec({field}=[]) curve must be non-empty")
            curve = []
            for x in value:
                try:
                    curve.append(float(x))
                except (TypeError, ValueError):
                    raise ValueError(f"TaskSpec({field}=...) curve element {x!r} is not a number")
        else:
            raise ValueError(f"TaskSpec({field}=...) must be a number or list of numbers; got {type(value).__name__}")
        for v in curve:
            if v <= 0:
                raise ValueError(f"TaskSpec({field}=...) curve must be all positive; got {curve!r}")
        for a, b in zip(curve, curve[1:]):
            if b < a:
                raise ValueError(
                    f"TaskSpec({field}=...) curve must be monotonically non-decreasing "
                    f"(retry should never need less than the prior attempt); got {curve!r}"
                )
        return curve

    @property
    def max_cpu(self):
        """Worst-case CPU across the attempt curve. Used by the recruiter to
        size workers so the heaviest retry still fits."""
        return self.cpu_curve[-1] if self.cpu_curve else None

    @property
    def max_mem(self):
        """Worst-case memory across the attempt curve."""
        return self.mem_curve[-1] if self.mem_curve else None

    @property
    def max_disk(self):
        """Worst-case disk across the attempt curve."""
        return self.disk_curve[-1] if self.disk_curve else None

    def resources_at_attempt(self, attempt: int):
        """Return (cpu, mem, disk) for the given attempt index (0-based).
        Indices beyond the curve length clamp to the last element — once you've
        hit the largest curve value, further retries stay there (the user
        accepts the failure if even the biggest shape can't run it)."""
        def _at(curve, idx):
            if not curve:
                return None
            if idx >= len(curve):
                return curve[-1]
            return curve[idx]
        a = max(0, int(attempt))
        return (_at(self.cpu_curve, a), _at(self.mem_curve, a), _at(self.disk_curve, a))

    def _parse_prefetch(self, p):
        if p is None:
            return 0
        if isinstance(p, str) and p.endswith("%"):
            return float(p.strip("%")) / 100.0
        return float(p)

    def __eq__(self, other):
        return isinstance(other, TaskSpec) and (
            self.cpu_curve, self.mem_curve, self.disk_curve,
            self.concurrency, self.prefetch, self.scitq_auth, self.numa
        ) == (other.cpu_curve, other.mem_curve, other.disk_curve,
              other.concurrency, other.prefetch, other.scitq_auth, other.numa)

    def __str__(self):
        # Show curves in their canonical list form so the per-attempt
        # escalation is visible at a glance; collapse single-element curves
        # to a bare scalar for the common case.
        def _short(c):
            if c is None:
                return None
            return c[0] if len(c) == 1 else c
        return (f"TaskSpec(cpu={_short(self.cpu_curve)}, mem={_short(self.mem_curve)}, "
                f"disk={_short(self.disk_curve)} concurrency={self.concurrency}, "
                f"prefetch={self.prefetch}, scitq_auth={self.scitq_auth}, numa={self.numa})")


def underscore_join(*args: str) -> str:
    """
    Joins multiple strings with underscores, ignoring empty strings.
    """
    return "_".join(filter(None, args))

_RUN_STRATEGY_ALIASES = {
    "batch": "B", "b": "B",
    "thread": "T", "t": "T",
    "debug": "D", "d": "D",
    "suspended": "Z", "z": "Z",
}


def _normalise_run_strategy(value: Optional[str]) -> Optional[str]:
    """Accept either a single-letter code (B/T/D/Z) or a friendly word
    (batch/thread/debug/suspended) and return the single-letter code the
    server expects. None → None (server picks its default, currently 'B')."""
    if value is None:
        return None
    key = value.strip().lower()
    if key in _RUN_STRATEGY_ALIASES:
        return _RUN_STRATEGY_ALIASES[key]
    raise ValueError(
        f"unknown run_strategy {value!r}; expected one of "
        f"{sorted(set(_RUN_STRATEGY_ALIASES.values()) | set(_RUN_STRATEGY_ALIASES.keys()))}"
    )


def dot_join(*args: str) -> str:
    """
    Joins multiple strings with dots, ignoring empty strings.
    """
    return ".".join(filter(None, args))


class Step:
    """A Step hybrid object, encompassing Step specific attributes and one or more Task objects (see grouped() method to access Tasks).
    Steps should be contructed using Workflow.Step() method not Step() constructor directly.
    """

    def __init__(self, name: str, workflow: "Workflow", worker_pool: Optional[WorkerPool] = None, task_spec: Optional[TaskSpec] = None,
                 naming_strategy: callable = dot_join, quality: Optional[Quality] = None):
        self.name = name
        self.tasks: List[Task] = []
        self.worker_pool = worker_pool
        self.task_spec = task_spec
        self.step_id: Optional[int] = None
        self.outputs_globs: Dict[str, str] = {}
        self.workflow = workflow
        self.naming_strategy = naming_strategy
        self.quality = quality

    def add_task(
        self,
        *,
        tag: str,
        command: str,
        container: str,
        outputs: Optional[Outputs] = None,
        inputs: Optional[Union[str, OutputBase, List[str], List[OutputBase]]] = None,
        resources: Optional[Union[Resource, str, List[Resource], List[str]]] = None,
        language: Optional[Language] = None,
        depends: Optional[Union["Step",List["Step"]]] = None,
        skip_if_exists: bool = False,
        retry: Optional[int]=None,
        accept_failure: bool = False,
    ):
        """Complete an existing Step object with a new Task."""
        if outputs:
            if self.outputs_globs and outputs.globs != self.outputs_globs:
                raise ValueError(f"Inconsistent outputs declared in step '{self.name}'")
            self.outputs_globs = outputs.globs

        if isinstance(resources, Resource) or isinstance(resources, str):
            resources_list = [resources]
        else:
            resources_list = list(resources) if resources else []
        # Prepend workflow-level resources
        resources_list = self.workflow.resources + resources_list

        if depends is None:
            resolved_depends = None
        elif isinstance(depends, Step):
            resolved_depends = [depends.task]
        elif isinstance(depends, GroupedStep):
            resolved_depends = [depends.step.task for depends in depends.step.tasks]
        elif isinstance(depends, Iterable):
            resolved_depends = []
            for dep in depends:
                if isinstance(dep, Step):
                    resolved_depends.append(dep.task)
                elif isinstance(dep, GroupedStep):
                    resolved_depends.extend(dep.task_ids())
                else:   
                    raise ValueError(f"""Depends not of the right kind, when a list it should be a list of Step or GroupedStep""")
            resolved_depends = [dep.task for dep in depends]
        else:
            raise ValueError(f"""Depends not of the right kind, should be a Step or list of Step""")

        # Resolve publish path
        raw_publish = outputs.publish if outputs else None
        publish = self._resolve_publish(raw_publish, tag)
        publish_mode = outputs.publish_mode if outputs else None

        task = Task(tag=tag, step=self, command=command, container=container,
                    inputs=inputs, resources=resources_list, language=language,
                    depends=resolved_depends, publish=publish, publish_mode=publish_mode,
                    skip_if_exists=skip_if_exists, retry=retry,
                    accept_failure=accept_failure)
        self.tasks.append(task)

    def _resolve_publish(self, raw_publish, tag: Optional[str]) -> Optional[str]:
        """Resolve publish value using workflow's publish_root if needed."""
        if raw_publish is None:
            return None
        root = self.workflow.publish_root
        if raw_publish is True:
            if root is None:
                raise ValueError(f"Step '{self.name}' uses publish=True but no publish_root is set on the Workflow.")
            parts = [root, self.name]
            if tag:
                parts.append(tag)
            return _normalize_uri("/".join(parts) + "/")
        # String publish: absolute URI (contains "://") is used as-is, otherwise relative to publish_root
        if "://" in raw_publish:
            return _normalize_uri(raw_publish)
        if root is None:
            raise ValueError(f"Step '{self.name}' uses a relative publish path '{raw_publish}' but no publish_root is set on the Workflow.")
        return _normalize_uri(f"{root}/{raw_publish.strip('/')}/")

    def output(self, name: Optional[str] = None, grouped: bool = False, move: Optional[str] = None, action: Optional[str] = "", task: Optional[Task] = None):
        """Create an Output object for this step last task (or the whole step if grouped is True or a specific task if task is specified)."""
        if name is not None:
            output_glob = self.outputs_globs.get(name, "")
        else:
            output_glob = ""
        if task is None:
            task = self.task
        return Output(task=task, grouped=grouped, globs=output_glob, move=move, action=action)

    def compile(self, client: Scitq2Client, opportunistic: bool = False, untrusted: Optional[List[str]] = None):
        """Compile the Step into real scitq objects by calling appropriate gRPC functions. Called automatically during the template run phase."""
        quality_json = self.quality.to_json() if self.quality else None
        ext = self.workflow._extend
        # Find-or-create the step by name. In extend mode, a step that already
        # exists in the target workflow is reused (its tasks get reconciled);
        # only a genuinely new step is created.
        step_existed = False
        if ext is not None and self.name in ext.step_ids:
            self.step_id = ext.step_ids[self.name]
            step_existed = True
        else:
            self.step_id = client.create_step(self.workflow.workflow_id, self.name, quality_definition=quality_json)
            if ext is not None:
                ext.step_ids[self.name] = self.step_id

        # Only attach a recruiter to a newly-created step — an existing step
        # already has its recruiter(s); adding another would duplicate.
        pool = self.worker_pool or self.workflow.worker_pool
        if pool and not step_existed:
            options = pool.build_recruiter(self.task_spec,
                                           default_provider=self.workflow.provider,
                                           default_region=self.workflow.region)
            try:
                client.create_recruiter(step_id=self.step_id, **options)
            except Exception as e:
                print(f'[Step {self.step_id}:{self.name}] Failure {e} with options {options}')
                raise e

        for task in self.tasks:
            task.compile(client, opportunistic=opportunistic, untrusted=untrusted)

        # Extend: widen self.tasks so grouped/fan-in outputs see the full
        # set of existing tasks for this step, not just the ones this run
        # touched. Without this, a compile/aggregation step downstream
        # (Step.output(grouped=True)) silently produces inputs for only
        # the new samples, dropping anything previously processed.
        #
        # We include EVERY existing task — including failed ones —
        # unconditionally. Silently filtering them produces aggregations
        # that look fine but quietly omit data; if an operator wants a
        # failed task excluded, they delete/hide it deliberately. See
        # memory feedback_no_silent_filtering.
        if ext is not None:
            existing = ext.tasks_by_step.get(self.step_id, {})
            already_covered = {t.full_name for t in self.tasks}
            for task_name, (task_id, _cmd, _container, _status, publish) in existing.items():
                if task_name in already_covered:
                    continue
                self.tasks.append(Task.as_reference(
                    step=self,
                    task_name=task_name,
                    task_id=task_id,
                    publish=publish,
                ))

    def grouped(self) -> GroupedStep:
        """Create a grouped step with a specific tag."""
        return GroupedStep(step=self)
    
    @property
    def container(self) -> str:
        """Return the container for the last task in this step."""
        return self.task.container

    @property
    def task(self) -> Task:
        """Return the latest task for this step."""
        if not self.tasks:
            raise ValueError(f"Step {self.name} has no tasks defined")
        return self.tasks[-1]

class _ExtendContext:
    """State carried through compile() when extending an existing workflow.

    See specs/workflow_extend.md. Built once in Workflow.compile from the
    target workflow's current steps and (non-hidden) tasks, then consulted by
    Step.compile (find-or-create step by name) and Task.compile
    (find-or-reference/edit task by (step_id, task_name), with cascade).
    """
    def __init__(self, workflow_id, retry_failed_only, step_ids, tasks_by_step, workflow_name):
        self.workflow_id = workflow_id
        self.retry_failed_only = retry_failed_only
        self.step_ids = step_ids            # {step_name: step_id}
        self.tasks_by_step = tasks_by_step  # {step_id: {task_name: (task_id, command, container, status, publish)}}
        self.workflow_name = workflow_name
        # task_ids (re)created or edit-and-retried this pass; drives the
        # default cascade ("re-run a dependent whose prerequisite changed").
        self.changed = set()


class Workflow:
    """Workflow objects are the corner stone of scitq DSL. They define the name, version and description of the template, 
    as well as the defining settings for future scitq workflows created by the template. All Steps and Tasks are attached to the
    Workflow and there can only be one Workflow object in a scitq DSL script.
    - name: the template name (must be unique in scitq instance),
    - version: enable to have different versions of the same template,
    - description: a help text for users that shows in both UI and CLI,
    - tag: a subname added (see naming_strategy) to the template name to create a workflow name that help distinguish each run (otherwise they are numbered),
    - language: (default step value) language of commands in steps,
    - worker_pool: definition of the workflow level worker pool (see WorkerPool objects), the workflow WorkerPool maximum_recruited define the workflow maximal recruitment,
    - provider: instance provider ('local.local' for permanent workers, "azure.primary" or "openstack.ovh" for cloud workers, etc.),
    - region: instance region (e.g. 'swedencentral' for Microsoft or 'GRA11' for OVH)
    - naming_strategy: function to define how workflow and step names are constructed (default to dot_join),
    - task_naming_strategy: function to define how task names are constructed (default to dot_join),
    - container: (default step value) Docker container image for steps (can be overridden at step level),
    - publish_root: base URI for publishing outputs (e.g. "azure://rnd/results/project/"). Steps can then use Outputs(publish=True) or Outputs(publish="subdir/"),
    - resources: default resources for all steps (can be overridden at step level),
    - retry: default number of retry for each task in the workflow (can be overridden at step level),
    - run_strategy: workflow scheduling mode. Accepts a single-letter DB code ('B'/'T'/'D'/'Z') or a friendly word ('batch'/'thread'/'debug'/'suspended'). Default: server picks 'B' (batch). 'T' (thread) requests sticky scheduling — currently a partially-implemented feature: the field is plumbed through to the database, but the sticky-scheduling and worker-side I/O short-circuit logic is still pending (see specs/sticky_thread_run_strategy.md), so 'T' behaves like 'B' until that ships
    """
    last_created = None

    def __init__(self, name: str, version:str, description: str = "", worker_pool: Optional[WorkerPool] = None, language: Optional[Language] = None, tag: Optional[str] = None,
                 naming_strategy: callable = dot_join, task_naming_strategy: callable = dot_join, provider: Optional[str] = None, region: Optional[str] = None,
                 container: Optional[str] = None, publish_root: Optional[str] = None,
                 resources: Optional[Union[Resource, str, List[Resource], List[str]]] = None,
                 skip_if_exists: bool = False, retry: Optional[int] = None,
                 live: bool = False, run_strategy: Optional[str] = None):
        self.name = name
        self.live = live
        self.tag = tag
        self.description = description
        self._steps: Dict[str, Step] = {}
        self.worker_pool = worker_pool
        self.max_recruited = worker_pool.max_recruited if worker_pool else None
        self.language = language or Raw()
        self.naming_strategy = naming_strategy
        self.task_naming_strategy = task_naming_strategy
        self.provider = provider
        self.region = region
        self.workflow_id: Optional[int] = None
        self.full_name: Optional[str] = None
        self.workspace_root: Optional[str] = None
        # Set by compile() when extending an existing workflow (see
        # specs/workflow_extend.md); None for a normal create-new run.
        self._extend: Optional["_ExtendContext"] = None
        self.version = version
        self.container = container
        self.publish_root = publish_root.rstrip("/") if publish_root else None
        if resources is None:
            self.resources = []
        elif isinstance(resources, (Resource, str)):
            self.resources = [resources]
        else:
            self.resources = list(resources)
        self.skip_if_exists = skip_if_exists
        self.retry = retry
        # Accept either a single-letter DB code (B/T/D/Z) or a friendly word
        # (batch/thread/debug/suspended). None → server default ('B').
        self.run_strategy = _normalise_run_strategy(run_strategy)
        if Workflow.last_created is not None:
            print(f"⚠️ Warning: it is highly recommended to avoid declaring several Workflow in a code, you have previously declared {Workflow.last_created.name} and you redeclare {self.name}", file=sys.stderr)
        Workflow.last_created = self

    def Step(
        self,
        *,
        name: str,
        command: str,
        container: Optional[str] = None,
        tag: Optional[str] = None,
        inputs: Optional[Union[str, OutputBase, List[str], List[OutputBase]]] = None,
        outputs: Optional[Outputs] = None,
        resources: Optional[Union[Resource, str, List[Resource], List[str]]] = None,
        language: Optional[Language] = None,
        worker_pool: Optional[WorkerPool] = None,
        task_spec: Optional[TaskSpec] = None,
        naming_strategy: Optional[callable] = None,
        depends: Optional[Union["Step", List["Step"]]] = None,
        skip_if_exists: Optional[bool] = None,
        retry: Optional[int] = None,
        accept_failure: bool = False,
        quality: Optional[Quality] = None,
    ) -> Step:
        """Add a Step to the Workflow with a single Task.
        If the Step already exists with the same name, the Task is added to the existing Step.
        - name: the step name,
        - command: the command to run in the task,
        - container: the container image to use for the task,
        - tag: an optional tag to distinguish multiple tasks within the same step,
        - inputs: optional inputs for the task (can be str, Output, or list of these),
        - outputs: optional Outputs object defining the task outputs,
        - resources: optional resources required for the task (can be Resource, str, or list of these),
        - language: optional language for the task command (overrides workflow default),
        - worker_pool: optional worker pool for the step (overrides workflow default),
        - task_spec: optional task specification for the step (overrides workflow default),
        - naming_strategy: optional naming strategy for the step (overrides workflow default),
        - depends: optional dependencies for the task (can be Step or list of Steps),
        - retry: optional number of retries for the task (overrides workflow default)"""
        if container is None:
            container = self.container
        if container is None:
            raise ValueError(f"Step '{name}' has no container specified and no default container is set on the Workflow.")
        if naming_strategy is None:
            naming_strategy = self.task_naming_strategy
        if skip_if_exists is None:
            skip_if_exists = self.skip_if_exists
        if retry is None:
            retry = self.retry
        new_step = Step(name=name, workflow=self, worker_pool=worker_pool, task_spec=task_spec, naming_strategy=naming_strategy, quality=quality)
        if name in self._steps:
            existing = self._steps[name]
            if (existing.worker_pool != new_step.worker_pool or existing.task_spec != new_step.task_spec):
                print("worker_pool", existing.worker_pool, new_step.worker_pool)
                print("task_spec", existing.task_spec, new_step.task_spec)
                raise ValueError(
                    f"Step '{name}' was already defined with a different worker_pool or task_spec. "
                    "Steps with different specifications must be given distinct names."
                )
            step = existing
        else:
            self._steps[name] = new_step
            step = new_step

        effective_language = language or self.language
        if tag is None and step.tasks:
            raise RuntimeError(f"Step '{name}' has no tag specified and has several iterations which is forbidden")
        step.add_task(tag=tag, command=command, container=container, outputs=outputs, inputs=inputs, resources=resources,
                      language=effective_language, depends=depends, skip_if_exists=skip_if_exists, retry=retry,
                      accept_failure=accept_failure)
        return step

    def _build_extend_context(self, client: Scitq2Client, workflow_id: int, retry_failed_only: bool) -> "_ExtendContext":
        """Snapshot the target workflow's steps and live tasks for reconcile."""
        steps = client.list_steps(workflow_id)
        step_ids = {s.name: s.step_id for s in steps}
        workflow_name = steps[0].workflow_name if steps else None
        if workflow_name is None:
            # Extending a workflow with no steps yet — resolve the name directly.
            for wf in client.list_workflows():
                if wf.workflow_id == workflow_id:
                    workflow_name = wf.name
                    break
        if workflow_name is None:
            raise ValueError(f"extend: workflow {workflow_id} not found")
        tasks_by_step: dict = {}
        for t in client.list_tasks(workflow_id=workflow_id, show_hidden=False):
            if not t.task_name:
                continue
            # 4-tuple (was 3-tuple). The 4th element is "where do this
            # task's outputs live?" — prefer publish, fall back to output.
            # The DSL submit path aliases publish into the output column
            # when no workspace_root is set (see Task.compile's
            # resolved_output/resolved_publish logic), so a task that
            # originally declared publish="..." in the DSL appears with
            # publish=NULL and output=URI in the DB. Reference tasks we
            # synthesise for grouped/fan-in outputs need the URI in
            # either column to produce the right path; without this
            # fallback, build_path returns None and silently drops the
            # task from the aggregation.
            publish = t.publish if t.HasField("publish") else None
            if publish is None and t.HasField("output"):
                publish = t.output
            tasks_by_step.setdefault(t.step_id, {})[t.task_name] = (
                t.task_id, t.command, t.container, t.status, publish,
            )
        return _ExtendContext(workflow_id, retry_failed_only, step_ids, tasks_by_step, workflow_name)

    def compile(self, client: Scitq2Client, *, activate_leading_tasks: bool = False, workflow_status: Optional[str] = None,
                opportunistic: bool = False, untrusted: Optional[List[str]] = None,
                extend_workflow_id: Optional[int] = None, retry_failed_only: bool = False) -> int:
        if self.provider:
            self.workspace_root = client.get_workspace_root(
                provider=self.provider,
                region=self.region,
            )
        else:
            self.workspace_root = None

        if extend_workflow_id is not None:
            # Extend mode: reuse the existing workflow, don't create one. The
            # full_name comes from the existing workflow so task output paths
            # land alongside its current tasks. Workflow-level settings
            # (status, max_recruited, run_strategy, live) are left untouched.
            self._extend = self._build_extend_context(client, extend_workflow_id, retry_failed_only)
            self.workflow_id = extend_workflow_id
            self.full_name = self._extend.workflow_name
        else:
            self._extend = None
            base_name = self.naming_strategy(self.name, self.tag) if self.tag else self.name
            for i in count():
                candidate_name = base_name if i == 0 else self.naming_strategy(base_name,str(i))
                try:
                    self.workflow_id = client.create_workflow(
                        name=candidate_name,
                        maximum_workers=1 if workflow_status == "D" else self.max_recruited,
                        status=workflow_status,
                        live=self.live,
                        run_strategy=self.run_strategy,
                    )
                    self.full_name = candidate_name
                    break
                except Exception as e:
                    if 'unique constraint' in str(e).lower() or 'duplicate key' in str(e).lower():
                        continue
                    raise  # re-raise non-duplicate errors

        template_run_id = os.environ.get("SCITQ_TEMPLATE_RUN_ID")
        if template_run_id:
            client.update_template_run(template_run_id=int(template_run_id), workflow_id=self.workflow_id)
        for step in self._steps.values():
            step.compile(client, opportunistic=opportunistic, untrusted=untrusted)

        if activate_leading_tasks:
            client.update_workflow_status(workflow_id=self.workflow_id, status="R")
        return self.workflow_id
    
    @property
    def steps(self) -> List[Step]:
        return list(self._steps.values())
