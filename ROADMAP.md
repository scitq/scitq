# Recently done

[✅] implement SSL certificate
[✅] implement "onhold" status for workflow template (so that the workflow starts only when it is ready)
[✅] implement task_dependancies (SQL table exists but that's it for now)
[✅] add local worker recruitment
  [✅] Ensure "local" provider and region exist
  [✅] Add a new gRPC endpoint: RegisterSpecifications(ResourceSpec)
  [✅] Implement server-side logic for RegisterSpecifications
  [✅] Implement client-side logic for RegisterSpecifications
  [✅] Test recruitment and worker visibility
[✅] add proper version in code, report in UI
[✅] add reporting of error of client (called worker events) in the server, accessible by CLI
[✅] add OVH support
[✅] check task retries
[✅] recruitment / flavor-blacklist follow-ups (after Lucie's 2026-05-04 `Standard_DC32ads_cc_v5` incident)
  [✅] config-level `flavor_include_patterns` / `flavor_exclude_patterns` per provider
  [✅] `persistFlavorBlacklist` on `unsupported_flavor` failures
  [✅] stuck-delete janitor (`server/stuck_delete_cleanup.go`) + `scitq worker delete --undeployed` CLI/MCP flag
[✅] fix UI freeze on workflows page (sideband counter store in `ui/src/lib/wfCounters.ts`; bypasses Svelte 4 `safe_not_equal` cascade)

# TODO short term


[✅] show worker event in UI
[✅] step view stats in UI
[◒] fix web sockets
  [✅] Fix concurrent-write hazard (writer pump per connection)
  [ ] Add event envelope with monotonic event_id + server ring buffer (to replay missed events)
  [ ] wsClient.ts small upgrades: lastEventId tracking/since, single dispatcher -> route by msg.type
[✅] implement download/execution/upload timeout in client (using either config setting or database content for the task)
[✅] add access to timeout in python DSL
[✅] add run duration measurement
[✅] add helper as `source /resource/shell_helpers.sh` (or `source /builtinresource/shell_helpers.sh` ?), rather than copying it in all scripts, same for find_pairs, (`source /builtinresource/shell_biology.sh`)

# TODO later

[ ] implement some timeouts for workflow template scripts
[ ] implement debug mode
[ ] implement workflow strategy (sticky)
[ ] heterogeneous worker pools per step — currently a step has one `worker_pool` and one `task_spec`, which assumes every worker serving the step shares the same per-task budget shape. Some workloads would benefit from declaring N pools per step with paired task_specs (e.g. a NUMA-bound megahit step that recruits both EPYC 7451 boxes and Intel single-socket machines, each with its own `task_spec.numa` derivation). Goes beyond NUMA — same model unlocks "GPU pool + CPU pool serving the same step" and similar. Worth doing once a real workload demands it.
[ ] chaining workflow v2 — next iteration of the workflow_chain feature shipped in commit 49d6a19 (`specs/workflow_chain.md`). Scope TBD when a real need surfaces.
[ ] migrate UI to Svelte 5 runes mode — we're already on Svelte 5.27 but in legacy mode, which preserves Svelte 4 reactivity (incl. the `safe_not_equal` cascade we hit on the workflows page). Runes (`$state`, `$derived`, `$effect`) would make that class of bug impossible. Driver script ready at `tools/svelte5_migrate_driver.py`; budget ~a weekend for the silent-bug hunt after the mechanical pass. Sideband-store workaround stays as defence in depth.
