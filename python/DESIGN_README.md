# pyscitq2 Design Summary

## 🧠 Purpose

`pyscitq2` is a Python DSL that allows users to define and submit `scitq2` workflows to a Go backend via gRPC. Python handles **definition and submission**, but **not orchestration** (which is handled by the Go engine).

---

## ✅ System Overview

- The Python DSL (`pyscitq2`) is a thin layer over:
  - gRPC calls to the Go `scitq2` backend
  - High-level Python constructs: `Workflow`, `Step`, `WorkerPool`, `TaskSpec`, etc.

- Script lifecycle:
  1. User defines a `Workflow` using the DSL.
  2. The Python layer transforms the high-level structure into low-level gRPC messages.
  3. Calls to Go backend:
     - `CreateWorkflow(...)`
     - `CreateStep(...)`
     - `SubmitTask(...)`

---

## ✅ gRPC Integration

- `.proto` file is stored at: `proto/taskqueue.proto`
- Python gRPC stubs are generated using:

  ```bash
  python -m grpc_tools.protoc \
    -I proto \
    --python_out=src/scitq2/pb \
    --grpc_python_out=src/scitq2/pb \
    proto/taskqueue.proto
  ```

Generated files:

- `src/scitq2/pb/taskqueue_pb2.py`
- `src/scitq2/pb/taskqueue_pb2_grpc.py`

--

## ✅ Submission Layer

- `grpc_client.py` provides:

  ```python
  create_workflow(name: str, ...) -> workflow_id
  create_step(workflow_id: int, name: str) -> step_id
  submit_task(step_id: int, command: str, container: str, ...) -> task_id
  ```

- `compiler.py` traverses the `Workflow` object and calls `grpc_client`.

