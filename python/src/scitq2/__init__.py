# dsl.py — user-facing DSL surface for scitq2

from .workflow import Workflow, TaskSpec, Outputs
from .param import Param, ParamSpec
from .recruit import WorkerPool, W
from .util import cond
from .language import Shell, Raw, Python
from .uri import Resource, URI, check_if_file
from .runner import run
from .grpc_client import Scitq2Client as Client

try:
    from .__version__ import __version__, __commit__, __build_time__
except Exception:
    __version__ = "0.0.0+dev"
    __commit__ = "unknown"
    __build_time__ = "unknown"

__all__ = [
    "Workflow",
    "Param", "ParamSpec",
    "WorkerPool", "W",
    "TaskSpec",
    "Shell",
    "Raw",
    "Python",
    "Resource",
    "Outputs",
    "cond",
    "URI",
    "check_if_file",
    "run",
    "Client",
    "__version__",
    "__commit__",
    "__build_time__"
]

