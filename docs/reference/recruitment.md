# Recruitment rules

Recruiters give a step the workers it needs: they move idle workers onto the
step (*recycling*) and deploy new cloud workers (*deploying*). This page
states the rules the recruitment cycle follows. The server runs that cycle
every few seconds.

## A step's recruiters work as one

A step can have several recruiters, ranked 1, 2, 3… Every rule applies to the
step as a whole: how much capacity it asks for, how many workers it may hold,
how its shortfall is shared. Ranks only order the sources of workers — for
example rank 1 recycles local machines, rank 2 deploys in one cloud region,
rank 3 in another.

## How much capacity a step asks for

A step asks for enough free task slots to run its pending tasks in `rounds`
rounds:

    wanted slots = ceil(pending tasks / rounds)

Free slots are counted over every worker on the step: its concurrency minus
the tasks it is already running. When free slots fall short of wanted slots,
the step recruits for the difference.

With 100 pending tasks and workers of concurrency 10, `rounds: 1` asks for 100
slots (10 workers, everything in one go), `rounds: 2` for 50 slots (5 workers,
each running two batches). Raise `rounds` when tasks have a heavy start-up
(large downloads, long index loading): fewer workers, each reused longer.

`rounds` is one value per step. All recruiters of a step use the same value:
creating a recruiter whose `rounds` differs from the step's other recruiters
is refused, and updating `rounds` on one recruiter updates all recruiters of
the step.

## Ranks: who recruits first

On each cycle the step's shortfall is offered to rank 1, then rank 2, and so
on. A rank first recycles idle workers that match its filter, then, if the
step is still short, deploys new cloud workers once its timeout has passed.

A rank acts only when every lower rank is **done**. A rank is done when any
of these holds:

- the step already has as many workers as the rank's `max_workers`;
- the rank has nothing it can recycle or deploy: no available flavor matches
  its filter, the workflow is at its maximum number of workers, or every
  matching flavor is out of quota or blacklisted;
- it tried to deploy and deployed nothing.

While a lower rank waits out its timeout, or has just deployed workers, the
higher ranks wait. The next cycle counts the new workers as step capacity, so
a higher rank only covers what is still missing once they are there.

## Timeout: deploys wait, recycling does not

A rank's `timeout` is the delay before it deploys new cloud workers. It starts
when the rank becomes eligible — the step is short and every lower rank is
done — and restarts after each deploy. When the rank stops being eligible
(the shortfall is covered, or a lower rank becomes active again), its timeout
is cleared, and it starts afresh the next time the rank becomes eligible.

The timeout gives idle and permanent workers time to absorb new tasks before
money is spent on new machines. Recycling never waits for it.

## Limits

- **`max_workers` on a recruiter counts every worker on the step**, whatever
  brought it there: workers recycled or deployed by any rank, and workers
  assigned by hand. It is not a quota of workers owned by that recruiter. A
  rank with `max_workers: 2` on a step that already has two workers — even
  hand-assigned ones its own filter would never match — is done before it
  starts, and the step's shortfall passes to the next rank. To give a rank
  room, set its `max_workers` above the number of workers you expect on the
  step before it acts.
- **The workflow's maximum number of workers** caps cloud deploys for the
  whole workflow. Recycling may exceed it, because recycled workers already
  exist. Creating or updating a recruiter with a `max_workers` above the
  workflow's maximum raises the workflow's maximum to match.
- **Where these are set.** In a template, `max_recruited` on the
  workflow-level `worker_pool` sets the workflow's maximum and, for every
  step using that pool, the recruiter's `max_workers`; `max_recruited` on a
  step's own `worker_pool` sets only that step's recruiter. For recruiters
  added by hand, `--max-workers` (CLI) or `max_workers` (MCP).
- **Provider quotas** (`scitq.yaml`, per provider and region) cap what can be
  deployed in each region. See the [configuration reference](configuration.md) (`quotas` of each provider).

## Recycling

A worker can be recycled onto a step when it is running, its flavor and
region match the recruiter's filter, it has free slots, and its recyclable
scope allows it: scope `G` (any workflow) or scope `W` (the same workflow
only). Its swap proportion and extra storage must also match the recruiter's
settings, since those are fixed when a worker is deployed. A worker stays
where it is while its current step still has more pending tasks than free
slots.

A recycled worker takes the recruiter's concurrency and prefetch: fixed
values when the recruiter sets them, otherwise computed from the per-task
needs (see below). Workers deployed or recycled by a workflow's recruiters
get scope `W`; when the workflow finishes they become `G`, available to any
workflow.

## Concurrency of recruited workers

A recruiter either sets a fixed `concurrency`, or derives it from per-task
needs: the worker's CPUs divided by `cpu_per_task`, its memory by
`memory_per_task`, its disk by `disk_per_task` and its GPUs by
`gpu_per_task` — the smallest of these, rounded down, then kept within
`concurrency_min` and `concurrency_max`. Prefetch is either fixed or a
percentage of that concurrency.

## Seeing what happened

Recruitment decisions are in the [audit trail](../usage/cli.md#audit): each
deploy and recycle names the recruiter that made it (for example
`recruiter step 75230 rank 2`), and recruiter changes are recorded on their
step.

```sh
scitq audit list --worker 7563        # who deployed or moved this worker
scitq audit list --step 75230         # recruiter changes on this step
```
