from typing import Any, List, Optional
from scitq2.constants import DEFAULT_RECRUITER_TIMEOUT
import sys
from dataclasses import dataclass

@dataclass(frozen=True)
class FieldExpr:
    field: str
    op: str
    value: Any

    def to_dict(self):
        return {
            "field": self.field,
            "op": self.op,
            "value": self.value,
        }

    def to_protofilter(self):
        val = str(self.value)
        if self.op == "is":
            return f"{self.field} is {val}"
        return f"{self.field}{self.op}{val}"


class WorkerSymbol:
    def __getattr__(self, field):
        return FieldBuilder(field)


class FieldBuilder:
    def __init__(self, field):
        self.field = field

    def __eq__(self, value): return FieldExpr(self.field, "==", value)
    def __ne__(self, value): raise NotImplementedError("'!=' is not supported in protofilter expressions")

class NumberFieldBuilder(FieldBuilder):
    def __ge__(self, value): return FieldExpr(self.field, ">=", value)
    def __gt__(self, value):  return FieldExpr(self.field, ">", value)
    def __le__(self, value): return FieldExpr(self.field, "<=", value)
    def __lt__(self, value):  return FieldExpr(self.field, "<", value)


class StringFieldBuilder(FieldBuilder):

    def like(self, pattern: str):
        return FieldExpr(self.field, "~", pattern)

class StringWithDefaultFieldBuilder(StringFieldBuilder):
    def is_default(self):
        return FieldExpr(self.field, "is", "default")


class W:
    """
    Static filter interface for worker recruitment.
    This class provides a set of fields that can be used to filter workers
    based on their attributes such as cpu, mem (memory), region, provider, flavor, and disk.
    Usage:
    worker_pool=WorkerPool(
        W.provider.like("azure%"),
        W.region.is_default(),
        W.cpu == 32,
        W.mem >= 120,
        W.disk >= 400,
        max_recruited=10,
        task_batches=2
    )
    """
    cpu = NumberFieldBuilder("cpu")
    mem = NumberFieldBuilder("mem")
    region = StringWithDefaultFieldBuilder("region")
    provider = StringFieldBuilder("provider")
    flavor = StringFieldBuilder("flavor")
    disk = NumberFieldBuilder("disk")


class WorkerPool:
    """
    Defines a strategy for recruiting workers based on hardware or metadata constraints.

    A WorkerPool specifies:
    - A list of filter expressions (e.g., W.cpu >= 32) that determine worker compatibility.
    - Optional recruiter-level parameters such as `max_recruited`.
    - Arbitrary extra options (e.g., container image, zone) passed to the backend.

    NB: while filter expressions accept W.region and W.provider you should refrain to use those and
    set region and provider at Workflow level to minimize inter-regional transfer fees. Yet you can still
    use these filters notably when some specific instances are required but beware of transfer fees.

    This class also supports estimating task concurrency based on a TaskSpec (cpu/mem needs).

    Parameters:
        *match: FieldExpr
            Filter expressions to select eligible workers.
        max_recruited: int, optional
            Maximum number of workers this pool can recruit (per step).
        task_batches: int, optional
            Number of batches that each worker should do to complete the step workload
        timeout: int, optional
            Time (in seconds) to wait for a recyclable worker before resorting to creating a new one


    Example:
        WorkerPool(
            W.cpu >= 32,
            max_recruited=10,
            task_batches=2
        )
    """
    
    def __init__(self, *match: FieldExpr, max_recruited: Optional[int] = None, task_batches: int = 1, timeout: int = DEFAULT_RECRUITER_TIMEOUT):
        self.match = set(match)
        self.extra_options = {}
        if max_recruited is not None:
            if max_recruited == 0:
                raise RuntimeError("Setting up a worker pool that is empty by construction is not allowed.")
            self.extra_options["max_recruited"]=max_recruited
        self.extra_options["rounds"]=task_batches
        self.extra_options["timeout"]=timeout

    @property
    def task_batches(self):
        return self.extra_options["rounds"]
    
    @property
    def timeout(self):
        return self.extra_options["timeout"]

    @property
    def max_recruited(self):
        return self.extra_options.get("max_recruited", None)

    def compile_filter(self, default_provider: Optional[str] = None, default_region: Optional[str] = None) -> str:
        """ Compiles the worker pool's match expressions into a protofilter string."""
        constraints = list(self.match)  # copy to avoid mutation

        existing_fields = {expr.field for expr in constraints}

        if default_provider and "provider" not in existing_fields:
            constraints.append(FieldExpr("provider", "==", default_provider))
        else:
            print(f"⚠️ Warning: using provider filter may generate high transfer fees, hope you know what you are doing.", file=sys.stderr)

        if default_region and "region" not in existing_fields:
            constraints.append(FieldExpr("region", "==", default_region))
        else:
            print(f"⚠️ Warning: using region filter may generate high transfer fees, hope you know what you are doing.", file=sys.stderr)

        return compile_protofilter(constraints)

    def build_recruiter(self, task_spec, default_provider: Optional[str] = None, default_region: Optional[str] = None) -> tuple[str, dict]:
        options = dict(self.extra_options)
        options["protofilter"] = self.compile_filter(default_provider=default_provider, default_region=default_region)

        if task_spec is None:
            options["concurrency"] = 1
            options["prefetch"] = 0
        else:
            if task_spec.concurrency is None:
                options["cpu_per_task"]=task_spec.cpu
                options["memory_per_task"]=task_spec.mem
                options["prefetch_percent"]=int(task_spec.prefetch*100)
            else:
                concurrency = task_spec.concurrency
                prefetch = round(concurrency * task_spec.prefetch)
                options["concurrency"] = concurrency
                if prefetch >= 0:
                    options["prefetch"] = prefetch

            

        return options
    
    def clone_with(self, *match: FieldExpr, max_recruited: Optional[int] = None, task_batches: Optional[int] = None, 
                    timeout: Optional[int] = None) -> "WorkerPool":
        """
        Returns a copy of the current WorkerPool with optionally overridden fields.
        Any provided arguments override the corresponding fields in the original pool.
        """
        new_match = list(match) if match else self.match
        return WorkerPool(*new_match, 
                          max_recruited=max_recruited if max_recruited is not None else self.max_recruited,
                          task_batches=task_batches if task_batches is not None else self.task_batches,
                          timeout=timeout if timeout is not None else self.timeout)
    
    def __eq__(self, other: Any) -> bool:
        if not isinstance(other, WorkerPool):
            return False
        return (self.match == other.match and
                self.max_recruited == other.max_recruited and
                self.extra_options == other.extra_options)


def compile_protofilter(match: List[FieldExpr]) -> str:
    return ":".join(expr.to_protofilter() for expr in match)
