# CLAUDE.md

## Proto stub generation

**Always use `make proto-all` (or `make proto-python`, `make proto-ui`, `make proto`) to regenerate gRPC stubs.** Never run `protoc` directly — the Makefile includes post-processing fixups (e.g. converting absolute imports to relative imports in Python stubs) that are required for the code to work.

If `make proto-python` fails because `grpc_tools` is not found, use the venv python explicitly:
```sh
cd python && ./venv/bin/python -m grpc_tools.protoc ... && sed -i '' 's/^import taskqueue_pb2/from . import taskqueue_pb2/' src/scitq2/pb/taskqueue_pb2_grpc.py
```

After regenerating Python stubs, update `python/pyproject.toml` to match the `GRPC_GENERATED_VERSION` in `python/src/scitq2/pb/taskqueue_pb2_grpc.py`. The generated stubs enforce a minimum grpcio version at runtime.

## Operating procedures

See `AISOPs/` for standard operating procedures that govern AI behavior.

## Coding style

See `MANTRA.md` for the project's coding principles.
