from google.protobuf import empty_pb2 as _empty_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TaskResponse(_message.Message):
    __slots__ = ("task_id",)
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    def __init__(self, task_id: _Optional[int] = ...) -> None: ...

class WorkerInfo(_message.Message):
    __slots__ = ("name", "concurrency", "is_permanent", "provider", "region", "version", "commit", "build_arch")
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    IS_PERMANENT_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    COMMIT_FIELD_NUMBER: _ClassVar[int]
    BUILD_ARCH_FIELD_NUMBER: _ClassVar[int]
    name: str
    concurrency: int
    is_permanent: bool
    provider: str
    region: str
    version: str
    commit: str
    build_arch: str
    def __init__(self, name: _Optional[str] = ..., concurrency: _Optional[int] = ..., is_permanent: bool = ..., provider: _Optional[str] = ..., region: _Optional[str] = ..., version: _Optional[str] = ..., commit: _Optional[str] = ..., build_arch: _Optional[str] = ...) -> None: ...

class ServerVersionResponse(_message.Message):
    __slots__ = ("version", "commit", "build_arch")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    COMMIT_FIELD_NUMBER: _ClassVar[int]
    BUILD_ARCH_FIELD_NUMBER: _ClassVar[int]
    version: str
    commit: str
    build_arch: str
    def __init__(self, version: _Optional[str] = ..., commit: _Optional[str] = ..., build_arch: _Optional[str] = ...) -> None: ...

class TaskRequest(_message.Message):
    __slots__ = ("command", "shell", "container", "container_options", "step_id", "input", "resource", "output", "retry", "is_final", "uses_cache", "download_timeout", "running_timeout", "upload_timeout", "status", "dependency", "task_name", "skip_if_exists", "accept_failure", "publish", "reuse_key", "consume_reuse")
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    SHELL_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    INPUT_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    RETRY_FIELD_NUMBER: _ClassVar[int]
    IS_FINAL_FIELD_NUMBER: _ClassVar[int]
    USES_CACHE_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    RUNNING_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DEPENDENCY_FIELD_NUMBER: _ClassVar[int]
    TASK_NAME_FIELD_NUMBER: _ClassVar[int]
    SKIP_IF_EXISTS_FIELD_NUMBER: _ClassVar[int]
    ACCEPT_FAILURE_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_FIELD_NUMBER: _ClassVar[int]
    REUSE_KEY_FIELD_NUMBER: _ClassVar[int]
    CONSUME_REUSE_FIELD_NUMBER: _ClassVar[int]
    command: str
    shell: str
    container: str
    container_options: str
    step_id: int
    input: _containers.RepeatedScalarFieldContainer[str]
    resource: _containers.RepeatedScalarFieldContainer[str]
    output: str
    retry: int
    is_final: bool
    uses_cache: bool
    download_timeout: float
    running_timeout: float
    upload_timeout: float
    status: str
    dependency: _containers.RepeatedScalarFieldContainer[int]
    task_name: str
    skip_if_exists: bool
    accept_failure: bool
    publish: str
    reuse_key: str
    consume_reuse: bool
    def __init__(self, command: _Optional[str] = ..., shell: _Optional[str] = ..., container: _Optional[str] = ..., container_options: _Optional[str] = ..., step_id: _Optional[int] = ..., input: _Optional[_Iterable[str]] = ..., resource: _Optional[_Iterable[str]] = ..., output: _Optional[str] = ..., retry: _Optional[int] = ..., is_final: bool = ..., uses_cache: bool = ..., download_timeout: _Optional[float] = ..., running_timeout: _Optional[float] = ..., upload_timeout: _Optional[float] = ..., status: _Optional[str] = ..., dependency: _Optional[_Iterable[int]] = ..., task_name: _Optional[str] = ..., skip_if_exists: bool = ..., accept_failure: bool = ..., publish: _Optional[str] = ..., reuse_key: _Optional[str] = ..., consume_reuse: bool = ...) -> None: ...

class Task(_message.Message):
    __slots__ = ("task_id", "command", "shell", "container", "container_options", "step_id", "input", "resource", "output", "retry", "is_final", "uses_cache", "download_timeout", "running_timeout", "upload_timeout", "status", "worker_id", "workflow_id", "task_name", "retry_count", "hidden", "previous_task_id", "weight", "run_start_time", "skip_if_exists", "publish", "reuse_key", "download_duration", "run_duration", "upload_duration", "quality_score", "quality_vars")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    SHELL_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    INPUT_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    RETRY_FIELD_NUMBER: _ClassVar[int]
    IS_FINAL_FIELD_NUMBER: _ClassVar[int]
    USES_CACHE_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    RUNNING_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_NAME_FIELD_NUMBER: _ClassVar[int]
    RETRY_COUNT_FIELD_NUMBER: _ClassVar[int]
    HIDDEN_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_TASK_ID_FIELD_NUMBER: _ClassVar[int]
    WEIGHT_FIELD_NUMBER: _ClassVar[int]
    RUN_START_TIME_FIELD_NUMBER: _ClassVar[int]
    SKIP_IF_EXISTS_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_FIELD_NUMBER: _ClassVar[int]
    REUSE_KEY_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_DURATION_FIELD_NUMBER: _ClassVar[int]
    RUN_DURATION_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_DURATION_FIELD_NUMBER: _ClassVar[int]
    QUALITY_SCORE_FIELD_NUMBER: _ClassVar[int]
    QUALITY_VARS_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    command: str
    shell: str
    container: str
    container_options: str
    step_id: int
    input: _containers.RepeatedScalarFieldContainer[str]
    resource: _containers.RepeatedScalarFieldContainer[str]
    output: str
    retry: int
    is_final: bool
    uses_cache: bool
    download_timeout: float
    running_timeout: float
    upload_timeout: float
    status: str
    worker_id: int
    workflow_id: int
    task_name: str
    retry_count: int
    hidden: bool
    previous_task_id: int
    weight: float
    run_start_time: int
    skip_if_exists: bool
    publish: str
    reuse_key: str
    download_duration: int
    run_duration: int
    upload_duration: int
    quality_score: float
    quality_vars: str
    def __init__(self, task_id: _Optional[int] = ..., command: _Optional[str] = ..., shell: _Optional[str] = ..., container: _Optional[str] = ..., container_options: _Optional[str] = ..., step_id: _Optional[int] = ..., input: _Optional[_Iterable[str]] = ..., resource: _Optional[_Iterable[str]] = ..., output: _Optional[str] = ..., retry: _Optional[int] = ..., is_final: bool = ..., uses_cache: bool = ..., download_timeout: _Optional[float] = ..., running_timeout: _Optional[float] = ..., upload_timeout: _Optional[float] = ..., status: _Optional[str] = ..., worker_id: _Optional[int] = ..., workflow_id: _Optional[int] = ..., task_name: _Optional[str] = ..., retry_count: _Optional[int] = ..., hidden: bool = ..., previous_task_id: _Optional[int] = ..., weight: _Optional[float] = ..., run_start_time: _Optional[int] = ..., skip_if_exists: bool = ..., publish: _Optional[str] = ..., reuse_key: _Optional[str] = ..., download_duration: _Optional[int] = ..., run_duration: _Optional[int] = ..., upload_duration: _Optional[int] = ..., quality_score: _Optional[float] = ..., quality_vars: _Optional[str] = ...) -> None: ...

class TaskList(_message.Message):
    __slots__ = ("tasks",)
    TASKS_FIELD_NUMBER: _ClassVar[int]
    tasks: _containers.RepeatedCompositeFieldContainer[Task]
    def __init__(self, tasks: _Optional[_Iterable[_Union[Task, _Mapping]]] = ...) -> None: ...

class RetryTaskRequest(_message.Message):
    __slots__ = ("task_id", "retry")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RETRY_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    retry: int
    def __init__(self, task_id: _Optional[int] = ..., retry: _Optional[int] = ...) -> None: ...

class ForceRunTaskRequest(_message.Message):
    __slots__ = ("task_id",)
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    def __init__(self, task_id: _Optional[int] = ...) -> None: ...

class EditAndRetryTaskRequest(_message.Message):
    __slots__ = ("task_id", "command")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    command: str
    def __init__(self, task_id: _Optional[int] = ..., command: _Optional[str] = ...) -> None: ...

class StringList(_message.Message):
    __slots__ = ("values",)
    VALUES_FIELD_NUMBER: _ClassVar[int]
    values: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, values: _Optional[_Iterable[str]] = ...) -> None: ...

class EditTaskRequest(_message.Message):
    __slots__ = ("task_id", "command", "container", "container_options", "shell", "status", "input", "resource", "output", "publish", "retry")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_OPTIONS_FIELD_NUMBER: _ClassVar[int]
    SHELL_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    INPUT_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    PUBLISH_FIELD_NUMBER: _ClassVar[int]
    RETRY_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    command: str
    container: str
    container_options: str
    shell: str
    status: str
    input: StringList
    resource: StringList
    output: str
    publish: str
    retry: int
    def __init__(self, task_id: _Optional[int] = ..., command: _Optional[str] = ..., container: _Optional[str] = ..., container_options: _Optional[str] = ..., shell: _Optional[str] = ..., status: _Optional[str] = ..., input: _Optional[_Union[StringList, _Mapping]] = ..., resource: _Optional[_Union[StringList, _Mapping]] = ..., output: _Optional[str] = ..., publish: _Optional[str] = ..., retry: _Optional[int] = ...) -> None: ...

class EditStepCommandRequest(_message.Message):
    __slots__ = ("step_id", "find", "replace", "is_regexp")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    FIND_FIELD_NUMBER: _ClassVar[int]
    REPLACE_FIELD_NUMBER: _ClassVar[int]
    IS_REGEXP_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    find: str
    replace: str
    is_regexp: bool
    def __init__(self, step_id: _Optional[int] = ..., find: _Optional[str] = ..., replace: _Optional[str] = ..., is_regexp: bool = ...) -> None: ...

class EditStepCommandResponse(_message.Message):
    __slots__ = ("edited_count", "new_task_ids")
    EDITED_COUNT_FIELD_NUMBER: _ClassVar[int]
    NEW_TASK_IDS_FIELD_NUMBER: _ClassVar[int]
    edited_count: int
    new_task_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, edited_count: _Optional[int] = ..., new_task_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class Worker(_message.Message):
    __slots__ = ("worker_id", "name", "concurrency", "prefetch", "status", "ipv4", "ipv6", "flavor", "provider", "region", "step_id", "step_name", "is_permanent", "recyclable_scope", "workflow_id", "workflow_name", "flavor_cpu", "flavor_mem", "flavor_disk", "version", "commit", "build_arch", "upgrade_status", "upgrade_requested")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    IPV4_FIELD_NUMBER: _ClassVar[int]
    IPV6_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_PERMANENT_FIELD_NUMBER: _ClassVar[int]
    RECYCLABLE_SCOPE_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_NAME_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_CPU_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_MEM_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_DISK_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    COMMIT_FIELD_NUMBER: _ClassVar[int]
    BUILD_ARCH_FIELD_NUMBER: _ClassVar[int]
    UPGRADE_STATUS_FIELD_NUMBER: _ClassVar[int]
    UPGRADE_REQUESTED_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    name: str
    concurrency: int
    prefetch: int
    status: str
    ipv4: str
    ipv6: str
    flavor: str
    provider: str
    region: str
    step_id: int
    step_name: str
    is_permanent: bool
    recyclable_scope: str
    workflow_id: int
    workflow_name: str
    flavor_cpu: int
    flavor_mem: float
    flavor_disk: float
    version: str
    commit: str
    build_arch: str
    upgrade_status: str
    upgrade_requested: str
    def __init__(self, worker_id: _Optional[int] = ..., name: _Optional[str] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., status: _Optional[str] = ..., ipv4: _Optional[str] = ..., ipv6: _Optional[str] = ..., flavor: _Optional[str] = ..., provider: _Optional[str] = ..., region: _Optional[str] = ..., step_id: _Optional[int] = ..., step_name: _Optional[str] = ..., is_permanent: bool = ..., recyclable_scope: _Optional[str] = ..., workflow_id: _Optional[int] = ..., workflow_name: _Optional[str] = ..., flavor_cpu: _Optional[int] = ..., flavor_mem: _Optional[float] = ..., flavor_disk: _Optional[float] = ..., version: _Optional[str] = ..., commit: _Optional[str] = ..., build_arch: _Optional[str] = ..., upgrade_status: _Optional[str] = ..., upgrade_requested: _Optional[str] = ...) -> None: ...

class WorkersList(_message.Message):
    __slots__ = ("workers",)
    WORKERS_FIELD_NUMBER: _ClassVar[int]
    workers: _containers.RepeatedCompositeFieldContainer[Worker]
    def __init__(self, workers: _Optional[_Iterable[_Union[Worker, _Mapping]]] = ...) -> None: ...

class ListWorkersRequest(_message.Message):
    __slots__ = ("workflow_id",)
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    def __init__(self, workflow_id: _Optional[int] = ...) -> None: ...

class WorkerUpgradeRequest(_message.Message):
    __slots__ = ("worker_ids", "all", "mode")
    WORKER_IDS_FIELD_NUMBER: _ClassVar[int]
    ALL_FIELD_NUMBER: _ClassVar[int]
    MODE_FIELD_NUMBER: _ClassVar[int]
    worker_ids: _containers.RepeatedScalarFieldContainer[int]
    all: bool
    mode: str
    def __init__(self, worker_ids: _Optional[_Iterable[int]] = ..., all: bool = ..., mode: _Optional[str] = ...) -> None: ...

class WorkerUpgradeReply(_message.Message):
    __slots__ = ("affected_worker_ids",)
    AFFECTED_WORKER_IDS_FIELD_NUMBER: _ClassVar[int]
    affected_worker_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, affected_worker_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class ClientUpgradeInfo(_message.Message):
    __slots__ = ("binary_url", "sha256_url", "insecure_skip_verify")
    BINARY_URL_FIELD_NUMBER: _ClassVar[int]
    SHA256_URL_FIELD_NUMBER: _ClassVar[int]
    INSECURE_SKIP_VERIFY_FIELD_NUMBER: _ClassVar[int]
    binary_url: str
    sha256_url: str
    insecure_skip_verify: bool
    def __init__(self, binary_url: _Optional[str] = ..., sha256_url: _Optional[str] = ..., insecure_skip_verify: bool = ...) -> None: ...

class TaskUpdate(_message.Message):
    __slots__ = ("weight",)
    WEIGHT_FIELD_NUMBER: _ClassVar[int]
    weight: float
    def __init__(self, weight: _Optional[float] = ...) -> None: ...

class TaskUpdateList(_message.Message):
    __slots__ = ("updates",)
    class UpdatesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: int
        value: TaskUpdate
        def __init__(self, key: _Optional[int] = ..., value: _Optional[_Union[TaskUpdate, _Mapping]] = ...) -> None: ...
    UPDATES_FIELD_NUMBER: _ClassVar[int]
    updates: _containers.MessageMap[int, TaskUpdate]
    def __init__(self, updates: _Optional[_Mapping[int, TaskUpdate]] = ...) -> None: ...

class TaskSignal(_message.Message):
    __slots__ = ("task_id", "signal", "grace_period")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    SIGNAL_FIELD_NUMBER: _ClassVar[int]
    GRACE_PERIOD_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    signal: str
    grace_period: int
    def __init__(self, task_id: _Optional[int] = ..., signal: _Optional[str] = ..., grace_period: _Optional[int] = ...) -> None: ...

class TaskListAndOther(_message.Message):
    __slots__ = ("tasks", "concurrency", "updates", "active_tasks", "signals", "upgrade_requested", "server_upgrade_in_progress")
    TASKS_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    UPDATES_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_TASKS_FIELD_NUMBER: _ClassVar[int]
    SIGNALS_FIELD_NUMBER: _ClassVar[int]
    UPGRADE_REQUESTED_FIELD_NUMBER: _ClassVar[int]
    SERVER_UPGRADE_IN_PROGRESS_FIELD_NUMBER: _ClassVar[int]
    tasks: _containers.RepeatedCompositeFieldContainer[Task]
    concurrency: int
    updates: TaskUpdateList
    active_tasks: _containers.RepeatedScalarFieldContainer[int]
    signals: _containers.RepeatedCompositeFieldContainer[TaskSignal]
    upgrade_requested: str
    server_upgrade_in_progress: bool
    def __init__(self, tasks: _Optional[_Iterable[_Union[Task, _Mapping]]] = ..., concurrency: _Optional[int] = ..., updates: _Optional[_Union[TaskUpdateList, _Mapping]] = ..., active_tasks: _Optional[_Iterable[int]] = ..., signals: _Optional[_Iterable[_Union[TaskSignal, _Mapping]]] = ..., upgrade_requested: _Optional[str] = ..., server_upgrade_in_progress: bool = ...) -> None: ...

class TaskSignalRequest(_message.Message):
    __slots__ = ("task_id", "signal", "grace_period")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    SIGNAL_FIELD_NUMBER: _ClassVar[int]
    GRACE_PERIOD_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    signal: str
    grace_period: int
    def __init__(self, task_id: _Optional[int] = ..., signal: _Optional[str] = ..., grace_period: _Optional[int] = ...) -> None: ...

class TaskStatusUpdate(_message.Message):
    __slots__ = ("task_id", "new_status", "duration", "free_retry")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    NEW_STATUS_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    FREE_RETRY_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    new_status: str
    duration: int
    free_retry: bool
    def __init__(self, task_id: _Optional[int] = ..., new_status: _Optional[str] = ..., duration: _Optional[int] = ..., free_retry: bool = ...) -> None: ...

class TaskLog(_message.Message):
    __slots__ = ("task_id", "log_type", "log_text")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    LOG_TYPE_FIELD_NUMBER: _ClassVar[int]
    LOG_TEXT_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    log_type: str
    log_text: str
    def __init__(self, task_id: _Optional[int] = ..., log_type: _Optional[str] = ..., log_text: _Optional[str] = ...) -> None: ...

class GetLogsRequest(_message.Message):
    __slots__ = ("taskIds", "chunkSize", "skipFromEnd", "log_type")
    TASKIDS_FIELD_NUMBER: _ClassVar[int]
    CHUNKSIZE_FIELD_NUMBER: _ClassVar[int]
    SKIPFROMEND_FIELD_NUMBER: _ClassVar[int]
    LOG_TYPE_FIELD_NUMBER: _ClassVar[int]
    taskIds: _containers.RepeatedScalarFieldContainer[int]
    chunkSize: int
    skipFromEnd: int
    log_type: str
    def __init__(self, taskIds: _Optional[_Iterable[int]] = ..., chunkSize: _Optional[int] = ..., skipFromEnd: _Optional[int] = ..., log_type: _Optional[str] = ...) -> None: ...

class LogChunk(_message.Message):
    __slots__ = ("taskId", "stdout", "stderr")
    TASKID_FIELD_NUMBER: _ClassVar[int]
    STDOUT_FIELD_NUMBER: _ClassVar[int]
    STDERR_FIELD_NUMBER: _ClassVar[int]
    taskId: int
    stdout: _containers.RepeatedScalarFieldContainer[str]
    stderr: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, taskId: _Optional[int] = ..., stdout: _Optional[_Iterable[str]] = ..., stderr: _Optional[_Iterable[str]] = ...) -> None: ...

