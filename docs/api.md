# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [proto/taskqueue.proto](#proto_taskqueue-proto)
    - [Accum](#taskqueue-Accum)
    - [Ack](#taskqueue-Ack)
    - [ChangePasswordRequest](#taskqueue-ChangePasswordRequest)
    - [CreateUserRequest](#taskqueue-CreateUserRequest)
    - [DeleteTemplateRunRequest](#taskqueue-DeleteTemplateRunRequest)
    - [DiskIOStats](#taskqueue-DiskIOStats)
    - [DiskUsage](#taskqueue-DiskUsage)
    - [DockerCredential](#taskqueue-DockerCredential)
    - [DockerCredentials](#taskqueue-DockerCredentials)
    - [FetchInfoResponse](#taskqueue-FetchInfoResponse)
    - [FetchListRequest](#taskqueue-FetchListRequest)
    - [FetchListResponse](#taskqueue-FetchListResponse)
    - [Flavor](#taskqueue-Flavor)
    - [FlavorCreateRequest](#taskqueue-FlavorCreateRequest)
    - [FlavorId](#taskqueue-FlavorId)
    - [FlavorsList](#taskqueue-FlavorsList)
    - [GetLogsRequest](#taskqueue-GetLogsRequest)
    - [GetWorkerStatsRequest](#taskqueue-GetWorkerStatsRequest)
    - [GetWorkerStatsResponse](#taskqueue-GetWorkerStatsResponse)
    - [GetWorkerStatsResponse.WorkerStatsEntry](#taskqueue-GetWorkerStatsResponse-WorkerStatsEntry)
    - [Job](#taskqueue-Job)
    - [JobId](#taskqueue-JobId)
    - [JobStatus](#taskqueue-JobStatus)
    - [JobStatusRequest](#taskqueue-JobStatusRequest)
    - [JobStatusResponse](#taskqueue-JobStatusResponse)
    - [JobUpdate](#taskqueue-JobUpdate)
    - [JobsList](#taskqueue-JobsList)
    - [ListFlavorsRequest](#taskqueue-ListFlavorsRequest)
    - [ListJobsRequest](#taskqueue-ListJobsRequest)
    - [ListTasksRequest](#taskqueue-ListTasksRequest)
    - [ListWorkersRequest](#taskqueue-ListWorkersRequest)
    - [LogChunk](#taskqueue-LogChunk)
    - [LogChunkList](#taskqueue-LogChunkList)
    - [LoginRequest](#taskqueue-LoginRequest)
    - [LoginResponse](#taskqueue-LoginResponse)
    - [NetIOStats](#taskqueue-NetIOStats)
    - [PingAndGetNewTasksRequest](#taskqueue-PingAndGetNewTasksRequest)
    - [Provider](#taskqueue-Provider)
    - [ProviderList](#taskqueue-ProviderList)
    - [RcloneRemote](#taskqueue-RcloneRemote)
    - [RcloneRemote.OptionsEntry](#taskqueue-RcloneRemote-OptionsEntry)
    - [RcloneRemotes](#taskqueue-RcloneRemotes)
    - [RcloneRemotes.RemotesEntry](#taskqueue-RcloneRemotes-RemotesEntry)
    - [Recruiter](#taskqueue-Recruiter)
    - [RecruiterFilter](#taskqueue-RecruiterFilter)
    - [RecruiterId](#taskqueue-RecruiterId)
    - [RecruiterList](#taskqueue-RecruiterList)
    - [RecruiterUpdate](#taskqueue-RecruiterUpdate)
    - [Region](#taskqueue-Region)
    - [RegionList](#taskqueue-RegionList)
    - [ResourceSpec](#taskqueue-ResourceSpec)
    - [RetryTaskRequest](#taskqueue-RetryTaskRequest)
    - [RunTemplateRequest](#taskqueue-RunTemplateRequest)
    - [Step](#taskqueue-Step)
    - [StepFilter](#taskqueue-StepFilter)
    - [StepId](#taskqueue-StepId)
    - [StepList](#taskqueue-StepList)
    - [StepRequest](#taskqueue-StepRequest)
    - [StepStats](#taskqueue-StepStats)
    - [StepStatsRequest](#taskqueue-StepStatsRequest)
    - [StepStatsResponse](#taskqueue-StepStatsResponse)
    - [Task](#taskqueue-Task)
    - [TaskId](#taskqueue-TaskId)
    - [TaskIds](#taskqueue-TaskIds)
    - [TaskList](#taskqueue-TaskList)
    - [TaskListAndOther](#taskqueue-TaskListAndOther)
    - [TaskLog](#taskqueue-TaskLog)
    - [TaskRequest](#taskqueue-TaskRequest)
    - [TaskResponse](#taskqueue-TaskResponse)
    - [TaskStatusUpdate](#taskqueue-TaskStatusUpdate)
    - [TaskUpdate](#taskqueue-TaskUpdate)
    - [TaskUpdateList](#taskqueue-TaskUpdateList)
    - [TaskUpdateList.UpdatesEntry](#taskqueue-TaskUpdateList-UpdatesEntry)
    - [Template](#taskqueue-Template)
    - [TemplateFilter](#taskqueue-TemplateFilter)
    - [TemplateList](#taskqueue-TemplateList)
    - [TemplateRun](#taskqueue-TemplateRun)
    - [TemplateRunFilter](#taskqueue-TemplateRunFilter)
    - [TemplateRunList](#taskqueue-TemplateRunList)
    - [Token](#taskqueue-Token)
    - [UpdateTemplateRunRequest](#taskqueue-UpdateTemplateRunRequest)
    - [UploadTemplateRequest](#taskqueue-UploadTemplateRequest)
    - [UploadTemplateResponse](#taskqueue-UploadTemplateResponse)
    - [User](#taskqueue-User)
    - [UserId](#taskqueue-UserId)
    - [UsersList](#taskqueue-UsersList)
    - [Worker](#taskqueue-Worker)
    - [WorkerDeletion](#taskqueue-WorkerDeletion)
    - [WorkerDetails](#taskqueue-WorkerDetails)
    - [WorkerEvent](#taskqueue-WorkerEvent)
    - [WorkerEventFilter](#taskqueue-WorkerEventFilter)
    - [WorkerEventId](#taskqueue-WorkerEventId)
    - [WorkerEventList](#taskqueue-WorkerEventList)
    - [WorkerEventPruneFilter](#taskqueue-WorkerEventPruneFilter)
    - [WorkerEventPruneResult](#taskqueue-WorkerEventPruneResult)
    - [WorkerEventRecord](#taskqueue-WorkerEventRecord)
    - [WorkerId](#taskqueue-WorkerId)
    - [WorkerIds](#taskqueue-WorkerIds)
    - [WorkerInfo](#taskqueue-WorkerInfo)
    - [WorkerRequest](#taskqueue-WorkerRequest)
    - [WorkerStats](#taskqueue-WorkerStats)
    - [WorkerStatus](#taskqueue-WorkerStatus)
    - [WorkerStatusRequest](#taskqueue-WorkerStatusRequest)
    - [WorkerStatusResponse](#taskqueue-WorkerStatusResponse)
    - [WorkerUpdateRequest](#taskqueue-WorkerUpdateRequest)
    - [WorkersList](#taskqueue-WorkersList)
    - [Workflow](#taskqueue-Workflow)
    - [WorkflowFilter](#taskqueue-WorkflowFilter)
    - [WorkflowId](#taskqueue-WorkflowId)
    - [WorkflowList](#taskqueue-WorkflowList)
    - [WorkflowRequest](#taskqueue-WorkflowRequest)
    - [WorkspaceRootRequest](#taskqueue-WorkspaceRootRequest)
    - [WorkspaceRootResponse](#taskqueue-WorkspaceRootResponse)
  
    - [TaskQueue](#taskqueue-TaskQueue)
  
- [Scalar Value Types](#scalar-value-types)



<a name="proto_taskqueue-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## proto/taskqueue.proto



<a name="taskqueue-Accum"></a>

### Accum



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| count | [int32](#int32) |  | number of observations |
| sum | [float](#float) |  | total seconds |
| min | [float](#float) |  | minimum seconds |
| max | [float](#float) |  | maximum seconds |






<a name="taskqueue-Ack"></a>

### Ack



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| success | [bool](#bool) |  |  |






<a name="taskqueue-ChangePasswordRequest"></a>

### ChangePasswordRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| username | [string](#string) |  |  |
| old_password | [string](#string) |  |  |
| new_password | [string](#string) |  |  |






<a name="taskqueue-CreateUserRequest"></a>

### CreateUserRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| username | [string](#string) |  |  |
| password | [string](#string) |  |  |
| email | [string](#string) |  |  |
| is_admin | [bool](#bool) |  |  |






<a name="taskqueue-DeleteTemplateRunRequest"></a>

### DeleteTemplateRunRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| template_run_id | [int32](#int32) |  |  |






<a name="taskqueue-DiskIOStats"></a>

### DiskIOStats



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| read_bytes_total | [int64](#int64) |  | Total bytes read |
| write_bytes_total | [int64](#int64) |  | Total bytes written |
| read_bytes_rate | [float](#float) |  | Bytes per second |
| write_bytes_rate | [float](#float) |  | Bytes per second |






<a name="taskqueue-DiskUsage"></a>

### DiskUsage



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| device_name | [string](#string) |  | E.g., &#34;/dev/sda1&#34; |
| usage_percent | [float](#float) |  | 0-100, float |






<a name="taskqueue-DockerCredential"></a>

### DockerCredential



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| registry | [string](#string) |  | e.g., &#34;3jfz1gy8.gra7.container-registry.ovh.net&#34; |
| auth | [string](#string) |  | auth string as found in .docker/config.json |






<a name="taskqueue-DockerCredentials"></a>

### DockerCredentials



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| credentials | [DockerCredential](#taskqueue-DockerCredential) | repeated |  |






<a name="taskqueue-FetchInfoResponse"></a>

### FetchInfoResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| uri | [string](#string) |  |  |
| filename | [string](#string) |  |  |
| description | [string](#string) |  |  |
| size | [int64](#int64) |  |  |
| is_file | [bool](#bool) |  |  |
| is_dir | [bool](#bool) |  |  |






<a name="taskqueue-FetchListRequest"></a>

### FetchListRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| uri | [string](#string) |  | URI to fetch the list from (can include glob patterns) |






<a name="taskqueue-FetchListResponse"></a>

### FetchListResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| files | [string](#string) | repeated | Absolute paths from the given URI |






<a name="taskqueue-Flavor"></a>

### Flavor



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| flavor_id | [int32](#int32) |  | Fields from the &#34;flavor&#34; table

PRIMARY KEY |
| flavor_name | [string](#string) |  | Name of the flavor |
| provider_id | [int32](#int32) |  | Foreign key to provider table |
| provider | [string](#string) |  | Name of the provider (provider_name.config_name) |
| cpu | [int32](#int32) |  | Number of CPU cores |
| mem | [float](#float) |  | Memory in GB (or as needed) |
| disk | [float](#float) |  | Disk size in GB (or as needed) |
| bandwidth | [int32](#int32) |  | Bandwidth (if applicable) |
| gpu | [string](#string) |  | GPU description |
| gpumem | [int32](#int32) |  | GPU memory (in GB, for example) |
| has_gpu | [bool](#bool) |  | Whether a GPU is present |
| has_quick_disks | [bool](#bool) |  | Whether quick disks are supported |
| region_id | [int32](#int32) |  | Fields from the &#34;flavor_region&#34; table

Foreign key to region table |
| region | [string](#string) |  | (Optional) Region name |
| eviction | [float](#float) |  | Eviction rate value |
| cost | [float](#float) |  | Cost value |






<a name="taskqueue-FlavorCreateRequest"></a>

### FlavorCreateRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider_name | [string](#string) |  |  |
| config_name | [string](#string) |  |  |
| flavor_name | [string](#string) |  |  |
| cpu | [int32](#int32) |  |  |
| memory | [float](#float) |  |  |
| disk | [float](#float) |  |  |
| bandwidth | [int32](#int32) | optional |  |
| gpu | [string](#string) | optional |  |
| gpumem | [int32](#int32) | optional |  |
| has_gpu | [bool](#bool) | optional |  |
| has_quick_disks | [bool](#bool) | optional |  |
| region_names | [string](#string) | repeated |  |
| costs | [float](#float) | repeated |  |
| evictions | [float](#float) | repeated |  |






<a name="taskqueue-FlavorId"></a>

### FlavorId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| flavor_id | [int32](#int32) |  |  |






<a name="taskqueue-FlavorsList"></a>

### FlavorsList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| flavors | [Flavor](#taskqueue-Flavor) | repeated |  |






<a name="taskqueue-GetLogsRequest"></a>

### GetLogsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| taskIds | [int32](#int32) | repeated |  |
| chunkSize | [int32](#int32) |  |  |
| skipFromEnd | [int32](#int32) | optional |  |
| log_type | [string](#string) | optional |  |






<a name="taskqueue-GetWorkerStatsRequest"></a>

### GetWorkerStatsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_ids | [int32](#int32) | repeated |  |






<a name="taskqueue-GetWorkerStatsResponse"></a>

### GetWorkerStatsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_stats | [GetWorkerStatsResponse.WorkerStatsEntry](#taskqueue-GetWorkerStatsResponse-WorkerStatsEntry) | repeated |  |






<a name="taskqueue-GetWorkerStatsResponse-WorkerStatsEntry"></a>

### GetWorkerStatsResponse.WorkerStatsEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [int32](#int32) |  |  |
| value | [WorkerStats](#taskqueue-WorkerStats) |  |  |






<a name="taskqueue-Job"></a>

### Job



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| job_id | [int32](#int32) |  |  |
| status | [string](#string) |  |  |
| flavor_id | [int32](#int32) |  |  |
| retry | [int32](#int32) |  |  |
| worker_id | [int32](#int32) |  |  |
| action | [string](#string) |  |  |
| created_at | [string](#string) |  |  |
| modified_at | [string](#string) |  |  |
| progression | [int32](#int32) |  |  |
| log | [string](#string) |  |  |






<a name="taskqueue-JobId"></a>

### JobId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| job_id | [int32](#int32) |  |  |






<a name="taskqueue-JobStatus"></a>

### JobStatus



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| job_id | [int32](#int32) |  |  |
| status | [string](#string) |  |  |
| progression | [int32](#int32) |  |  |






<a name="taskqueue-JobStatusRequest"></a>

### JobStatusRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| job_ids | [int32](#int32) | repeated |  |






<a name="taskqueue-JobStatusResponse"></a>

### JobStatusResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| statuses | [JobStatus](#taskqueue-JobStatus) | repeated |  |






<a name="taskqueue-JobUpdate"></a>

### JobUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| job_id | [int32](#int32) |  | which job to update |
| status | [string](#string) | optional | &#39;P&#39;|&#39;R&#39;|&#39;S&#39;|&#39;F&#39;|&#39;X&#39; (only if you want to set it) |
| append_log | [string](#string) | optional | text appended to job.log (server prepends timestamp) |
| progression | [int32](#int32) | optional | 0..100 (server clamps) |






<a name="taskqueue-JobsList"></a>

### JobsList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| jobs | [Job](#taskqueue-Job) | repeated |  |






<a name="taskqueue-ListFlavorsRequest"></a>

### ListFlavorsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| limit | [int32](#int32) |  |  |
| filter | [string](#string) |  |  |






<a name="taskqueue-ListJobsRequest"></a>

### ListJobsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| limit | [int32](#int32) | optional |  |
| offset | [int32](#int32) | optional |  |






<a name="taskqueue-ListTasksRequest"></a>

### ListTasksRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status_filter | [string](#string) | optional |  |
| worker_id_filter | [int32](#int32) | optional |  |
| workflow_id_filter | [int32](#int32) | optional |  |
| step_id_filter | [int32](#int32) | optional |  |
| command_filter | [string](#string) | optional |  |
| limit | [int32](#int32) | optional |  |
| offset | [int32](#int32) | optional |  |
| show_hidden | [bool](#bool) | optional |  |






<a name="taskqueue-ListWorkersRequest"></a>

### ListWorkersRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_id | [int32](#int32) | optional |  |






<a name="taskqueue-LogChunk"></a>

### LogChunk



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| taskId | [int32](#int32) |  |  |
| stdout | [string](#string) | repeated |  |
| stderr | [string](#string) | repeated |  |






<a name="taskqueue-LogChunkList"></a>

### LogChunkList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| logs | [LogChunk](#taskqueue-LogChunk) | repeated |  |






<a name="taskqueue-LoginRequest"></a>

### LoginRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| username | [string](#string) |  |  |
| password | [string](#string) |  |  |






<a name="taskqueue-LoginResponse"></a>

### LoginResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| token | [string](#string) |  |  |






<a name="taskqueue-NetIOStats"></a>

### NetIOStats



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| recv_bytes_total | [int64](#int64) |  | Total bytes received |
| sent_bytes_total | [int64](#int64) |  | Total bytes sent |
| recv_bytes_rate | [float](#float) |  | Bytes per second |
| sent_bytes_rate | [float](#float) |  | Bytes per second |






<a name="taskqueue-PingAndGetNewTasksRequest"></a>

### PingAndGetNewTasksRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| stats | [WorkerStats](#taskqueue-WorkerStats) |  | Optional |






<a name="taskqueue-Provider"></a>

### Provider



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider_id | [int32](#int32) |  |  |
| provider_name | [string](#string) |  |  |
| config_name | [string](#string) |  |  |






<a name="taskqueue-ProviderList"></a>

### ProviderList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| providers | [Provider](#taskqueue-Provider) | repeated |  |






<a name="taskqueue-RcloneRemote"></a>

### RcloneRemote



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| options | [RcloneRemote.OptionsEntry](#taskqueue-RcloneRemote-OptionsEntry) | repeated |  |






<a name="taskqueue-RcloneRemote-OptionsEntry"></a>

### RcloneRemote.OptionsEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [string](#string) |  |  |






<a name="taskqueue-RcloneRemotes"></a>

### RcloneRemotes



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| remotes | [RcloneRemotes.RemotesEntry](#taskqueue-RcloneRemotes-RemotesEntry) | repeated |  |






<a name="taskqueue-RcloneRemotes-RemotesEntry"></a>

### RcloneRemotes.RemotesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [string](#string) |  |  |
| value | [RcloneRemote](#taskqueue-RcloneRemote) |  |  |






<a name="taskqueue-Recruiter"></a>

### Recruiter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |
| rank | [int32](#int32) |  |  |
| protofilter | [string](#string) |  |  |
| concurrency | [int32](#int32) | optional |  |
| prefetch | [int32](#int32) | optional |  |
| max_workers | [int32](#int32) | optional |  |
| rounds | [int32](#int32) |  |  |
| timeout | [int32](#int32) |  |  |
| cpu_per_task | [int32](#int32) | optional |  |
| memory_per_task | [float](#float) | optional |  |
| disk_per_task | [float](#float) | optional |  |
| prefetch_percent | [int32](#int32) | optional |  |
| concurrency_min | [int32](#int32) | optional |  |
| concurrency_max | [int32](#int32) | optional |  |






<a name="taskqueue-RecruiterFilter"></a>

### RecruiterFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) | optional |  |






<a name="taskqueue-RecruiterId"></a>

### RecruiterId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |
| rank | [int32](#int32) |  |  |






<a name="taskqueue-RecruiterList"></a>

### RecruiterList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| recruiters | [Recruiter](#taskqueue-Recruiter) | repeated |  |






<a name="taskqueue-RecruiterUpdate"></a>

### RecruiterUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |
| rank | [int32](#int32) |  |  |
| protofilter | [string](#string) | optional |  |
| concurrency | [int32](#int32) | optional |  |
| prefetch | [int32](#int32) | optional |  |
| max_workers | [int32](#int32) | optional |  |
| rounds | [int32](#int32) | optional |  |
| timeout | [int32](#int32) | optional |  |
| cpu_per_task | [int32](#int32) | optional |  |
| memory_per_task | [float](#float) | optional |  |
| disk_per_task | [float](#float) | optional |  |
| prefetch_percent | [int32](#int32) | optional |  |
| concurrency_min | [int32](#int32) | optional |  |
| concurrency_max | [int32](#int32) | optional |  |






<a name="taskqueue-Region"></a>

### Region



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| region_id | [int32](#int32) |  |  |
| provider_id | [int32](#int32) |  |  |
| region_name | [string](#string) |  |  |
| is_default | [bool](#bool) |  |  |






<a name="taskqueue-RegionList"></a>

### RegionList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| regions | [Region](#taskqueue-Region) | repeated |  |






<a name="taskqueue-ResourceSpec"></a>

### ResourceSpec



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [string](#string) |  |  |
| cpu | [int32](#int32) |  |  |
| mem | [float](#float) |  |  |
| disk | [float](#float) |  |  |






<a name="taskqueue-RetryTaskRequest"></a>

### RetryTaskRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |
| retry | [int32](#int32) | optional |  |






<a name="taskqueue-RunTemplateRequest"></a>

### RunTemplateRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_template_id | [int32](#int32) |  |  |
| param_values_json | [string](#string) |  |  |






<a name="taskqueue-Step"></a>

### Step



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |
| workflow_name | [string](#string) |  |  |
| workflow_id | [int32](#int32) |  |  |
| name | [string](#string) |  |  |






<a name="taskqueue-StepFilter"></a>

### StepFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| WorkflowId | [int32](#int32) |  |  |
| limit | [int32](#int32) | optional |  |
| offset | [int32](#int32) | optional |  |






<a name="taskqueue-StepId"></a>

### StepId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |






<a name="taskqueue-StepList"></a>

### StepList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| steps | [Step](#taskqueue-Step) | repeated |  |






<a name="taskqueue-StepRequest"></a>

### StepRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_name | [string](#string) | optional |  |
| workflow_id | [int32](#int32) | optional |  |
| name | [string](#string) |  |  |






<a name="taskqueue-StepStats"></a>

### StepStats



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| step_id | [int32](#int32) |  |  |
| step_name | [string](#string) |  |  |
| total_tasks | [int32](#int32) |  |  |
| waiting_tasks | [int32](#int32) |  |  |
| pending_tasks | [int32](#int32) |  |  |
| accepted_tasks | [int32](#int32) |  |  |
| running_tasks | [int32](#int32) |  |  |
| uploading_tasks | [int32](#int32) |  |  |
| successful_tasks | [int32](#int32) |  |  |
| failed_tasks | [int32](#int32) |  |  |
| really_failed_tasks | [int32](#int32) |  | tasks that have exhausted all retries and are failed |
| success_run | [Accum](#taskqueue-Accum) |  | succeeded tasks&#39; run durations |
| failed_run | [Accum](#taskqueue-Accum) |  | failed tasks&#39; run durations |
| running_run | [Accum](#taskqueue-Accum) |  | running tasks&#39; elapsed durations (at eval time) |
| download | [Accum](#taskqueue-Accum) |  | download durations |
| upload | [Accum](#taskqueue-Accum) |  | upload durations |
| start_time | [int64](#int64) | optional | epoch timestamp of the first task start time |
| end_time | [int64](#int64) | optional | epoch timestamp of the last task end time |
| stats_eval_time | [int32](#int32) |  | epoch seconds when these stats were computed |






<a name="taskqueue-StepStatsRequest"></a>

### StepStatsRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_id | [int32](#int32) | optional |  |
| step_ids | [int32](#int32) | repeated |  |






<a name="taskqueue-StepStatsResponse"></a>

### StepStatsResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| stats | [StepStats](#taskqueue-StepStats) | repeated |  |






<a name="taskqueue-Task"></a>

### Task



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |
| command | [string](#string) |  |  |
| shell | [string](#string) | optional |  |
| container | [string](#string) |  |  |
| container_options | [string](#string) | optional |  |
| step_id | [int32](#int32) | optional |  |
| input | [string](#string) | repeated |  |
| resource | [string](#string) | repeated |  |
| output | [string](#string) | optional |  |
| retry | [int32](#int32) | optional |  |
| is_final | [bool](#bool) | optional |  |
| uses_cache | [bool](#bool) | optional |  |
| download_timeout | [float](#float) | optional |  |
| running_timeout | [float](#float) | optional |  |
| upload_timeout | [float](#float) | optional |  |
| status | [string](#string) |  |  |
| worker_id | [int32](#int32) | optional |  |
| workflow_id | [int32](#int32) | optional |  |
| task_name | [string](#string) | optional |  |
| retry_count | [int32](#int32) |  |  |
| hidden | [bool](#bool) |  |  |
| previous_task_id | [int32](#int32) | optional |  |
| weight | [double](#double) | optional | Fraction of the assigned worker&#39;s concurrency consumed by this task (default 1.0) |
| run_start_time | [int64](#int64) | optional | epoch timestamp of the first task start time |






<a name="taskqueue-TaskId"></a>

### TaskId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |






<a name="taskqueue-TaskIds"></a>

### TaskIds



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_ids | [int32](#int32) | repeated |  |






<a name="taskqueue-TaskList"></a>

### TaskList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tasks | [Task](#taskqueue-Task) | repeated |  |






<a name="taskqueue-TaskListAndOther"></a>

### TaskListAndOther



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| tasks | [Task](#taskqueue-Task) | repeated |  |
| concurrency | [int32](#int32) |  |  |
| updates | [TaskUpdateList](#taskqueue-TaskUpdateList) |  |  |
| active_tasks | [int32](#int32) | repeated |  |






<a name="taskqueue-TaskLog"></a>

### TaskLog



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |
| log_type | [string](#string) |  | &#39;O&#39; for stdout, &#39;E&#39; for stderr |
| log_text | [string](#string) |  |  |






<a name="taskqueue-TaskRequest"></a>

### TaskRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| command | [string](#string) |  |  |
| shell | [string](#string) | optional |  |
| container | [string](#string) |  |  |
| container_options | [string](#string) | optional |  |
| step_id | [int32](#int32) | optional |  |
| input | [string](#string) | repeated |  |
| resource | [string](#string) | repeated |  |
| output | [string](#string) | optional |  |
| retry | [int32](#int32) | optional |  |
| is_final | [bool](#bool) | optional |  |
| uses_cache | [bool](#bool) | optional |  |
| download_timeout | [float](#float) | optional |  |
| running_timeout | [float](#float) | optional |  |
| upload_timeout | [float](#float) | optional |  |
| status | [string](#string) |  |  |
| dependency | [int32](#int32) | repeated | IDs of tasks that this task depends on |
| task_name | [string](#string) | optional |  |






<a name="taskqueue-TaskResponse"></a>

### TaskResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |






<a name="taskqueue-TaskStatusUpdate"></a>

### TaskStatusUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [int32](#int32) |  |  |
| new_status | [string](#string) |  |  |
| duration | [int32](#int32) | optional | in seconds |
| free_retry | [bool](#bool) | optional | if new_status is F and this is true, then retry is increased by 1 before setting status to F |






<a name="taskqueue-TaskUpdate"></a>

### TaskUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| weight | [double](#double) |  |  |






<a name="taskqueue-TaskUpdateList"></a>

### TaskUpdateList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| updates | [TaskUpdateList.UpdatesEntry](#taskqueue-TaskUpdateList-UpdatesEntry) | repeated | optional — can be empty |






<a name="taskqueue-TaskUpdateList-UpdatesEntry"></a>

### TaskUpdateList.UpdatesEntry



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| key | [int32](#int32) |  |  |
| value | [TaskUpdate](#taskqueue-TaskUpdate) |  |  |






<a name="taskqueue-Template"></a>

### Template



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_template_id | [int32](#int32) |  |  |
| name | [string](#string) |  |  |
| version | [string](#string) |  |  |
| description | [string](#string) |  |  |
| param_json | [string](#string) |  |  |
| uploaded_at | [string](#string) |  |  |
| uploaded_by | [int32](#int32) | optional |  |






<a name="taskqueue-TemplateFilter"></a>

### TemplateFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_template_id | [int32](#int32) | optional |  |
| name | [string](#string) | optional |  |
| version | [string](#string) | optional |  |






<a name="taskqueue-TemplateList"></a>

### TemplateList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| templates | [Template](#taskqueue-Template) | repeated |  |






<a name="taskqueue-TemplateRun"></a>

### TemplateRun



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| template_run_id | [int32](#int32) |  |  |
| workflow_template_id | [int32](#int32) |  |  |
| template_name | [string](#string) | optional |  |
| template_version | [string](#string) | optional |  |
| workflow_name | [string](#string) | optional |  |
| run_by | [int32](#int32) | optional |  |
| status | [string](#string) |  |  |
| workflow_id | [int32](#int32) | optional |  |
| created_at | [string](#string) |  |  |
| param_values_json | [string](#string) |  |  |
| error_message | [string](#string) | optional |  |
| run_by_username | [string](#string) | optional |  |






<a name="taskqueue-TemplateRunFilter"></a>

### TemplateRunFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_template_id | [int32](#int32) | optional |  |






<a name="taskqueue-TemplateRunList"></a>

### TemplateRunList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| runs | [TemplateRun](#taskqueue-TemplateRun) | repeated |  |






<a name="taskqueue-Token"></a>

### Token



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| token | [string](#string) |  |  |






<a name="taskqueue-UpdateTemplateRunRequest"></a>

### UpdateTemplateRunRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| template_run_id | [int32](#int32) |  |  |
| workflow_id | [int32](#int32) | optional |  |
| error_message | [string](#string) | optional |  |






<a name="taskqueue-UploadTemplateRequest"></a>

### UploadTemplateRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| script | [bytes](#bytes) |  |  |
| force | [bool](#bool) |  |  |






<a name="taskqueue-UploadTemplateResponse"></a>

### UploadTemplateResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| success | [bool](#bool) |  |  |
| message | [string](#string) |  |  |
| workflow_template_id | [int32](#int32) | optional |  |
| name | [string](#string) | optional |  |
| version | [string](#string) | optional |  |
| description | [string](#string) | optional |  |
| param_json | [string](#string) | optional |  |






<a name="taskqueue-User"></a>

### User



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user_id | [int32](#int32) |  |  |
| username | [string](#string) | optional |  |
| email | [string](#string) | optional |  |
| is_admin | [bool](#bool) | optional |  |






<a name="taskqueue-UserId"></a>

### UserId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| user_id | [int32](#int32) |  |  |






<a name="taskqueue-UsersList"></a>

### UsersList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| users | [User](#taskqueue-User) | repeated |  |






<a name="taskqueue-Worker"></a>

### Worker



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| name | [string](#string) |  |  |
| concurrency | [int32](#int32) |  |  |
| prefetch | [int32](#int32) |  |  |
| status | [string](#string) |  |  |
| ipv4 | [string](#string) |  |  |
| ipv6 | [string](#string) |  |  |
| flavor | [string](#string) |  |  |
| provider | [string](#string) |  |  |
| region | [string](#string) |  |  |
| step_id | [int32](#int32) | optional |  |
| step_name | [string](#string) | optional |  |
| is_permanent | [bool](#bool) |  |  |
| recyclable_scope | [string](#string) |  |  |
| workflow_id | [int32](#int32) | optional |  |
| workflow_name | [string](#string) | optional |  |






<a name="taskqueue-WorkerDeletion"></a>

### WorkerDeletion



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| undeployed | [bool](#bool) | optional | if true, the worker is already undeployed so deletion can proceed |






<a name="taskqueue-WorkerDetails"></a>

### WorkerDetails



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| worker_name | [string](#string) |  |  |
| job_id | [int32](#int32) |  |  |






<a name="taskqueue-WorkerEvent"></a>

### WorkerEvent



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) | optional | optional because a worker may fail before registration |
| worker_name | [string](#string) |  | free text (hostname or generated name) |
| level | [string](#string) |  | single letter: D/I/W/E |
| event_class | [string](#string) |  | e.g. &#34;install&#34;, &#34;bootstrap&#34;, &#34;runtime&#34;, &#34;network&#34; |
| message | [string](#string) |  | short human description |
| details_json | [string](#string) |  | raw JSON string (optional) |






<a name="taskqueue-WorkerEventFilter"></a>

### WorkerEventFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) | optional |  |
| level | [string](#string) | optional | &#39;D&#39;,&#39;I&#39;,&#39;W&#39;,&#39;E&#39; |
| class | [string](#string) | optional | event_class filter |
| limit | [int32](#int32) | optional | max number of events (default 50) |






<a name="taskqueue-WorkerEventId"></a>

### WorkerEventId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| event_id | [int32](#int32) |  |  |






<a name="taskqueue-WorkerEventList"></a>

### WorkerEventList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| events | [WorkerEventRecord](#taskqueue-WorkerEventRecord) | repeated |  |






<a name="taskqueue-WorkerEventPruneFilter"></a>

### WorkerEventPruneFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| before | [string](#string) | optional | RFC3339 UTC, e.g. &#34;2025-08-19T00:00:00Z&#34; |
| level | [string](#string) | optional | &#39;D&#39;|&#39;I&#39;|&#39;W&#39;|&#39;E&#39; |
| class | [string](#string) | optional | event_class exact match |
| worker_id | [int32](#int32) | optional |  |
| dry_run | [bool](#bool) |  | if true, count only; no deletion |






<a name="taskqueue-WorkerEventPruneResult"></a>

### WorkerEventPruneResult



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| matched | [int32](#int32) |  |  |
| deleted | [int32](#int32) |  |  |






<a name="taskqueue-WorkerEventRecord"></a>

### WorkerEventRecord



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| event_id | [int32](#int32) |  |  |
| created_at | [string](#string) |  |  |
| worker_id | [int32](#int32) | optional |  |
| worker_name | [string](#string) |  |  |
| level | [string](#string) |  |  |
| event_class | [string](#string) |  |  |
| message | [string](#string) |  |  |
| details_json | [string](#string) |  |  |






<a name="taskqueue-WorkerId"></a>

### WorkerId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |






<a name="taskqueue-WorkerIds"></a>

### WorkerIds



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workers_details | [WorkerDetails](#taskqueue-WorkerDetails) | repeated |  |






<a name="taskqueue-WorkerInfo"></a>

### WorkerInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| concurrency | [int32](#int32) | optional |  |
| is_permanent | [bool](#bool) | optional |  |
| provider | [string](#string) | optional |  |
| region | [string](#string) | optional |  |






<a name="taskqueue-WorkerRequest"></a>

### WorkerRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider_id | [int32](#int32) |  |  |
| flavor_id | [int32](#int32) |  |  |
| region_id | [int32](#int32) |  |  |
| number | [int32](#int32) |  |  |
| concurrency | [int32](#int32) |  |  |
| prefetch | [int32](#int32) |  |  |
| step_id | [int32](#int32) | optional |  |






<a name="taskqueue-WorkerStats"></a>

### WorkerStats



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| cpu_usage_percent | [float](#float) |  | 0-100, float |
| mem_usage_percent | [float](#float) |  | 0-100, float |
| load_1min | [float](#float) |  | e.g., 0.58, float |
| iowait_percent | [float](#float) |  | 0-100 float |
| disks | [DiskUsage](#taskqueue-DiskUsage) | repeated | Per-disk usage |
| disk_io | [DiskIOStats](#taskqueue-DiskIOStats) |  | Global disk IO (aggregated) |
| net_io | [NetIOStats](#taskqueue-NetIOStats) |  | Global network IO (aggregated) |






<a name="taskqueue-WorkerStatus"></a>

### WorkerStatus



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| status | [string](#string) |  |  |






<a name="taskqueue-WorkerStatusRequest"></a>

### WorkerStatusRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_ids | [int32](#int32) | repeated |  |






<a name="taskqueue-WorkerStatusResponse"></a>

### WorkerStatusResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| statuses | [WorkerStatus](#taskqueue-WorkerStatus) | repeated |  |






<a name="taskqueue-WorkerUpdateRequest"></a>

### WorkerUpdateRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [int32](#int32) |  |  |
| provider_id | [int32](#int32) | optional |  |
| flavor_id | [int32](#int32) | optional |  |
| region_id | [int32](#int32) | optional |  |
| concurrency | [int32](#int32) | optional |  |
| prefetch | [int32](#int32) | optional |  |
| step_id | [int32](#int32) | optional |  |
| is_permanent | [bool](#bool) | optional |  |
| recyclable_scope | [string](#string) | optional |  |






<a name="taskqueue-WorkersList"></a>

### WorkersList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workers | [Worker](#taskqueue-Worker) | repeated |  |






<a name="taskqueue-Workflow"></a>

### Workflow



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_id | [int32](#int32) |  |  |
| name | [string](#string) |  |  |
| run_strategy | [string](#string) |  |  |
| maximum_workers | [int32](#int32) | optional |  |






<a name="taskqueue-WorkflowFilter"></a>

### WorkflowFilter



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name_like | [string](#string) | optional |  |
| limit | [int32](#int32) | optional |  |
| offset | [int32](#int32) | optional |  |






<a name="taskqueue-WorkflowId"></a>

### WorkflowId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflow_id | [int32](#int32) |  |  |






<a name="taskqueue-WorkflowList"></a>

### WorkflowList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workflows | [Workflow](#taskqueue-Workflow) | repeated |  |






<a name="taskqueue-WorkflowRequest"></a>

### WorkflowRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| run_strategy | [string](#string) | optional |  |
| maximum_workers | [int32](#int32) | optional |  |






<a name="taskqueue-WorkspaceRootRequest"></a>

### WorkspaceRootRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider | [string](#string) |  | e.g., &#34;azure.default&#34; or &#34;openstack.ovh&#34; |
| region | [string](#string) |  | e.g., &#34;northeurope&#34; |






<a name="taskqueue-WorkspaceRootResponse"></a>

### WorkspaceRootResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| root_uri | [string](#string) |  | e.g., &#34;aznorth://workspace&#34; |





 

 

 


<a name="taskqueue-TaskQueue"></a>

### TaskQueue


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| SubmitTask | [TaskRequest](#taskqueue-TaskRequest) | [TaskResponse](#taskqueue-TaskResponse) |  |
| RegisterWorker | [WorkerInfo](#taskqueue-WorkerInfo) | [WorkerId](#taskqueue-WorkerId) |  |
| PingAndTakeNewTasks | [PingAndGetNewTasksRequest](#taskqueue-PingAndGetNewTasksRequest) | [TaskListAndOther](#taskqueue-TaskListAndOther) |  |
| UpdateTaskStatus | [TaskStatusUpdate](#taskqueue-TaskStatusUpdate) | [Ack](#taskqueue-Ack) |  |
| SendTaskLogs | [TaskLog](#taskqueue-TaskLog) stream | [Ack](#taskqueue-Ack) |  |
| StreamTaskLogsOutput | [TaskId](#taskqueue-TaskId) | [TaskLog](#taskqueue-TaskLog) stream |  |
| StreamTaskLogsErr | [TaskId](#taskqueue-TaskId) | [TaskLog](#taskqueue-TaskLog) stream |  |
| GetLogsChunk | [GetLogsRequest](#taskqueue-GetLogsRequest) | [LogChunkList](#taskqueue-LogChunkList) |  |
| ListTasks | [ListTasksRequest](#taskqueue-ListTasksRequest) | [TaskList](#taskqueue-TaskList) |  |
| RetryTask | [RetryTaskRequest](#taskqueue-RetryTaskRequest) | [TaskResponse](#taskqueue-TaskResponse) |  |
| ListWorkers | [ListWorkersRequest](#taskqueue-ListWorkersRequest) | [WorkersList](#taskqueue-WorkersList) |  |
| CreateWorker | [WorkerRequest](#taskqueue-WorkerRequest) | [WorkerIds](#taskqueue-WorkerIds) |  |
| UpdateWorkerStatus | [WorkerStatus](#taskqueue-WorkerStatus) | [Ack](#taskqueue-Ack) |  |
| DeleteWorker | [WorkerDeletion](#taskqueue-WorkerDeletion) | [JobId](#taskqueue-JobId) |  |
| UpdateWorker | [WorkerUpdateRequest](#taskqueue-WorkerUpdateRequest) | [Ack](#taskqueue-Ack) |  |
| GetWorkerStatuses | [WorkerStatusRequest](#taskqueue-WorkerStatusRequest) | [WorkerStatusResponse](#taskqueue-WorkerStatusResponse) |  |
| ListJobs | [ListJobsRequest](#taskqueue-ListJobsRequest) | [JobsList](#taskqueue-JobsList) |  |
| GetJobStatuses | [JobStatusRequest](#taskqueue-JobStatusRequest) | [JobStatusResponse](#taskqueue-JobStatusResponse) |  |
| DeleteJob | [JobId](#taskqueue-JobId) | [Ack](#taskqueue-Ack) |  |
| UpdateJob | [JobUpdate](#taskqueue-JobUpdate) | [Ack](#taskqueue-Ack) |  |
| ListFlavors | [ListFlavorsRequest](#taskqueue-ListFlavorsRequest) | [FlavorsList](#taskqueue-FlavorsList) |  |
| ListProviders | [.google.protobuf.Empty](#google-protobuf-Empty) | [ProviderList](#taskqueue-ProviderList) |  |
| ListRegions | [.google.protobuf.Empty](#google-protobuf-Empty) | [RegionList](#taskqueue-RegionList) |  |
| CreateFlavor | [FlavorCreateRequest](#taskqueue-FlavorCreateRequest) | [FlavorId](#taskqueue-FlavorId) |  |
| GetRcloneConfig | [.google.protobuf.Empty](#google-protobuf-Empty) | [RcloneRemotes](#taskqueue-RcloneRemotes) |  |
| GetDockerCredentials | [.google.protobuf.Empty](#google-protobuf-Empty) | [DockerCredentials](#taskqueue-DockerCredentials) |  |
| Login | [LoginRequest](#taskqueue-LoginRequest) | [LoginResponse](#taskqueue-LoginResponse) |  |
| Logout | [Token](#taskqueue-Token) | [Ack](#taskqueue-Ack) |  |
| CreateUser | [CreateUserRequest](#taskqueue-CreateUserRequest) | [UserId](#taskqueue-UserId) |  |
| ListUsers | [.google.protobuf.Empty](#google-protobuf-Empty) | [UsersList](#taskqueue-UsersList) |  |
| DeleteUser | [UserId](#taskqueue-UserId) | [Ack](#taskqueue-Ack) |  |
| UpdateUser | [User](#taskqueue-User) | [Ack](#taskqueue-Ack) |  |
| ChangePassword | [ChangePasswordRequest](#taskqueue-ChangePasswordRequest) | [Ack](#taskqueue-Ack) |  |
| ListRecruiters | [RecruiterFilter](#taskqueue-RecruiterFilter) | [RecruiterList](#taskqueue-RecruiterList) |  |
| CreateRecruiter | [Recruiter](#taskqueue-Recruiter) | [Ack](#taskqueue-Ack) |  |
| UpdateRecruiter | [RecruiterUpdate](#taskqueue-RecruiterUpdate) | [Ack](#taskqueue-Ack) |  |
| DeleteRecruiter | [RecruiterId](#taskqueue-RecruiterId) | [Ack](#taskqueue-Ack) |  |
| ListWorkflows | [WorkflowFilter](#taskqueue-WorkflowFilter) | [WorkflowList](#taskqueue-WorkflowList) |  |
| CreateWorkflow | [WorkflowRequest](#taskqueue-WorkflowRequest) | [WorkflowId](#taskqueue-WorkflowId) |  |
| DeleteWorkflow | [WorkflowId](#taskqueue-WorkflowId) | [Ack](#taskqueue-Ack) |  |
| ListSteps | [StepFilter](#taskqueue-StepFilter) | [StepList](#taskqueue-StepList) |  |
| CreateStep | [StepRequest](#taskqueue-StepRequest) | [StepId](#taskqueue-StepId) |  |
| DeleteStep | [StepId](#taskqueue-StepId) | [Ack](#taskqueue-Ack) |  |
| GetStepStats | [StepStatsRequest](#taskqueue-StepStatsRequest) | [StepStatsResponse](#taskqueue-StepStatsResponse) |  |
| GetWorkerStats | [GetWorkerStatsRequest](#taskqueue-GetWorkerStatsRequest) | [GetWorkerStatsResponse](#taskqueue-GetWorkerStatsResponse) |  |
| FetchList | [FetchListRequest](#taskqueue-FetchListRequest) | [FetchListResponse](#taskqueue-FetchListResponse) |  |
| FetchInfo | [FetchListRequest](#taskqueue-FetchListRequest) | [FetchInfoResponse](#taskqueue-FetchInfoResponse) |  |
| UploadTemplate | [UploadTemplateRequest](#taskqueue-UploadTemplateRequest) | [UploadTemplateResponse](#taskqueue-UploadTemplateResponse) | Template system |
| RunTemplate | [RunTemplateRequest](#taskqueue-RunTemplateRequest) | [TemplateRun](#taskqueue-TemplateRun) |  |
| ListTemplates | [TemplateFilter](#taskqueue-TemplateFilter) | [TemplateList](#taskqueue-TemplateList) |  |
| ListTemplateRuns | [TemplateRunFilter](#taskqueue-TemplateRunFilter) | [TemplateRunList](#taskqueue-TemplateRunList) |  |
| UpdateTemplateRun | [UpdateTemplateRunRequest](#taskqueue-UpdateTemplateRunRequest) | [Ack](#taskqueue-Ack) |  |
| DeleteTemplateRun | [DeleteTemplateRunRequest](#taskqueue-DeleteTemplateRunRequest) | [Ack](#taskqueue-Ack) |  |
| GetWorkspaceRoot | [WorkspaceRootRequest](#taskqueue-WorkspaceRootRequest) | [WorkspaceRootResponse](#taskqueue-WorkspaceRootResponse) |  |
| RegisterSpecifications | [ResourceSpec](#taskqueue-ResourceSpec) | [Ack](#taskqueue-Ack) |  |
| ReportWorkerEvent | [WorkerEvent](#taskqueue-WorkerEvent) | [Ack](#taskqueue-Ack) | Worker events |
| ListWorkerEvents | [WorkerEventFilter](#taskqueue-WorkerEventFilter) | [WorkerEventList](#taskqueue-WorkerEventList) |  |
| DeleteWorkerEvent | [WorkerEventId](#taskqueue-WorkerEventId) | [Ack](#taskqueue-Ack) |  |
| PruneWorkerEvents | [WorkerEventPruneFilter](#taskqueue-WorkerEventPruneFilter) | [WorkerEventPruneResult](#taskqueue-WorkerEventPruneResult) |  |

 



## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |

