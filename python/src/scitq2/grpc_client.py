# src/scitq2/grpc_client.py

import grpc
import os
from scitq2.pb import taskqueue_pb2, taskqueue_pb2_grpc
from scitq2.constants import DEFAULT_RECRUITER_TIMEOUT
from typing import Optional, List

DEFAULT_EMBEDDED_CERT = b"""-----BEGIN CERTIFICATE-----
MIIFJTCCAw2gAwIBAgIUXIJA+VktSR67ML6vvO8VMh9D/24wDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI2MDQwODA5MjAxOVoXDTM2MDQw
NTA5MjAxOVowFDESMBAGA1UEAwwJbG9jYWxob3N0MIICIjANBgkqhkiG9w0BAQEF
AAOCAg8AMIICCgKCAgEAvyDam0+cbdIosUfphYvk+tosWE6CTPt3fsn/u2PSo19V
XDrGBrlojA7JVXywh/2hdQWEGmNakIUNB04kiHIS50NhslIXUxqW0xVu5h9WNkgs
xiyAw0nCqThNCkv3yEge8mudd7HXWVGr1PoVeRTGIQ859vV/is81P3UqvoCOQGG8
5HYx1ZYC9M39SM6X/97IeXsxEXqdnFnlI69fqWbRg6Apt7WqdoCu+GBxucJHP0ye
6zzmnYo0ycUScGIcc/oDTo24FDMZJJIGaJA4KvP8HCtX/wBO/F8lqpRWg9Lvo5OE
ZKTmeTmf58RTLelerqjDqtaC40bbc+Z1W+dhcnoeL8f0v7VC5cOlqFvYhFPTzNHb
7uHrdUF022orl3p6EYxV0IbyXCAv1HuNR/SEKajpn8L6jXDlu3roQmqeDt+u+VFV
ijC3825Nn7nMMsKpoM15jWjFj9oVkoe0shTsNsr7+koldFQixORvKAlEyXLb3iSz
Wk10OGCc4IBLXW+KlOqlOz4PBIf5okxnxWnoYfdSmaqRM58zaF0LXrxbIRNlL19d
gVNAMw2dgXU/4P3Nrw57sSh17gSOWbKpSRibNj9rWXFl3IH+hG2OdkqidBYNZnbD
W/0B2iIOEQoIOJFCcGgScfE6cNZZaNYXdbcQYxta4v/LVWK1PyyNGNmAUSwEhZ0C
AwEAAaNvMG0wHQYDVR0OBBYEFGAjUJmjEIgEfKa5iQHc+S5U2hBpMB8GA1UdIwQY
MBaAFGAjUJmjEIgEfKa5iQHc+S5U2hBpMA8GA1UdEwEB/wQFMAMBAf8wGgYDVR0R
BBMwEYIJbG9jYWxob3N0hwR/AAABMA0GCSqGSIb3DQEBCwUAA4ICAQAFYBqaEg0g
IgXXrfEH5Sm9tHxWyi7bTaxd74Ax+SK9f/Xr2P/RehWQg10m05UXBn6f2a4cZ+j4
uMD0NxVysqFX2cQWBZ5pgs8zKF15iiS6ncrqINGg+tNgZ3hDJM/azJt+bxdqzOXJ
52d4yx29uG+ixvSAo8H0cJuCf78rGIMiuK2RppwLA+plqrKMmog75ipfybqOlRHm
jMx/JI/IjRpxwYL4RV5JrVTEN51ZDyrT726pYUOrWVeU7bYL7d/BjFneFn6WnFuq
MV7/hkrH1jkXjpe6CtazC9LLHnkt/Q9Cj9/odYeoB5TJP9Waih2lKMiHns1HPH97
P3oaBtZTDxmxfOdbCDQUWfSn1+G7o8SequY8QAohJak/kbZ+YETRLW7LtkBwyik2
NgZUKsU+Ax/IktPvObqqw8VMd2eP7DZwax3VRp7Xe9x2RSFX+FcFe4jCxhBVtrP1
eiuUvLU/0V8Je2a5kUGTZnB9Ldx+AJLRGK6IQMa/+HNvNvYUDury1zajP6c4vmjB
f6509216H/4egLlYaDroSHrtyNCzNKP3PcDgKagBiFsTaQWrQC7oi8o3G+WHKRjW
W/ighW7F2e+o6qmgQ8aXMxIvMUHIXLKOCy4b/THf/c/CqWVoaENQ8UoOlnY0KVLk
BLmzD/Ho5TEtoGCyEyCpoV6VQc//zRCvjg==
-----END CERTIFICATE-----"""

def load_tls_certificate() -> bytes:
    cert_string = os.environ.get("SCITQ_SSL_CERTIFICATE")
    if cert_string and "-----BEGIN CERTIFICATE-----" in cert_string:
        return cert_string.encode('utf-8')
    else:
        return DEFAULT_EMBEDDED_CERT