class LogChunkList(_message.Message):
    __slots__ = ("logs",)
    LOGS_FIELD_NUMBER: _ClassVar[int]
    logs: _containers.RepeatedCompositeFieldContainer[LogChunk]
    def __init__(self, logs: _Optional[_Iterable[_Union[LogChunk, _Mapping]]] = ...) -> None: ...

class TaskIds(_message.Message):
    __slots__ = ("task_ids",)
    TASK_IDS_FIELD_NUMBER: _ClassVar[int]
    task_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, task_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class TaskId(_message.Message):
    __slots__ = ("task_id",)
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    task_id: int
    def __init__(self, task_id: _Optional[int] = ...) -> None: ...

class WorkerId(_message.Message):
    __slots__ = ("worker_id",)
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    def __init__(self, worker_id: _Optional[int] = ...) -> None: ...

class WorkerDeletion(_message.Message):
    __slots__ = ("worker_id", "undeployed")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    UNDEPLOYED_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    undeployed: bool
    def __init__(self, worker_id: _Optional[int] = ..., undeployed: bool = ...) -> None: ...

class WorkerStatusRequest(_message.Message):
    __slots__ = ("worker_ids",)
    WORKER_IDS_FIELD_NUMBER: _ClassVar[int]
    worker_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, worker_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class WorkerStatus(_message.Message):
    __slots__ = ("worker_id", "status")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    status: str
    def __init__(self, worker_id: _Optional[int] = ..., status: _Optional[str] = ...) -> None: ...

class WorkerStatusResponse(_message.Message):
    __slots__ = ("statuses",)
    STATUSES_FIELD_NUMBER: _ClassVar[int]
    statuses: _containers.RepeatedCompositeFieldContainer[WorkerStatus]
    def __init__(self, statuses: _Optional[_Iterable[_Union[WorkerStatus, _Mapping]]] = ...) -> None: ...

class WorkerDetails(_message.Message):
    __slots__ = ("worker_id", "worker_name", "job_id")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    worker_name: str
    job_id: int
    def __init__(self, worker_id: _Optional[int] = ..., worker_name: _Optional[str] = ..., job_id: _Optional[int] = ...) -> None: ...

class WorkerIds(_message.Message):
    __slots__ = ("workers_details",)
    WORKERS_DETAILS_FIELD_NUMBER: _ClassVar[int]
    workers_details: _containers.RepeatedCompositeFieldContainer[WorkerDetails]
    def __init__(self, workers_details: _Optional[_Iterable[_Union[WorkerDetails, _Mapping]]] = ...) -> None: ...

class PingAndGetNewTasksRequest(_message.Message):
    __slots__ = ("worker_id", "stats")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    STATS_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    stats: WorkerStats
    def __init__(self, worker_id: _Optional[int] = ..., stats: _Optional[_Union[WorkerStats, _Mapping]] = ...) -> None: ...

class Ack(_message.Message):
    __slots__ = ("success",)
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    def __init__(self, success: bool = ...) -> None: ...

class ListTasksRequest(_message.Message):
    __slots__ = ("status_filter", "worker_id_filter", "workflow_id_filter", "step_id_filter", "command_filter", "limit", "offset", "show_hidden", "compact_command", "compact_command_max")
    STATUS_FILTER_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FILTER_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FILTER_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FILTER_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FILTER_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    SHOW_HIDDEN_FIELD_NUMBER: _ClassVar[int]
    COMPACT_COMMAND_FIELD_NUMBER: _ClassVar[int]
    COMPACT_COMMAND_MAX_FIELD_NUMBER: _ClassVar[int]
    status_filter: str
    worker_id_filter: int
    workflow_id_filter: int
    step_id_filter: int
    command_filter: str
    limit: int
    offset: int
    show_hidden: bool
    compact_command: bool
    compact_command_max: int
    def __init__(self, status_filter: _Optional[str] = ..., worker_id_filter: _Optional[int] = ..., workflow_id_filter: _Optional[int] = ..., step_id_filter: _Optional[int] = ..., command_filter: _Optional[str] = ..., limit: _Optional[int] = ..., offset: _Optional[int] = ..., show_hidden: bool = ..., compact_command: bool = ..., compact_command_max: _Optional[int] = ...) -> None: ...

class WorkerRequest(_message.Message):
    __slots__ = ("provider_id", "flavor_id", "region_id", "number", "concurrency", "prefetch", "step_id")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_ID_FIELD_NUMBER: _ClassVar[int]
    REGION_ID_FIELD_NUMBER: _ClassVar[int]
    NUMBER_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    provider_id: int
    flavor_id: int
    region_id: int
    number: int
    concurrency: int
    prefetch: int
    step_id: int
    def __init__(self, provider_id: _Optional[int] = ..., flavor_id: _Optional[int] = ..., region_id: _Optional[int] = ..., number: _Optional[int] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., step_id: _Optional[int] = ...) -> None: ...

class CreateWorkerByNameRequest(_message.Message):
    __slots__ = ("provider", "flavor", "region", "count", "concurrency", "prefetch", "step_id")
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    provider: str
    flavor: str
    region: str
    count: int
    concurrency: int
    prefetch: int
    step_id: int
    def __init__(self, provider: _Optional[str] = ..., flavor: _Optional[str] = ..., region: _Optional[str] = ..., count: _Optional[int] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., step_id: _Optional[int] = ...) -> None: ...

class WorkerUpdateRequest(_message.Message):
    __slots__ = ("worker_id", "provider_id", "flavor_id", "region_id", "concurrency", "prefetch", "step_id", "is_permanent", "recyclable_scope", "workflow_name", "step_name")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_ID_FIELD_NUMBER: _ClassVar[int]
    REGION_ID_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    IS_PERMANENT_FIELD_NUMBER: _ClassVar[int]
    RECYCLABLE_SCOPE_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_NAME_FIELD_NUMBER: _ClassVar[int]
    STEP_NAME_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    provider_id: int
    flavor_id: int
    region_id: int
    concurrency: int
    prefetch: int
    step_id: int
    is_permanent: bool
    recyclable_scope: str
    workflow_name: str
    step_name: str
    def __init__(self, worker_id: _Optional[int] = ..., provider_id: _Optional[int] = ..., flavor_id: _Optional[int] = ..., region_id: _Optional[int] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., step_id: _Optional[int] = ..., is_permanent: bool = ..., recyclable_scope: _Optional[str] = ..., workflow_name: _Optional[str] = ..., step_name: _Optional[str] = ...) -> None: ...

class ListFlavorsRequest(_message.Message):
    __slots__ = ("limit", "filter")
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    FILTER_FIELD_NUMBER: _ClassVar[int]
    limit: int
    filter: str
    def __init__(self, limit: _Optional[int] = ..., filter: _Optional[str] = ...) -> None: ...

class Flavor(_message.Message):
    __slots__ = ("flavor_id", "flavor_name", "provider_id", "provider", "cpu", "mem", "disk", "bandwidth", "gpu", "gpumem", "has_gpu", "has_quick_disks", "region_id", "region", "eviction", "cost")
    FLAVOR_ID_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_NAME_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    CPU_FIELD_NUMBER: _ClassVar[int]
    MEM_FIELD_NUMBER: _ClassVar[int]
    DISK_FIELD_NUMBER: _ClassVar[int]
    BANDWIDTH_FIELD_NUMBER: _ClassVar[int]
    GPU_FIELD_NUMBER: _ClassVar[int]
    GPUMEM_FIELD_NUMBER: _ClassVar[int]
    HAS_GPU_FIELD_NUMBER: _ClassVar[int]
    HAS_QUICK_DISKS_FIELD_NUMBER: _ClassVar[int]
    REGION_ID_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    EVICTION_FIELD_NUMBER: _ClassVar[int]
    COST_FIELD_NUMBER: _ClassVar[int]
    flavor_id: int
    flavor_name: str
    provider_id: int
    provider: str
    cpu: int
    mem: float
    disk: float
    bandwidth: int
    gpu: str
    gpumem: int
    has_gpu: bool
    has_quick_disks: bool
    region_id: int
    region: str
    eviction: float
    cost: float
    def __init__(self, flavor_id: _Optional[int] = ..., flavor_name: _Optional[str] = ..., provider_id: _Optional[int] = ..., provider: _Optional[str] = ..., cpu: _Optional[int] = ..., mem: _Optional[float] = ..., disk: _Optional[float] = ..., bandwidth: _Optional[int] = ..., gpu: _Optional[str] = ..., gpumem: _Optional[int] = ..., has_gpu: bool = ..., has_quick_disks: bool = ..., region_id: _Optional[int] = ..., region: _Optional[str] = ..., eviction: _Optional[float] = ..., cost: _Optional[float] = ...) -> None: ...

class FlavorsList(_message.Message):
    __slots__ = ("flavors",)
    FLAVORS_FIELD_NUMBER: _ClassVar[int]
    flavors: _containers.RepeatedCompositeFieldContainer[Flavor]
    def __init__(self, flavors: _Optional[_Iterable[_Union[Flavor, _Mapping]]] = ...) -> None: ...

