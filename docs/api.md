# Protocol Documentation
<a name="top"></a>

## Table of Contents

- [proto/taskqueue.proto](#proto_taskqueue-proto)
    - [Ack](#taskqueue-Ack)
    - [ListTasksRequest](#taskqueue-ListTasksRequest)
    - [ListWorkersRequest](#taskqueue-ListWorkersRequest)
    - [Task](#taskqueue-Task)
    - [TaskId](#taskqueue-TaskId)
    - [TaskList](#taskqueue-TaskList)
    - [TaskListAndOther](#taskqueue-TaskListAndOther)
    - [TaskLog](#taskqueue-TaskLog)
    - [TaskRequest](#taskqueue-TaskRequest)
    - [TaskResponse](#taskqueue-TaskResponse)
    - [TaskStatusUpdate](#taskqueue-TaskStatusUpdate)
    - [Worker](#taskqueue-Worker)
    - [WorkerId](#taskqueue-WorkerId)
    - [WorkerIds](#taskqueue-WorkerIds)
    - [WorkerInfo](#taskqueue-WorkerInfo)
    - [WorkerRequest](#taskqueue-WorkerRequest)
    - [WorkersList](#taskqueue-WorkersList)
  
    - [TaskQueue](#taskqueue-TaskQueue)
  
- [Scalar Value Types](#scalar-value-types)



<a name="proto_taskqueue-proto"></a>
<p align="right"><a href="#top">Top</a></p>

## proto/taskqueue.proto



<a name="taskqueue-Ack"></a>

### Ack



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| success | [bool](#bool) |  |  |






<a name="taskqueue-ListTasksRequest"></a>

### ListTasksRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| status_filter | [string](#string) | optional |  |






<a name="taskqueue-ListWorkersRequest"></a>

### ListWorkersRequest







<a name="taskqueue-Task"></a>

### Task



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [uint32](#uint32) |  |  |
| command | [string](#string) |  |  |
| container | [string](#string) |  |  |
| status | [string](#string) |  |  |






<a name="taskqueue-TaskId"></a>

### TaskId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [uint32](#uint32) |  |  |






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
| concurrency | [uint32](#uint32) |  |  |






<a name="taskqueue-TaskLog"></a>

### TaskLog



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [uint32](#uint32) |  |  |
| log_type | [string](#string) |  | &#39;O&#39; for stdout, &#39;E&#39; for stderr |
| log_text | [string](#string) |  |  |






<a name="taskqueue-TaskRequest"></a>

### TaskRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| command | [string](#string) |  |  |
| container | [string](#string) |  |  |






<a name="taskqueue-TaskResponse"></a>

### TaskResponse



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [uint32](#uint32) |  |  |






<a name="taskqueue-TaskStatusUpdate"></a>

### TaskStatusUpdate



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| task_id | [uint32](#uint32) |  |  |
| new_status | [string](#string) |  |  |






<a name="taskqueue-Worker"></a>

### Worker



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [uint32](#uint32) |  |  |
| name | [string](#string) |  |  |
| concurrency | [uint32](#uint32) |  |  |






<a name="taskqueue-WorkerId"></a>

### WorkerId



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_id | [uint32](#uint32) |  |  |






<a name="taskqueue-WorkerIds"></a>

### WorkerIds



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| worker_ids | [uint32](#uint32) | repeated |  |






<a name="taskqueue-WorkerInfo"></a>

### WorkerInfo



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| name | [string](#string) |  |  |
| concurrency | [uint32](#uint32) | optional |  |






<a name="taskqueue-WorkerRequest"></a>

### WorkerRequest



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| provider | [string](#string) |  |  |
| flavor | [string](#string) |  |  |
| number | [uint32](#uint32) |  |  |
| concurrency | [uint32](#uint32) |  |  |
| prefetch | [uint32](#uint32) |  |  |
| step_id | [uint32](#uint32) |  |  |
| flavor_id | [uint32](#uint32) |  |  |
| region_id | [uint32](#uint32) |  |  |






<a name="taskqueue-WorkersList"></a>

### WorkersList



| Field | Type | Label | Description |
| ----- | ---- | ----- | ----------- |
| workers | [Worker](#taskqueue-Worker) | repeated |  |





 

 

 


<a name="taskqueue-TaskQueue"></a>

### TaskQueue


| Method Name | Request Type | Response Type | Description |
| ----------- | ------------ | ------------- | ------------|
| SubmitTask | [TaskRequest](#taskqueue-TaskRequest) | [TaskResponse](#taskqueue-TaskResponse) |  |
| RegisterWorker | [WorkerInfo](#taskqueue-WorkerInfo) | [WorkerId](#taskqueue-WorkerId) |  |
| PingAndTakeNewTasks | [WorkerId](#taskqueue-WorkerId) | [TaskListAndOther](#taskqueue-TaskListAndOther) |  |
| UpdateTaskStatus | [TaskStatusUpdate](#taskqueue-TaskStatusUpdate) | [Ack](#taskqueue-Ack) |  |
| SendTaskLogs | [TaskLog](#taskqueue-TaskLog) stream | [Ack](#taskqueue-Ack) |  |
| StreamTaskLogs | [TaskId](#taskqueue-TaskId) | [TaskLog](#taskqueue-TaskLog) stream |  |
| ListTasks | [ListTasksRequest](#taskqueue-ListTasksRequest) | [TaskList](#taskqueue-TaskList) |  |
| ListWorkers | [ListWorkersRequest](#taskqueue-ListWorkersRequest) | [WorkersList](#taskqueue-WorkersList) |  |
| CreateWorker | [WorkerRequest](#taskqueue-WorkerRequest) | [WorkerIds](#taskqueue-WorkerIds) |  |
| DeleteWorker | [WorkerId](#taskqueue-WorkerId) | [Ack](#taskqueue-Ack) |  |

 



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

