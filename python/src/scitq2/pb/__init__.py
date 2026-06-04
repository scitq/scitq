# Marker file so setuptools' find_packages picks up this directory as a
# regular subpackage of scitq2. Without it the wheel ships scitq2/ but not
# scitq2/pb/, and `from scitq2.pb import taskqueue_pb2` fails at import
# time with ModuleNotFoundError. The directory contains generated gRPC
# stubs (taskqueue_pb2.py / taskqueue_pb2_grpc.py), so an empty
# __init__.py is the right shape — no re-exports needed.