class ListJobsRequest(_message.Message):
    __slots__ = ("limit", "offset")
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    limit: int
    offset: int
    def __init__(self, limit: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class Job(_message.Message):
    __slots__ = ("job_id", "status", "flavor_id", "retry", "worker_id", "action", "created_at", "modified_at", "progression", "log", "worker_name", "flavor_info")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_ID_FIELD_NUMBER: _ClassVar[int]
    RETRY_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    MODIFIED_AT_FIELD_NUMBER: _ClassVar[int]
    PROGRESSION_FIELD_NUMBER: _ClassVar[int]
    LOG_FIELD_NUMBER: _ClassVar[int]
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_INFO_FIELD_NUMBER: _ClassVar[int]
    job_id: int
    status: str
    flavor_id: int
    retry: int
    worker_id: int
    action: str
    created_at: str
    modified_at: str
    progression: int
    log: str
    worker_name: str
    flavor_info: str
    def __init__(self, job_id: _Optional[int] = ..., status: _Optional[str] = ..., flavor_id: _Optional[int] = ..., retry: _Optional[int] = ..., worker_id: _Optional[int] = ..., action: _Optional[str] = ..., created_at: _Optional[str] = ..., modified_at: _Optional[str] = ..., progression: _Optional[int] = ..., log: _Optional[str] = ..., worker_name: _Optional[str] = ..., flavor_info: _Optional[str] = ...) -> None: ...

class JobId(_message.Message):
    __slots__ = ("job_id",)
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    job_id: int
    def __init__(self, job_id: _Optional[int] = ...) -> None: ...

class JobsList(_message.Message):
    __slots__ = ("jobs",)
    JOBS_FIELD_NUMBER: _ClassVar[int]
    jobs: _containers.RepeatedCompositeFieldContainer[Job]
    def __init__(self, jobs: _Optional[_Iterable[_Union[Job, _Mapping]]] = ...) -> None: ...

class JobStatusRequest(_message.Message):
    __slots__ = ("job_ids",)
    JOB_IDS_FIELD_NUMBER: _ClassVar[int]
    job_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, job_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class JobStatus(_message.Message):
    __slots__ = ("job_id", "status", "progression")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PROGRESSION_FIELD_NUMBER: _ClassVar[int]
    job_id: int
    status: str
    progression: int
    def __init__(self, job_id: _Optional[int] = ..., status: _Optional[str] = ..., progression: _Optional[int] = ...) -> None: ...

class JobStatusResponse(_message.Message):
    __slots__ = ("statuses",)
    STATUSES_FIELD_NUMBER: _ClassVar[int]
    statuses: _containers.RepeatedCompositeFieldContainer[JobStatus]
    def __init__(self, statuses: _Optional[_Iterable[_Union[JobStatus, _Mapping]]] = ...) -> None: ...

class JobUpdate(_message.Message):
    __slots__ = ("job_id", "status", "append_log", "progression")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    APPEND_LOG_FIELD_NUMBER: _ClassVar[int]
    PROGRESSION_FIELD_NUMBER: _ClassVar[int]
    job_id: int
    status: str
    append_log: str
    progression: int
    def __init__(self, job_id: _Optional[int] = ..., status: _Optional[str] = ..., append_log: _Optional[str] = ..., progression: _Optional[int] = ...) -> None: ...

class RcloneRemotes(_message.Message):
    __slots__ = ("remotes",)
    class RemotesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: RcloneRemote
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[RcloneRemote, _Mapping]] = ...) -> None: ...
    REMOTES_FIELD_NUMBER: _ClassVar[int]
    remotes: _containers.MessageMap[str, RcloneRemote]
    def __init__(self, remotes: _Optional[_Mapping[str, RcloneRemote]] = ...) -> None: ...

class RcloneRemote(_message.Message):
    __slots__ = ("options",)
    class OptionsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    options: _containers.ScalarMap[str, str]
    def __init__(self, options: _Optional[_Mapping[str, str]] = ...) -> None: ...

class DockerCredential(_message.Message):
    __slots__ = ("registry", "auth")
    REGISTRY_FIELD_NUMBER: _ClassVar[int]
    AUTH_FIELD_NUMBER: _ClassVar[int]
    registry: str
    auth: str
    def __init__(self, registry: _Optional[str] = ..., auth: _Optional[str] = ...) -> None: ...

class DockerCredentials(_message.Message):
    __slots__ = ("credentials",)
    CREDENTIALS_FIELD_NUMBER: _ClassVar[int]
    credentials: _containers.RepeatedCompositeFieldContainer[DockerCredential]
    def __init__(self, credentials: _Optional[_Iterable[_Union[DockerCredential, _Mapping]]] = ...) -> None: ...

class LoginRequest(_message.Message):
    __slots__ = ("username", "password")
    USERNAME_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    username: str
    password: str
    def __init__(self, username: _Optional[str] = ..., password: _Optional[str] = ...) -> None: ...

class LoginResponse(_message.Message):
    __slots__ = ("token",)
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    token: str
    def __init__(self, token: _Optional[str] = ...) -> None: ...

class Certificate(_message.Message):
    __slots__ = ("pem",)
    PEM_FIELD_NUMBER: _ClassVar[int]
    pem: str
    def __init__(self, pem: _Optional[str] = ...) -> None: ...

class Token(_message.Message):
    __slots__ = ("token",)
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    token: str
    def __init__(self, token: _Optional[str] = ...) -> None: ...

class CreateUserRequest(_message.Message):
    __slots__ = ("username", "password", "email", "is_admin")
    USERNAME_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    EMAIL_FIELD_NUMBER: _ClassVar[int]
    IS_ADMIN_FIELD_NUMBER: _ClassVar[int]
    username: str
    password: str
    email: str
    is_admin: bool
    def __init__(self, username: _Optional[str] = ..., password: _Optional[str] = ..., email: _Optional[str] = ..., is_admin: bool = ...) -> None: ...

class UserId(_message.Message):
    __slots__ = ("user_id",)
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    user_id: int
    def __init__(self, user_id: _Optional[int] = ...) -> None: ...

class User(_message.Message):
    __slots__ = ("user_id", "username", "email", "is_admin")
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    USERNAME_FIELD_NUMBER: _ClassVar[int]
    EMAIL_FIELD_NUMBER: _ClassVar[int]
    IS_ADMIN_FIELD_NUMBER: _ClassVar[int]
    user_id: int
    username: str
    email: str
    is_admin: bool
    def __init__(self, user_id: _Optional[int] = ..., username: _Optional[str] = ..., email: _Optional[str] = ..., is_admin: bool = ...) -> None: ...

class AdminResetPasswordRequest(_message.Message):
    __slots__ = ("user_id", "new_password")
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    NEW_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    user_id: int
    new_password: str
    def __init__(self, user_id: _Optional[int] = ..., new_password: _Optional[str] = ...) -> None: ...

class UsersList(_message.Message):
    __slots__ = ("users",)
    USERS_FIELD_NUMBER: _ClassVar[int]
    users: _containers.RepeatedCompositeFieldContainer[User]
    def __init__(self, users: _Optional[_Iterable[_Union[User, _Mapping]]] = ...) -> None: ...

class ChangePasswordRequest(_message.Message):
    __slots__ = ("username", "old_password", "new_password")
    USERNAME_FIELD_NUMBER: _ClassVar[int]
    OLD_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    NEW_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    username: str
    old_password: str
    new_password: str
    def __init__(self, username: _Optional[str] = ..., old_password: _Optional[str] = ..., new_password: _Optional[str] = ...) -> None: ...

class RecruiterFilter(_message.Message):
    __slots__ = ("step_id",)
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    def __init__(self, step_id: _Optional[int] = ...) -> None: ...

class RecruiterId(_message.Message):
    __slots__ = ("step_id", "rank")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    RANK_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    rank: int
    def __init__(self, step_id: _Optional[int] = ..., rank: _Optional[int] = ...) -> None: ...

class Recruiter(_message.Message):
    __slots__ = ("step_id", "rank", "protofilter", "concurrency", "prefetch", "max_workers", "rounds", "timeout", "cpu_per_task", "memory_per_task", "disk_per_task", "prefetch_percent", "concurrency_min", "concurrency_max")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    RANK_FIELD_NUMBER: _ClassVar[int]
    PROTOFILTER_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    MAX_WORKERS_FIELD_NUMBER: _ClassVar[int]
    ROUNDS_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    CPU_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    MEMORY_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    DISK_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_PERCENT_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_MIN_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_MAX_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    rank: int
    protofilter: str
    concurrency: int
    prefetch: int
    max_workers: int
    rounds: int
    timeout: int
    cpu_per_task: int
    memory_per_task: float
    disk_per_task: float
    prefetch_percent: int
    concurrency_min: int
    concurrency_max: int
    def __init__(self, step_id: _Optional[int] = ..., rank: _Optional[int] = ..., protofilter: _Optional[str] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., max_workers: _Optional[int] = ..., rounds: _Optional[int] = ..., timeout: _Optional[int] = ..., cpu_per_task: _Optional[int] = ..., memory_per_task: _Optional[float] = ..., disk_per_task: _Optional[float] = ..., prefetch_percent: _Optional[int] = ..., concurrency_min: _Optional[int] = ..., concurrency_max: _Optional[int] = ...) -> None: ...

class RecruiterUpdate(_message.Message):
    __slots__ = ("step_id", "rank", "protofilter", "concurrency", "prefetch", "max_workers", "rounds", "timeout", "cpu_per_task", "memory_per_task", "disk_per_task", "prefetch_percent", "concurrency_min", "concurrency_max")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    RANK_FIELD_NUMBER: _ClassVar[int]
    PROTOFILTER_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_FIELD_NUMBER: _ClassVar[int]
    MAX_WORKERS_FIELD_NUMBER: _ClassVar[int]
    ROUNDS_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    CPU_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    MEMORY_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    DISK_PER_TASK_FIELD_NUMBER: _ClassVar[int]
    PREFETCH_PERCENT_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_MIN_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_MAX_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    rank: int
    protofilter: str
    concurrency: int
    prefetch: int
    max_workers: int
    rounds: int
    timeout: int
    cpu_per_task: int
    memory_per_task: float
    disk_per_task: float
    prefetch_percent: int
    concurrency_min: int
    concurrency_max: int
    def __init__(self, step_id: _Optional[int] = ..., rank: _Optional[int] = ..., protofilter: _Optional[str] = ..., concurrency: _Optional[int] = ..., prefetch: _Optional[int] = ..., max_workers: _Optional[int] = ..., rounds: _Optional[int] = ..., timeout: _Optional[int] = ..., cpu_per_task: _Optional[int] = ..., memory_per_task: _Optional[float] = ..., disk_per_task: _Optional[float] = ..., prefetch_percent: _Optional[int] = ..., concurrency_min: _Optional[int] = ..., concurrency_max: _Optional[int] = ...) -> None: ...

