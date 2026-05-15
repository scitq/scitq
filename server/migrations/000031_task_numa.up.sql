-- numa: number of NUMA nodes the task should be pinned to on its
-- worker. NULL = NUMA-unaware (the default; existing behavior). When
-- set, the worker translates this into --cpuset-cpus / --cpuset-mems
-- on docker run, and derives its semaphore size from the host's NUMA
-- topology (floor(host_nodes / numa)) rather than from task_spec.cpu.
-- See TaskRequest.numa in proto/taskqueue.proto.
ALTER TABLE task
    ADD COLUMN numa INT NULL;