class BearerAuth(grpc.AuthMetadataPlugin):
    """Adds a Bearer token to the gRPC metadata for authorization."""
    def __init__(self, token: str):
        self.token = token

    def __call__(self, context, callback):
        callback((("authorization", f"Bearer {self.token}"),), None)

class Scitq2Client:
    """
    NO_PUBLIC_DOC
    Client for interacting with the scitq2 gRPC backend.

    This client handles:
    - Secure connection setup (TLS, with InsecureSkipVerify)
    - Bearer token authentication (via SCITQ_TOKEN environment variable)
    - Submission of workflows, steps, tasks, and recruiters
    """
    def __init__(self, server: str = None, token: str = None):
        """
        Initializes a gRPC connection with authentication.

        Parameters:
        - server (str): server address (host:port). Defaults to SCITQ_SERVER or 'localhost:50051'.
        - token (str): Bearer token for authentication. Defaults to SCITQ_TOKEN environment variable.
        """
        server = server or os.environ.get("SCITQ_SERVER", "localhost:50051")
        token = token or os.environ.get("SCITQ_TOKEN")
        if not token:
            raise RuntimeError("Missing SCITQ_TOKEN environment variable")

        trusted_certs = load_tls_certificate()
        credentials = grpc.ssl_channel_credentials(root_certificates=trusted_certs)
        call_credentials = grpc.metadata_call_credentials(BearerAuth(token))
        composite_credentials = grpc.composite_channel_credentials(credentials, call_credentials)

        self.channel = grpc.secure_channel(
            server,
            composite_credentials,
            options=[
                ('grpc.max_send_message_length', 50 * 1024 * 1024),
                ('grpc.max_receive_message_length', 50 * 1024 * 1024),
            ],
        )
        self.stub = taskqueue_pb2_grpc.TaskQueueStub(self.channel)

    def create_workflow(self, name: str, maximum_workers: Optional[int] = None, status: Optional[str] = None) -> int:
        """
        Creates a new workflow on the server.

        Parameters:
        - name (str): Name of the workflow
        - description (str): Optional description

        Returns:
        - int: The workflow ID
        """
        request = taskqueue_pb2.WorkflowRequest(name=name)
        if maximum_workers is not None:
            request.maximum_workers = maximum_workers
        if status is not None:
            request.status = status
        response = self.stub.CreateWorkflow(request)
        return response.workflow_id

    def update_workflow_status(self, *, workflow_id: int, status: str, maximum_workers: Optional[int] = None) -> None:
        """
        Updates workflow status and optionally maximum_workers.
        """
        request = taskqueue_pb2.WorkflowStatusUpdate(workflow_id=workflow_id, status=status)
        if maximum_workers is not None:
            request.maximum_workers = maximum_workers
        self.stub.UpdateWorkflowStatus(request)

    def delete_workflow(self, workflow_id: int) -> None:
        self.stub.DeleteWorkflow(taskqueue_pb2.WorkflowId(workflow_id=workflow_id))

    def list_tasks(self, workflow_id: Optional[int] = None, show_hidden: bool = False, status: Optional[str] = None):
        req = taskqueue_pb2.ListTasksRequest()
        if workflow_id is not None:
            req.workflow_id_filter = workflow_id
        if show_hidden:
            req.show_hidden = True
        if status is not None:
            req.status_filter = status
        return self.stub.ListTasks(req).tasks

    def list_workers(self, *, workflow_id: Optional[int] = None):
        req = taskqueue_pb2.ListWorkersRequest()
        if workflow_id is not None:
            req.workflow_id = workflow_id
        return self.stub.ListWorkers(req).workers

    def delete_worker(self, *, worker_id: int) -> None:
        self.stub.DeleteWorker(taskqueue_pb2.WorkerDeletion(worker_id=worker_id))

    def list_recruiters(self, *, step_id: Optional[int] = None):
        req = taskqueue_pb2.RecruiterFilter()
        if step_id is not None:
            req.step_id = step_id
        return self.stub.ListRecruiters(req).recruiters

    def update_worker(self, *, worker_id: int, step_id: Optional[int] = None) -> None:
        req = taskqueue_pb2.WorkerUpdateRequest(worker_id=worker_id)
        if step_id is not None:
            req.step_id = step_id
        self.stub.UserUpdateWorker(req)

    def debug_assign_task(self, *, workflow_id: int, task_id: int) -> None:
        req = taskqueue_pb2.DebugAssignRequest(workflow_id=workflow_id, task_id=task_id)
        self.stub.DebugAssignTask(req)

    def debug_recruit_step(self, *, workflow_id: int, step_id: int) -> None:
        req = taskqueue_pb2.DebugRecruitRequest(workflow_id=workflow_id, step_id=step_id)
        self.stub.DebugRecruitStep(req)

    def debug_retry_task(self, *, task_id: int) -> int:
        resp = self.stub.DebugRetryTask(taskqueue_pb2.RetryTaskRequest(task_id=task_id))
        return resp.task_id

    def list_dependent_pending_tasks(self, task_id: int) -> List[int]:
        resp = self.stub.ListDependentPendingTasks(taskqueue_pb2.TaskId(task_id=task_id))
        return list(resp.task_ids)

    def stream_task_logs(self, *, task_id: int, log_type: str):
        if log_type == "stdout":
            return self.stub.StreamTaskLogsOutput(taskqueue_pb2.TaskId(task_id=task_id))
        if log_type == "stderr":
            return self.stub.StreamTaskLogsErr(taskqueue_pb2.TaskId(task_id=task_id))
        raise ValueError("log_type must be 'stdout' or 'stderr'")

    def create_step(self, workflow_id: int, name: str, quality_definition: Optional[str] = None) -> int:
        """
        Creates a new step associated with a given workflow.

        Parameters:
        - workflow_id (int): The parent workflow's ID
        - name (str): Name of the step
        - quality_definition (str, optional): JSON quality definition

        Returns:
        - int: The step ID
        """
        request = taskqueue_pb2.StepRequest(workflow_id=workflow_id, name=name)
        if quality_definition is not None:
            request.quality_definition = quality_definition
        response = self.stub.CreateStep(request)
        return response.step_id

    def signal_task(self, task_id: int, signal: str = "K"):
        """Send a signal to a running task. K=SIGKILL, T=SIGTERM."""
        self.stub.SignalTask(taskqueue_pb2.TaskSignalRequest(task_id=task_id, signal=signal))

    def submit_task(
        self,
        *,
        step_id: int,
        command: str,
        container: str,
        shell: Optional[str] = None,
        container_options: Optional[str] = None,
        inputs: Optional[List[str]] = None,
        resources: Optional[List[str]] = None,
        output: Optional[str] = None,
        retry: Optional[int] = None,
        is_final: Optional[bool] = None,
        uses_cache: Optional[bool] = None,
        download_timeout: Optional[float] = None,
        running_timeout: Optional[float] = None,
        upload_timeout: Optional[float] = None,
        status: str = "P",
        depends: Optional[List[int]] = None,
        task_name: Optional[str] = None,
        skip_if_exists: bool = False,
        accept_failure: bool = False,
        publish: Optional[str] = None,
        reuse_key: Optional[str] = None,
    ) -> int:
        """
        Submits a task to a specific step.

        Parameters match those in the proto definition.

        Returns:
        - int: The task ID
        """
        request = taskqueue_pb2.TaskRequest(
            step_id=step_id,
            command=command,
            container=container,
            status=status,
        )
        if shell is not None:
            request.shell = shell
        if container_options is not None:
            request.container_options = container_options
        if inputs is not None:
            request.input.extend(inputs)
        if resources is not None:
            request.resource.extend(resources)
        if output is not None:
            request.output = output
        if retry is not None:
            request.retry = retry
        if is_final is not None:
            request.is_final = is_final
        if uses_cache is not None:
            request.uses_cache = uses_cache
        if download_timeout is not None:
            request.download_timeout = download_timeout
        if running_timeout is not None:
            request.running_timeout = running_timeout
        if upload_timeout is not None:
            request.upload_timeout = upload_timeout
        if depends is not None:
            request.dependency.extend(depends)
        if task_name is not None:
            request.task_name = task_name
        if skip_if_exists:
            request.skip_if_exists = True
        if accept_failure:
            request.accept_failure = True
        if publish is not None:
            request.publish = publish
        if reuse_key is not None:
            request.reuse_key = reuse_key
        response = self.stub.SubmitTask(request)
        return response.task_id

    def create_recruiter(self, *, step_id: int, protofilter: str, rank: int=1, 
                         concurrency: Optional[int]=None, prefetch: Optional[int]=None, 
                         cpu_per_task: Optional[int]=None, memory_per_task: Optional[float]=None, disk_per_task: Optional[float]=None, 
                         concurrency_max: Optional[int]=None, concurrency_min: Optional[int]=None,
                         prefetch_percent: Optional[int]=None,
                         max_recruited: Optional[int]=None, rounds: int=1, timeout: int=DEFAULT_RECRUITER_TIMEOUT) -> int:
        """
        Creates a recruiter for a given step.

        Parameters:
        - step_id (int): ID of the step the recruiter belongs to
        - protofilter (str): Recruitment rule
        - rank (int): (optional) Enable concurrent recruiters (adjust timeout)

        either static concurrency:
        - concurrency (int): (optional) Concurrency settings for workers recruited here
        - prefetch (int): (optional) Prefetch settings for workers recruited here

        or dynamic concurrency (if concurrency is omitted, one of cpu_per_task, memory_per_task or disk_per_task must be specified):
        - cpu_per_task (int): (optional) Number of CPU required for 1 task,
        - memory_per_task (float): (optional) Memory (Gb) required for 1 task, 
        - disk_per_task (float): (optional) Disk space (Gb) required for 1 task,
        - prefetch_percent (int): (optional) prefetch expressed as a % of concurrency, 100 => prefetch = concurrency
        - concurrency_min (int): (optional) Minimal concurrency (only used with dynamic concurrency)
        - concurrency_max (int): (optional) Maximal concurrency (only used with dynamic concurrency)

        - max_recruited (int): (optional) Set an upper limit for this recruiter
        - rounds (int): (optional) Adjust level of recruitment based on the number of iteration expected for each worker
        - timeout (int): (optional) Adjust recycling/newly created worker strategy

        Returns:
        - int: The recruiter ID
        """
        request = taskqueue_pb2.Recruiter(
            step_id=step_id,
            protofilter=protofilter,
            rank=rank,
            concurrency=concurrency,
            prefetch=prefetch,
            cpu_per_task=cpu_per_task,
            memory_per_task=memory_per_task,
            disk_per_task=disk_per_task,
            prefetch_percent=prefetch_percent,
            concurrency_min=concurrency_min,
            concurrency_max=concurrency_max,
            rounds=rounds,
            timeout=timeout,
        )
        if max_recruited is not None:
            request.max_workers = max_recruited
        response = self.stub.CreateRecruiter(request)
        return response.success
    
    def fetch_list(self, uri) -> list[str]:
        """
        Fetches a list of files from a given URI.
        Parameters:
        - uri (str): The URI to fetch files from.
        Returns:
        - list[str]: List of file paths.
        """
        request = taskqueue_pb2.FetchListRequest(uri=uri)
        response = self.stub.FetchList(request)
        return response.files

    def fetch_info(self, uri: str) -> taskqueue_pb2.FetchInfoResponse:
        """
        Fetches a the info for a given cloud object.
        Parameters:
        - uri (str): The URI of the cloud object.
        Returns:
        - FetchInfoResponse object (uri, filename, size, is_file, is_dir)
        """
        request = taskqueue_pb2.FetchListRequest(uri=uri)
        response = self.stub.FetchInfo(request)
        return response

    def update_template_run(self, template_run_id: int, workflow_id: Optional[int] = None, error_message: Optional[str] = None):
        """
        Updates the status of a workflow template run. Can be used to attach a workflow_id
        or report a failure.

        Args:
            template_run_id: The template run ID from SCITQ_TEMPLATE_RUN_ID
            workflow_id: Optional ID of the created workflow (if successful)
            error_message: Optional error message (if failed)
        """
        request = taskqueue_pb2.UpdateTemplateRunRequest(
            template_run_id=template_run_id,
        )
        if workflow_id is not None:
            request.workflow_id = workflow_id
        if error_message is not None:
            request.error_message = error_message

        return self.stub.UpdateTemplateRun(request)

    def update_task_status(
        self,
        *,
        task_id: int,
        new_status: str,
        duration: Optional[int] = None,
        free_retry: Optional[bool] = None,
    ) -> bool:
        """
        Updates the status of a task.

        Parameters:
        - task_id (int): Task ID
        - new_status (str): New status value (e.g., "P")
        - duration (int): Optional duration in seconds
        - free_retry (bool): Optional retry flag for failure status

        Returns:
        - bool: Success flag from server
        """
        request = taskqueue_pb2.TaskStatusUpdate(
            task_id=task_id,
            new_status=new_status,
        )
        if duration is not None:
            request.duration = duration
        if free_retry is not None:
            request.free_retry = free_retry
        response = self.stub.UpdateTaskStatus(request)
        return response.success
    
    def get_workspace_root(self, provider: str, region: str) -> str:
        """
        Fetches the local workspace root URI for a given provider and region.

        Parameters:
        - provider (str): The provider identifier (e.g., 'azure.default')
        - region (str): The region identifier (e.g., 'northeurope')

        Returns:
        - str: The root URI string for the workspace (e.g., 'aznorth://workspace')
        """
        request = taskqueue_pb2.WorkspaceRootRequest(provider=provider, region=region)
        response = self.stub.GetWorkspaceRoot(request)
        return response.root_uri

    def get_resource_root(self, provider: str, region: str) -> str:
        """Fetches the local resource root URI for a given provider and region."""
        request = taskqueue_pb2.WorkspaceRootRequest(provider=provider, region=region)
        response = self.stub.GetResourceRoot(request)
        return response.root_uri