class RecruiterList(_message.Message):
    __slots__ = ("recruiters",)
    RECRUITERS_FIELD_NUMBER: _ClassVar[int]
    recruiters: _containers.RepeatedCompositeFieldContainer[Recruiter]
    def __init__(self, recruiters: _Optional[_Iterable[_Union[Recruiter, _Mapping]]] = ...) -> None: ...

class WorkflowFilter(_message.Message):
    __slots__ = ("name_like", "limit", "offset")
    NAME_LIKE_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    name_like: str
    limit: int
    offset: int
    def __init__(self, name_like: _Optional[str] = ..., limit: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class WorkflowId(_message.Message):
    __slots__ = ("workflow_id",)
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    def __init__(self, workflow_id: _Optional[int] = ...) -> None: ...

class Workflow(_message.Message):
    __slots__ = ("workflow_id", "name", "status", "run_strategy", "maximum_workers", "total_tasks", "succeeded_tasks", "failed_tasks", "running_tasks", "retrying_tasks", "live", "template_run_id", "template_name", "template_version", "script_name", "script_sha256")
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    RUN_STRATEGY_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_WORKERS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_TASKS_FIELD_NUMBER: _ClassVar[int]
    SUCCEEDED_TASKS_FIELD_NUMBER: _ClassVar[int]
    FAILED_TASKS_FIELD_NUMBER: _ClassVar[int]
    RUNNING_TASKS_FIELD_NUMBER: _ClassVar[int]
    RETRYING_TASKS_FIELD_NUMBER: _ClassVar[int]
    LIVE_FIELD_NUMBER: _ClassVar[int]
    TEMPLATE_RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TEMPLATE_NAME_FIELD_NUMBER: _ClassVar[int]
    TEMPLATE_VERSION_FIELD_NUMBER: _ClassVar[int]
    SCRIPT_NAME_FIELD_NUMBER: _ClassVar[int]
    SCRIPT_SHA256_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    name: str
    status: str
    run_strategy: str
    maximum_workers: int
    total_tasks: int
    succeeded_tasks: int
    failed_tasks: int
    running_tasks: int
    retrying_tasks: int
    live: bool
    template_run_id: int
    template_name: str
    template_version: str
    script_name: str
    script_sha256: str
    def __init__(self, workflow_id: _Optional[int] = ..., name: _Optional[str] = ..., status: _Optional[str] = ..., run_strategy: _Optional[str] = ..., maximum_workers: _Optional[int] = ..., total_tasks: _Optional[int] = ..., succeeded_tasks: _Optional[int] = ..., failed_tasks: _Optional[int] = ..., running_tasks: _Optional[int] = ..., retrying_tasks: _Optional[int] = ..., live: bool = ..., template_run_id: _Optional[int] = ..., template_name: _Optional[str] = ..., template_version: _Optional[str] = ..., script_name: _Optional[str] = ..., script_sha256: _Optional[str] = ...) -> None: ...

class WorkflowRequest(_message.Message):
    __slots__ = ("name", "run_strategy", "maximum_workers", "status", "live")
    NAME_FIELD_NUMBER: _ClassVar[int]
    RUN_STRATEGY_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_WORKERS_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    LIVE_FIELD_NUMBER: _ClassVar[int]
    name: str
    run_strategy: str
    maximum_workers: int
    status: str
    live: bool
    def __init__(self, name: _Optional[str] = ..., run_strategy: _Optional[str] = ..., maximum_workers: _Optional[int] = ..., status: _Optional[str] = ..., live: bool = ...) -> None: ...

class WorkflowList(_message.Message):
    __slots__ = ("workflows",)
    WORKFLOWS_FIELD_NUMBER: _ClassVar[int]
    workflows: _containers.RepeatedCompositeFieldContainer[Workflow]
    def __init__(self, workflows: _Optional[_Iterable[_Union[Workflow, _Mapping]]] = ...) -> None: ...

class WorkflowStatusUpdate(_message.Message):
    __slots__ = ("workflow_id", "status", "maximum_workers")
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    MAXIMUM_WORKERS_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    status: str
    maximum_workers: int
    def __init__(self, workflow_id: _Optional[int] = ..., status: _Optional[str] = ..., maximum_workers: _Optional[int] = ...) -> None: ...

class DebugAssignRequest(_message.Message):
    __slots__ = ("workflow_id", "task_id")
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    task_id: int
    def __init__(self, workflow_id: _Optional[int] = ..., task_id: _Optional[int] = ...) -> None: ...

class DebugRecruitRequest(_message.Message):
    __slots__ = ("workflow_id", "step_id")
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    step_id: int
    def __init__(self, workflow_id: _Optional[int] = ..., step_id: _Optional[int] = ...) -> None: ...

class StepFilter(_message.Message):
    __slots__ = ("WorkflowId", "limit", "offset")
    WORKFLOWID_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    WorkflowId: int
    limit: int
    offset: int
    def __init__(self, WorkflowId: _Optional[int] = ..., limit: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class StepId(_message.Message):
    __slots__ = ("step_id",)
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    def __init__(self, step_id: _Optional[int] = ...) -> None: ...

class Step(_message.Message):
    __slots__ = ("step_id", "workflow_name", "workflow_id", "name", "quality_definition")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_NAME_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    QUALITY_DEFINITION_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    workflow_name: str
    workflow_id: int
    name: str
    quality_definition: str
    def __init__(self, step_id: _Optional[int] = ..., workflow_name: _Optional[str] = ..., workflow_id: _Optional[int] = ..., name: _Optional[str] = ..., quality_definition: _Optional[str] = ...) -> None: ...

class StepRequest(_message.Message):
    __slots__ = ("workflow_name", "workflow_id", "name", "quality_definition")
    WORKFLOW_NAME_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    QUALITY_DEFINITION_FIELD_NUMBER: _ClassVar[int]
    workflow_name: str
    workflow_id: int
    name: str
    quality_definition: str
    def __init__(self, workflow_name: _Optional[str] = ..., workflow_id: _Optional[int] = ..., name: _Optional[str] = ..., quality_definition: _Optional[str] = ...) -> None: ...

class StepList(_message.Message):
    __slots__ = ("steps",)
    STEPS_FIELD_NUMBER: _ClassVar[int]
    steps: _containers.RepeatedCompositeFieldContainer[Step]
    def __init__(self, steps: _Optional[_Iterable[_Union[Step, _Mapping]]] = ...) -> None: ...

class StepStatsRequest(_message.Message):
    __slots__ = ("workflow_id", "step_ids")
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_IDS_FIELD_NUMBER: _ClassVar[int]
    workflow_id: int
    step_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, workflow_id: _Optional[int] = ..., step_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class Accum(_message.Message):
    __slots__ = ("count", "sum", "min", "max")
    COUNT_FIELD_NUMBER: _ClassVar[int]
    SUM_FIELD_NUMBER: _ClassVar[int]
    MIN_FIELD_NUMBER: _ClassVar[int]
    MAX_FIELD_NUMBER: _ClassVar[int]
    count: int
    sum: float
    min: float
    max: float
    def __init__(self, count: _Optional[int] = ..., sum: _Optional[float] = ..., min: _Optional[float] = ..., max: _Optional[float] = ...) -> None: ...

class StepStats(_message.Message):
    __slots__ = ("step_id", "step_name", "total_tasks", "waiting_tasks", "pending_tasks", "accepted_tasks", "running_tasks", "uploading_tasks", "successful_tasks", "failed_tasks", "really_failed_tasks", "success_run", "failed_run", "running_run", "download", "upload", "start_time", "end_time", "stats_eval_time")
    STEP_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_NAME_FIELD_NUMBER: _ClassVar[int]
    TOTAL_TASKS_FIELD_NUMBER: _ClassVar[int]
    WAITING_TASKS_FIELD_NUMBER: _ClassVar[int]
    PENDING_TASKS_FIELD_NUMBER: _ClassVar[int]
    ACCEPTED_TASKS_FIELD_NUMBER: _ClassVar[int]
    RUNNING_TASKS_FIELD_NUMBER: _ClassVar[int]
    UPLOADING_TASKS_FIELD_NUMBER: _ClassVar[int]
    SUCCESSFUL_TASKS_FIELD_NUMBER: _ClassVar[int]
    FAILED_TASKS_FIELD_NUMBER: _ClassVar[int]
    REALLY_FAILED_TASKS_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_RUN_FIELD_NUMBER: _ClassVar[int]
    FAILED_RUN_FIELD_NUMBER: _ClassVar[int]
    RUNNING_RUN_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_FIELD_NUMBER: _ClassVar[int]
    START_TIME_FIELD_NUMBER: _ClassVar[int]
    END_TIME_FIELD_NUMBER: _ClassVar[int]
    STATS_EVAL_TIME_FIELD_NUMBER: _ClassVar[int]
    step_id: int
    step_name: str
    total_tasks: int
    waiting_tasks: int
    pending_tasks: int
    accepted_tasks: int
    running_tasks: int
    uploading_tasks: int
    successful_tasks: int
    failed_tasks: int
    really_failed_tasks: int
    success_run: Accum
    failed_run: Accum
    running_run: Accum
    download: Accum
    upload: Accum
    start_time: int
    end_time: int
    stats_eval_time: int
    def __init__(self, step_id: _Optional[int] = ..., step_name: _Optional[str] = ..., total_tasks: _Optional[int] = ..., waiting_tasks: _Optional[int] = ..., pending_tasks: _Optional[int] = ..., accepted_tasks: _Optional[int] = ..., running_tasks: _Optional[int] = ..., uploading_tasks: _Optional[int] = ..., successful_tasks: _Optional[int] = ..., failed_tasks: _Optional[int] = ..., really_failed_tasks: _Optional[int] = ..., success_run: _Optional[_Union[Accum, _Mapping]] = ..., failed_run: _Optional[_Union[Accum, _Mapping]] = ..., running_run: _Optional[_Union[Accum, _Mapping]] = ..., download: _Optional[_Union[Accum, _Mapping]] = ..., upload: _Optional[_Union[Accum, _Mapping]] = ..., start_time: _Optional[int] = ..., end_time: _Optional[int] = ..., stats_eval_time: _Optional[int] = ...) -> None: ...

class StepStatsResponse(_message.Message):
    __slots__ = ("stats",)
    STATS_FIELD_NUMBER: _ClassVar[int]
    stats: _containers.RepeatedCompositeFieldContainer[StepStats]
    def __init__(self, stats: _Optional[_Iterable[_Union[StepStats, _Mapping]]] = ...) -> None: ...

class WorkerStats(_message.Message):
    __slots__ = ("cpu_usage_percent", "mem_usage_percent", "load_1min", "iowait_percent", "disks", "disk_io", "net_io", "num_cpus")
    CPU_USAGE_PERCENT_FIELD_NUMBER: _ClassVar[int]
    MEM_USAGE_PERCENT_FIELD_NUMBER: _ClassVar[int]
    LOAD_1MIN_FIELD_NUMBER: _ClassVar[int]
    IOWAIT_PERCENT_FIELD_NUMBER: _ClassVar[int]
    DISKS_FIELD_NUMBER: _ClassVar[int]
    DISK_IO_FIELD_NUMBER: _ClassVar[int]
    NET_IO_FIELD_NUMBER: _ClassVar[int]
    NUM_CPUS_FIELD_NUMBER: _ClassVar[int]
    cpu_usage_percent: float
    mem_usage_percent: float
    load_1min: float
    iowait_percent: float
    disks: _containers.RepeatedCompositeFieldContainer[DiskUsage]
    disk_io: DiskIOStats
    net_io: NetIOStats
    num_cpus: int
    def __init__(self, cpu_usage_percent: _Optional[float] = ..., mem_usage_percent: _Optional[float] = ..., load_1min: _Optional[float] = ..., iowait_percent: _Optional[float] = ..., disks: _Optional[_Iterable[_Union[DiskUsage, _Mapping]]] = ..., disk_io: _Optional[_Union[DiskIOStats, _Mapping]] = ..., net_io: _Optional[_Union[NetIOStats, _Mapping]] = ..., num_cpus: _Optional[int] = ...) -> None: ...

class DiskUsage(_message.Message):
    __slots__ = ("device_name", "usage_percent")
    DEVICE_NAME_FIELD_NUMBER: _ClassVar[int]
    USAGE_PERCENT_FIELD_NUMBER: _ClassVar[int]
    device_name: str
    usage_percent: float
    def __init__(self, device_name: _Optional[str] = ..., usage_percent: _Optional[float] = ...) -> None: ...

class DiskIOStats(_message.Message):
    __slots__ = ("read_bytes_total", "write_bytes_total", "read_bytes_rate", "write_bytes_rate")
    READ_BYTES_TOTAL_FIELD_NUMBER: _ClassVar[int]
    WRITE_BYTES_TOTAL_FIELD_NUMBER: _ClassVar[int]
    READ_BYTES_RATE_FIELD_NUMBER: _ClassVar[int]
    WRITE_BYTES_RATE_FIELD_NUMBER: _ClassVar[int]
    read_bytes_total: int
    write_bytes_total: int
    read_bytes_rate: float
    write_bytes_rate: float
    def __init__(self, read_bytes_total: _Optional[int] = ..., write_bytes_total: _Optional[int] = ..., read_bytes_rate: _Optional[float] = ..., write_bytes_rate: _Optional[float] = ...) -> None: ...

class NetIOStats(_message.Message):
    __slots__ = ("recv_bytes_total", "sent_bytes_total", "recv_bytes_rate", "sent_bytes_rate")
    RECV_BYTES_TOTAL_FIELD_NUMBER: _ClassVar[int]
    SENT_BYTES_TOTAL_FIELD_NUMBER: _ClassVar[int]
    RECV_BYTES_RATE_FIELD_NUMBER: _ClassVar[int]
    SENT_BYTES_RATE_FIELD_NUMBER: _ClassVar[int]
    recv_bytes_total: int
    sent_bytes_total: int
    recv_bytes_rate: float
    sent_bytes_rate: float
    def __init__(self, recv_bytes_total: _Optional[int] = ..., sent_bytes_total: _Optional[int] = ..., recv_bytes_rate: _Optional[float] = ..., sent_bytes_rate: _Optional[float] = ...) -> None: ...

class GetWorkerStatsRequest(_message.Message):
    __slots__ = ("worker_ids",)
    WORKER_IDS_FIELD_NUMBER: _ClassVar[int]
    worker_ids: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, worker_ids: _Optional[_Iterable[int]] = ...) -> None: ...

class GetWorkerStatsResponse(_message.Message):
    __slots__ = ("worker_stats",)
    class WorkerStatsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: int
        value: WorkerStats
        def __init__(self, key: _Optional[int] = ..., value: _Optional[_Union[WorkerStats, _Mapping]] = ...) -> None: ...
    WORKER_STATS_FIELD_NUMBER: _ClassVar[int]
    worker_stats: _containers.MessageMap[int, WorkerStats]
    def __init__(self, worker_stats: _Optional[_Mapping[int, WorkerStats]] = ...) -> None: ...

class FetchListRequest(_message.Message):
    __slots__ = ("uri",)
    URI_FIELD_NUMBER: _ClassVar[int]
    uri: str
    def __init__(self, uri: _Optional[str] = ...) -> None: ...

class FetchListResponse(_message.Message):
    __slots__ = ("files",)
    FILES_FIELD_NUMBER: _ClassVar[int]
    files: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, files: _Optional[_Iterable[str]] = ...) -> None: ...

