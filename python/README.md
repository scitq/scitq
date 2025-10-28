# pyscitq
Python workflow lib for scitq2

## Description
This library is a python DSL for scitq v2 engine. It enables the creation of workflow in a simple and clear python code that is self-descriptive.

## gRPC update

- Copy scitq2 .proto file in this repo proto folder
- make sure you have the right python package:
```sh
pip install grpcio grpcio-tools
```
- Type in the following command:
```sh
python -m grpc_tools.protoc \
  -I proto \
  --python_out=src/scitq2/pb \
  --grpc_python_out=src/scitq2/pb \
  --proto_path=proto \
  --experimental_allow_proto3_optional \
  proto/taskqueue.proto
sed -i '' 's/^import taskqueue_pb2/from . import taskqueue_pb2/' src/scitq2/pb/taskqueue_pb2_grpc.py
```
