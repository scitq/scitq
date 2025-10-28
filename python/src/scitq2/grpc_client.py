# src/scitq2/grpc_client.py

import grpc
import os
from scitq2.pb import taskqueue_pb2, taskqueue_pb2_grpc
from scitq2.constants import DEFAULT_RECRUITER_TIMEOUT
from typing import Optional, List

DEFAULT_EMBEDDED_CERT = b"""-----BEGIN CERTIFICATE-----
MIIFCTCCAvGgAwIBAgIUNZ5Q3aLDxUrUdyAz2+TYGxiuU4cwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJbG9jYWxob3N0MB4XDTI1MDIwMTIwMzk0NVoXDTM1MDEz
MDIwMzk0NVowFDESMBAGA1UEAwwJbG9jYWxob3N0MIICIjANBgkqhkiG9w0BAQEF
AAOCAg8AMIICCgKCAgEAu/4dLapuDNJQbdgCpz029xck1eo9c+V/I22s1wny7NO3
j3nE5DAogRKzft0dJxkXT5X4Hho882D2Uae54j4i3tSg+2og6QMwHOo09FyorkMN
KUfkiiScJ8pmiNqQdYSlpY1tdv3CIyi97p5c5zt5qupWMT3iMihuTLGl4lBfweF5
Hwuc6HRySRAUJUf7wQklE9660gTjZDWsKQbI88av0QzNamDnz+aaXbZuakuM+iw9
WyASyMpXsIU0/p+mB0wEJonczVh372hncGOU2uuXe7okhujQ4eO85kiwbXYCRXBH
lL9Fa38VXHy1/s8IJLCQS0daYnhwePY/OO5eMVOm5JkMZhHQ3HmF3uppExVHxCLm
F8T+xlSh9kJ1kPbpqsfPzSgN0fGWahgtdA5II/2hy4jtjRxdQa7hE+fvigdzWyzT
p+KfCqWvTbYoJlHF1vs7PcZ5S5EtvzZGHDvMxdusw2YmM5h3JBLYknZCDEggbj8E
19CX6fLybnX0ZNx9kvpLMPuGjyg1qZSPHbmy7Y6DJpntE+HQXjFcNX3kzRzvJJ3g
5oIhR5l0KOccQqc1QhmZ6bgsjnb+6QEoqE5YbtsIjxLPVn1cCixEyBYLpWJeiFxW
pqNgUwdVx6sFfM1YPAS69x0Zybp0LahWspf/VWVnMZbipd1N5p00dFxEKf3+R3UC
AwEAAaNTMFEwHQYDVR0OBBYEFKqv1gulfVc84m3jrr2spn9cB5hMMB8GA1UdIwQY
MBaAFKqv1gulfVc84m3jrr2spn9cB5hMMA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZI
hvcNAQELBQADggIBAFrTslO56fBIor/hY3torsAcUxSECDAzWhOP3g5RptTQDtA8
23Q9DVdXWuqkzAuEYw9MvpMlx3s/YBzD/ZRruf8gZdO6BT3aSu4eI1TRuq//nyVq
hA6ejOj3LOBdDTq8iPUyh4FLXAcGPllmmZgdp7fJ26atnOr1o9A3mGh/nTITejk2
VBvXdEEYDVyh7sBVFTO1VETgXvrO9ereyd/oGosfmDSd45F4lOmpK1FRYeo3U1We
YTEZLNJEPKUydIXf+NzCWF+ehFG8ovqk8tGKbD4tzgj5reuKlmknGk2On9rpL6qM
QvVJwxdXoBg2QaZ/l6XGlIxYKrQEOL8DTsKVb5XBvTXq3DBoh0U/j1dn5SEyNkEA
HCX7fSW+SNxHy3Cofxez62UgfmnnTW/qQtZAewkXizKlZoMKL997Lhu6dLQKQBx7
Gklbv0GuEOahGUIFrZHrU1e21q4ehyDCxakpMSNOVPTP0CkiA+2W0+q2yicYPcVJ
zDt+HSOZ0Agie8LJhQ4/vOfEK/O5NzUa15QuT5Haqzdv+Zsx146CPbwdSe6/Q5gv
KwaOqDFyO5he1Irqb1XOMXOF7FJZIct+VvL2ECO8zPfPcTHcaR67/bNLOxJRd8LO
qJ0sx+5dzhtylCwlS5XDjvYig4HAJ599kMZzvBu17LCJCZeo4n7ZGRbKi+Lx
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

        self.channel = grpc.secure_channel(server, composite_credentials)
        self.stub = taskqueue_pb2_grpc.TaskQueueStub(self.channel)

    def create_workflow(self, name: str, maximum_workers: Optional[int] = None) -> int:
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
        response = self.stub.CreateWorkflow(request)
        return response.workflow_id

    def create_step(self, workflow_id: int, name: str) -> int:
        """
        Creates a new step associated with a given workflow.

        Parameters:
        - workflow_id (int): The parent workflow's ID
        - name (str): Name of the step

        Returns:
        - int: The step ID
        """
        request = taskqueue_pb2.StepRequest(workflow_id=workflow_id, name=name)
        response = self.stub.CreateStep(request)
        return response.step_id

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