class FetchInfoResponse(_message.Message):
    __slots__ = ("uri", "filename", "description", "size", "is_file", "is_dir")
    URI_FIELD_NUMBER: _ClassVar[int]
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    SIZE_FIELD_NUMBER: _ClassVar[int]
    IS_FILE_FIELD_NUMBER: _ClassVar[int]
    IS_DIR_FIELD_NUMBER: _ClassVar[int]
    uri: str
    filename: str
    description: str
    size: int
    is_file: bool
    is_dir: bool
    def __init__(self, uri: _Optional[str] = ..., filename: _Optional[str] = ..., description: _Optional[str] = ..., size: _Optional[int] = ..., is_file: bool = ..., is_dir: bool = ...) -> None: ...

class UploadTemplateRequest(_message.Message):
    __slots__ = ("script", "force", "filename")
    SCRIPT_FIELD_NUMBER: _ClassVar[int]
    FORCE_FIELD_NUMBER: _ClassVar[int]
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    script: bytes
    force: bool
    filename: str
    def __init__(self, script: _Optional[bytes] = ..., force: bool = ..., filename: _Optional[str] = ...) -> None: ...

class UploadTemplateResponse(_message.Message):
    __slots__ = ("success", "message", "workflow_template_id", "name", "version", "description", "param_json")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAM_JSON_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    workflow_template_id: int
    name: str
    version: str
    description: str
    param_json: str
    def __init__(self, success: bool = ..., message: _Optional[str] = ..., workflow_template_id: _Optional[int] = ..., name: _Optional[str] = ..., version: _Optional[str] = ..., description: _Optional[str] = ..., param_json: _Optional[str] = ...) -> None: ...

class RunTemplateRequest(_message.Message):
    __slots__ = ("workflow_template_id", "param_values_json", "no_recruiters")
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    PARAM_VALUES_JSON_FIELD_NUMBER: _ClassVar[int]
    NO_RECRUITERS_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    param_values_json: str
    no_recruiters: bool
    def __init__(self, workflow_template_id: _Optional[int] = ..., param_values_json: _Optional[str] = ..., no_recruiters: bool = ...) -> None: ...

class TemplateFilter(_message.Message):
    __slots__ = ("workflow_template_id", "name", "version", "all_versions", "show_hidden")
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    ALL_VERSIONS_FIELD_NUMBER: _ClassVar[int]
    SHOW_HIDDEN_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    name: str
    version: str
    all_versions: bool
    show_hidden: bool
    def __init__(self, workflow_template_id: _Optional[int] = ..., name: _Optional[str] = ..., version: _Optional[str] = ..., all_versions: bool = ..., show_hidden: bool = ...) -> None: ...

class Template(_message.Message):
    __slots__ = ("workflow_template_id", "name", "version", "description", "param_json", "uploaded_at", "uploaded_by", "hidden", "version_count")
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAM_JSON_FIELD_NUMBER: _ClassVar[int]
    UPLOADED_AT_FIELD_NUMBER: _ClassVar[int]
    UPLOADED_BY_FIELD_NUMBER: _ClassVar[int]
    HIDDEN_FIELD_NUMBER: _ClassVar[int]
    VERSION_COUNT_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    name: str
    version: str
    description: str
    param_json: str
    uploaded_at: str
    uploaded_by: int
    hidden: bool
    version_count: int
    def __init__(self, workflow_template_id: _Optional[int] = ..., name: _Optional[str] = ..., version: _Optional[str] = ..., description: _Optional[str] = ..., param_json: _Optional[str] = ..., uploaded_at: _Optional[str] = ..., uploaded_by: _Optional[int] = ..., hidden: bool = ..., version_count: _Optional[int] = ...) -> None: ...

class UpdateTemplateRequest(_message.Message):
    __slots__ = ("workflow_template_id", "hidden")
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    HIDDEN_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    hidden: bool
    def __init__(self, workflow_template_id: _Optional[int] = ..., hidden: bool = ...) -> None: ...

class TemplateList(_message.Message):
    __slots__ = ("templates",)
    TEMPLATES_FIELD_NUMBER: _ClassVar[int]
    templates: _containers.RepeatedCompositeFieldContainer[Template]
    def __init__(self, templates: _Optional[_Iterable[_Union[Template, _Mapping]]] = ...) -> None: ...

class TemplateRun(_message.Message):
    __slots__ = ("template_run_id", "workflow_template_id", "template_name", "template_version", "workflow_name", "run_by", "status", "workflow_id", "created_at", "param_values_json", "error_message", "run_by_username", "script_name", "script_sha256")
    TEMPLATE_RUN_ID_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    TEMPLATE_NAME_FIELD_NUMBER: _ClassVar[int]
    TEMPLATE_VERSION_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_NAME_FIELD_NUMBER: _ClassVar[int]
    RUN_BY_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    PARAM_VALUES_JSON_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    RUN_BY_USERNAME_FIELD_NUMBER: _ClassVar[int]
    SCRIPT_NAME_FIELD_NUMBER: _ClassVar[int]
    SCRIPT_SHA256_FIELD_NUMBER: _ClassVar[int]
    template_run_id: int
    workflow_template_id: int
    template_name: str
    template_version: str
    workflow_name: str
    run_by: int
    status: str
    workflow_id: int
    created_at: str
    param_values_json: str
    error_message: str
    run_by_username: str
    script_name: str
    script_sha256: str
    def __init__(self, template_run_id: _Optional[int] = ..., workflow_template_id: _Optional[int] = ..., template_name: _Optional[str] = ..., template_version: _Optional[str] = ..., workflow_name: _Optional[str] = ..., run_by: _Optional[int] = ..., status: _Optional[str] = ..., workflow_id: _Optional[int] = ..., created_at: _Optional[str] = ..., param_values_json: _Optional[str] = ..., error_message: _Optional[str] = ..., run_by_username: _Optional[str] = ..., script_name: _Optional[str] = ..., script_sha256: _Optional[str] = ...) -> None: ...

class TemplateRunList(_message.Message):
    __slots__ = ("runs",)
    RUNS_FIELD_NUMBER: _ClassVar[int]
    runs: _containers.RepeatedCompositeFieldContainer[TemplateRun]
    def __init__(self, runs: _Optional[_Iterable[_Union[TemplateRun, _Mapping]]] = ...) -> None: ...

class TemplateRunFilter(_message.Message):
    __slots__ = ("workflow_template_id",)
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    def __init__(self, workflow_template_id: _Optional[int] = ...) -> None: ...

class UpdateTemplateRunRequest(_message.Message):
    __slots__ = ("template_run_id", "workflow_id", "error_message", "module_pins")
    TEMPLATE_RUN_ID_FIELD_NUMBER: _ClassVar[int]
    WORKFLOW_ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    MODULE_PINS_FIELD_NUMBER: _ClassVar[int]
    template_run_id: int
    workflow_id: int
    error_message: str
    module_pins: str
    def __init__(self, template_run_id: _Optional[int] = ..., workflow_id: _Optional[int] = ..., error_message: _Optional[str] = ..., module_pins: _Optional[str] = ...) -> None: ...

class WorkspaceRootRequest(_message.Message):
    __slots__ = ("provider", "region")
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    REGION_FIELD_NUMBER: _ClassVar[int]
    provider: str
    region: str
    def __init__(self, provider: _Optional[str] = ..., region: _Optional[str] = ...) -> None: ...

class WorkspaceRootResponse(_message.Message):
    __slots__ = ("root_uri",)
    ROOT_URI_FIELD_NUMBER: _ClassVar[int]
    root_uri: str
    def __init__(self, root_uri: _Optional[str] = ...) -> None: ...

class DeleteTemplateRunRequest(_message.Message):
    __slots__ = ("template_run_id",)
    TEMPLATE_RUN_ID_FIELD_NUMBER: _ClassVar[int]
    template_run_id: int
    def __init__(self, template_run_id: _Optional[int] = ...) -> None: ...

class RegisterAdhocRunRequest(_message.Message):
    __slots__ = ("script_name", "script_sha256", "param_values_json", "module_pins_json")
    SCRIPT_NAME_FIELD_NUMBER: _ClassVar[int]
    SCRIPT_SHA256_FIELD_NUMBER: _ClassVar[int]
    PARAM_VALUES_JSON_FIELD_NUMBER: _ClassVar[int]
    MODULE_PINS_JSON_FIELD_NUMBER: _ClassVar[int]
    script_name: str
    script_sha256: str
    param_values_json: str
    module_pins_json: str
    def __init__(self, script_name: _Optional[str] = ..., script_sha256: _Optional[str] = ..., param_values_json: _Optional[str] = ..., module_pins_json: _Optional[str] = ...) -> None: ...

class DownloadTemplateRequest(_message.Message):
    __slots__ = ("workflow_template_id", "name", "version")
    WORKFLOW_TEMPLATE_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    workflow_template_id: int
    name: str
    version: str
    def __init__(self, workflow_template_id: _Optional[int] = ..., name: _Optional[str] = ..., version: _Optional[str] = ...) -> None: ...

class UploadModuleRequest(_message.Message):
    __slots__ = ("filename", "content", "force")
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    FORCE_FIELD_NUMBER: _ClassVar[int]
    filename: str
    content: bytes
    force: bool
    def __init__(self, filename: _Optional[str] = ..., content: _Optional[bytes] = ..., force: bool = ...) -> None: ...

class ModuleList(_message.Message):
    __slots__ = ("modules", "entries")
    MODULES_FIELD_NUMBER: _ClassVar[int]
    ENTRIES_FIELD_NUMBER: _ClassVar[int]
    modules: _containers.RepeatedScalarFieldContainer[str]
    entries: _containers.RepeatedCompositeFieldContainer[ModuleEntry]
    def __init__(self, modules: _Optional[_Iterable[str]] = ..., entries: _Optional[_Iterable[_Union[ModuleEntry, _Mapping]]] = ...) -> None: ...

class ModuleEntry(_message.Message):
    __slots__ = ("path", "version", "origin", "description")
    PATH_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    path: str
    version: str
    origin: str
    description: str
    def __init__(self, path: _Optional[str] = ..., version: _Optional[str] = ..., origin: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class ModuleListFilter(_message.Message):
    __slots__ = ("path", "latest_only", "origin")
    PATH_FIELD_NUMBER: _ClassVar[int]
    LATEST_ONLY_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    path: str
    latest_only: bool
    origin: str
    def __init__(self, path: _Optional[str] = ..., latest_only: bool = ..., origin: _Optional[str] = ...) -> None: ...

class DownloadModuleRequest(_message.Message):
    __slots__ = ("filename",)
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    filename: str
    def __init__(self, filename: _Optional[str] = ...) -> None: ...

class UpgradeBundledModulesRequest(_message.Message):
    __slots__ = ("apply",)
    APPLY_FIELD_NUMBER: _ClassVar[int]
    apply: bool
    def __init__(self, apply: bool = ...) -> None: ...

class UpgradeBundledModulesResponse(_message.Message):
    __slots__ = ("report", "inserted", "forks_preserved", "conflicts", "skipped", "up_to_date")
    REPORT_FIELD_NUMBER: _ClassVar[int]
    INSERTED_FIELD_NUMBER: _ClassVar[int]
    FORKS_PRESERVED_FIELD_NUMBER: _ClassVar[int]
    CONFLICTS_FIELD_NUMBER: _ClassVar[int]
    SKIPPED_FIELD_NUMBER: _ClassVar[int]
    UP_TO_DATE_FIELD_NUMBER: _ClassVar[int]
    report: str
    inserted: int
    forks_preserved: int
    conflicts: int
    skipped: int
    up_to_date: int
    def __init__(self, report: _Optional[str] = ..., inserted: _Optional[int] = ..., forks_preserved: _Optional[int] = ..., conflicts: _Optional[int] = ..., skipped: _Optional[int] = ..., up_to_date: _Optional[int] = ...) -> None: ...

class ModuleOriginRequest(_message.Message):
    __slots__ = ("path", "version")
    PATH_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    path: str
    version: str
    def __init__(self, path: _Optional[str] = ..., version: _Optional[str] = ...) -> None: ...

class ModuleOriginResponse(_message.Message):
    __slots__ = ("path", "version", "origin", "content_sha256", "bundled_sha256", "description", "uploaded_at", "uploaded_by", "fork_is_outdated")
    PATH_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    CONTENT_SHA256_FIELD_NUMBER: _ClassVar[int]
    BUNDLED_SHA256_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    UPLOADED_AT_FIELD_NUMBER: _ClassVar[int]
    UPLOADED_BY_FIELD_NUMBER: _ClassVar[int]
    FORK_IS_OUTDATED_FIELD_NUMBER: _ClassVar[int]
    path: str
    version: str
    origin: str
    content_sha256: str
    bundled_sha256: str
    description: str
    uploaded_at: str
    uploaded_by: str
    fork_is_outdated: bool
    def __init__(self, path: _Optional[str] = ..., version: _Optional[str] = ..., origin: _Optional[str] = ..., content_sha256: _Optional[str] = ..., bundled_sha256: _Optional[str] = ..., description: _Optional[str] = ..., uploaded_at: _Optional[str] = ..., uploaded_by: _Optional[str] = ..., fork_is_outdated: bool = ...) -> None: ...

class ForkModuleRequest(_message.Message):
    __slots__ = ("source_path", "source_version", "new_version")
    SOURCE_PATH_FIELD_NUMBER: _ClassVar[int]
    SOURCE_VERSION_FIELD_NUMBER: _ClassVar[int]
    NEW_VERSION_FIELD_NUMBER: _ClassVar[int]
    source_path: str
    source_version: str
    new_version: str
    def __init__(self, source_path: _Optional[str] = ..., source_version: _Optional[str] = ..., new_version: _Optional[str] = ...) -> None: ...

class FileContent(_message.Message):
    __slots__ = ("filename", "content")
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    filename: str
    content: bytes
    def __init__(self, filename: _Optional[str] = ..., content: _Optional[bytes] = ...) -> None: ...

class ResourceSpec(_message.Message):
    __slots__ = ("worker_id", "cpu", "mem", "disk")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    CPU_FIELD_NUMBER: _ClassVar[int]
    MEM_FIELD_NUMBER: _ClassVar[int]
    DISK_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    cpu: int
    mem: float
    disk: float
    def __init__(self, worker_id: _Optional[str] = ..., cpu: _Optional[int] = ..., mem: _Optional[float] = ..., disk: _Optional[float] = ...) -> None: ...

class WorkerEvent(_message.Message):
    __slots__ = ("worker_id", "worker_name", "level", "event_class", "message", "details_json")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    EVENT_CLASS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    DETAILS_JSON_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    worker_name: str
    level: str
    event_class: str
    message: str
    details_json: str
    def __init__(self, worker_id: _Optional[int] = ..., worker_name: _Optional[str] = ..., level: _Optional[str] = ..., event_class: _Optional[str] = ..., message: _Optional[str] = ..., details_json: _Optional[str] = ...) -> None: ...

class WorkerEventFilter(_message.Message):
    __slots__ = ("worker_id", "level", "limit")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    CLASS_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    worker_id: int
    level: str
    limit: int
    def __init__(self, worker_id: _Optional[int] = ..., level: _Optional[str] = ..., limit: _Optional[int] = ..., **kwargs) -> None: ...

class WorkerEventRecord(_message.Message):
    __slots__ = ("event_id", "created_at", "worker_id", "worker_name", "level", "event_class", "message", "details_json")
    EVENT_ID_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    EVENT_CLASS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    DETAILS_JSON_FIELD_NUMBER: _ClassVar[int]
    event_id: int
    created_at: str
    worker_id: int
    worker_name: str
    level: str
    event_class: str
    message: str
    details_json: str
    def __init__(self, event_id: _Optional[int] = ..., created_at: _Optional[str] = ..., worker_id: _Optional[int] = ..., worker_name: _Optional[str] = ..., level: _Optional[str] = ..., event_class: _Optional[str] = ..., message: _Optional[str] = ..., details_json: _Optional[str] = ...) -> None: ...

class WorkerEventList(_message.Message):
    __slots__ = ("events",)
    EVENTS_FIELD_NUMBER: _ClassVar[int]
    events: _containers.RepeatedCompositeFieldContainer[WorkerEventRecord]
    def __init__(self, events: _Optional[_Iterable[_Union[WorkerEventRecord, _Mapping]]] = ...) -> None: ...

class WorkerEventId(_message.Message):
    __slots__ = ("event_id",)
    EVENT_ID_FIELD_NUMBER: _ClassVar[int]
    event_id: int
    def __init__(self, event_id: _Optional[int] = ...) -> None: ...

class WorkerEventPruneFilter(_message.Message):
    __slots__ = ("before", "level", "worker_id", "dry_run")
    BEFORE_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    CLASS_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    DRY_RUN_FIELD_NUMBER: _ClassVar[int]
    before: str
    level: str
    worker_id: int
    dry_run: bool
    def __init__(self, before: _Optional[str] = ..., level: _Optional[str] = ..., worker_id: _Optional[int] = ..., dry_run: bool = ..., **kwargs) -> None: ...

class WorkerEventPruneResult(_message.Message):
    __slots__ = ("matched", "deleted")
    MATCHED_FIELD_NUMBER: _ClassVar[int]
    DELETED_FIELD_NUMBER: _ClassVar[int]
    matched: int
    deleted: int
    def __init__(self, matched: _Optional[int] = ..., deleted: _Optional[int] = ...) -> None: ...

class Provider(_message.Message):
    __slots__ = ("provider_id", "provider_name", "config_name")
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_NAME_FIELD_NUMBER: _ClassVar[int]
    CONFIG_NAME_FIELD_NUMBER: _ClassVar[int]
    provider_id: int
    provider_name: str
    config_name: str
    def __init__(self, provider_id: _Optional[int] = ..., provider_name: _Optional[str] = ..., config_name: _Optional[str] = ...) -> None: ...

class ProviderList(_message.Message):
    __slots__ = ("providers",)
    PROVIDERS_FIELD_NUMBER: _ClassVar[int]
    providers: _containers.RepeatedCompositeFieldContainer[Provider]
    def __init__(self, providers: _Optional[_Iterable[_Union[Provider, _Mapping]]] = ...) -> None: ...

class Region(_message.Message):
    __slots__ = ("region_id", "provider_id", "region_name", "is_default")
    REGION_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_ID_FIELD_NUMBER: _ClassVar[int]
    REGION_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_DEFAULT_FIELD_NUMBER: _ClassVar[int]
    region_id: int
    provider_id: int
    region_name: str
    is_default: bool
    def __init__(self, region_id: _Optional[int] = ..., provider_id: _Optional[int] = ..., region_name: _Optional[str] = ..., is_default: bool = ...) -> None: ...

class RegionList(_message.Message):
    __slots__ = ("regions",)
    REGIONS_FIELD_NUMBER: _ClassVar[int]
    regions: _containers.RepeatedCompositeFieldContainer[Region]
    def __init__(self, regions: _Optional[_Iterable[_Union[Region, _Mapping]]] = ...) -> None: ...

class FlavorCreateRequest(_message.Message):
    __slots__ = ("provider_name", "config_name", "flavor_name", "cpu", "memory", "disk", "bandwidth", "gpu", "gpumem", "has_gpu", "has_quick_disks", "region_names", "costs", "evictions")
    PROVIDER_NAME_FIELD_NUMBER: _ClassVar[int]
    CONFIG_NAME_FIELD_NUMBER: _ClassVar[int]
    FLAVOR_NAME_FIELD_NUMBER: _ClassVar[int]
    CPU_FIELD_NUMBER: _ClassVar[int]
    MEMORY_FIELD_NUMBER: _ClassVar[int]
    DISK_FIELD_NUMBER: _ClassVar[int]
    BANDWIDTH_FIELD_NUMBER: _ClassVar[int]
    GPU_FIELD_NUMBER: _ClassVar[int]
    GPUMEM_FIELD_NUMBER: _ClassVar[int]
    HAS_GPU_FIELD_NUMBER: _ClassVar[int]
    HAS_QUICK_DISKS_FIELD_NUMBER: _ClassVar[int]
    REGION_NAMES_FIELD_NUMBER: _ClassVar[int]
    COSTS_FIELD_NUMBER: _ClassVar[int]
    EVICTIONS_FIELD_NUMBER: _ClassVar[int]
    provider_name: str
    config_name: str
    flavor_name: str
    cpu: int
    memory: float
    disk: float
    bandwidth: int
    gpu: str
    gpumem: int
    has_gpu: bool
    has_quick_disks: bool
    region_names: _containers.RepeatedScalarFieldContainer[str]
    costs: _containers.RepeatedScalarFieldContainer[float]
    evictions: _containers.RepeatedScalarFieldContainer[float]
    def __init__(self, provider_name: _Optional[str] = ..., config_name: _Optional[str] = ..., flavor_name: _Optional[str] = ..., cpu: _Optional[int] = ..., memory: _Optional[float] = ..., disk: _Optional[float] = ..., bandwidth: _Optional[int] = ..., gpu: _Optional[str] = ..., gpumem: _Optional[int] = ..., has_gpu: bool = ..., has_quick_disks: bool = ..., region_names: _Optional[_Iterable[str]] = ..., costs: _Optional[_Iterable[float]] = ..., evictions: _Optional[_Iterable[float]] = ...) -> None: ...

class FlavorId(_message.Message):
    __slots__ = ("flavor_id",)
    FLAVOR_ID_FIELD_NUMBER: _ClassVar[int]
    flavor_id: int
    def __init__(self, flavor_id: _Optional[int] = ...) -> None: ...

class TaskStatusCountsRequest(_message.Message):
    __slots__ = ("show_hidden",)
    SHOW_HIDDEN_FIELD_NUMBER: _ClassVar[int]
    show_hidden: bool
    def __init__(self, show_hidden: bool = ...) -> None: ...

class StatusCountEntry(_message.Message):
    __slots__ = ("status", "count", "worker_id")
    STATUS_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    status: str
    count: int
    worker_id: int
    def __init__(self, status: _Optional[str] = ..., count: _Optional[int] = ..., worker_id: _Optional[int] = ...) -> None: ...

class TaskStatusCountsResponse(_message.Message):
    __slots__ = ("global_counts", "per_worker_counts", "total_count")
    GLOBAL_COUNTS_FIELD_NUMBER: _ClassVar[int]
    PER_WORKER_COUNTS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_COUNT_FIELD_NUMBER: _ClassVar[int]
    global_counts: _containers.RepeatedCompositeFieldContainer[StatusCountEntry]
    per_worker_counts: _containers.RepeatedCompositeFieldContainer[StatusCountEntry]
    total_count: int
    def __init__(self, global_counts: _Optional[_Iterable[_Union[StatusCountEntry, _Mapping]]] = ..., per_worker_counts: _Optional[_Iterable[_Union[StatusCountEntry, _Mapping]]] = ..., total_count: _Optional[int] = ...) -> None: ...
