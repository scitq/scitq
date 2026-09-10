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
[✅] migrate UI to Svelte 5 runes mode — we're already on Svelte 5.27 but in legacy mode, which preserves Svelte 4 reactivity (incl. the `safe_not_equal` cascade we hit on the workflows page). Runes (`$state`, `$derived`, `$effect`) would make that class of bug impossible. Driver script ready at `tools/svelte5_migrate_driver.py`; budget ~a weekend for the silent-bug hunt after the mechanical pass. Sideband-store workaround stays as defence in depth.

# TODO later

[ ] filtered worker list on step view — the step's "Workers" column currently lists every worker whose `worker.step_id` matches, including ones the resource-fit check excludes from assignment (production case: bigbrother 15.6 GB attached to a hermes step demanding 20 GB → silently never picks up tasks). The `worker_event` warning shipped in `assigntask.go:maybeWarnNoFit` makes the cause discoverable; this followup makes the step view truthful by hiding (or visually demoting — greyed-out + "incompatible" tag) workers whose flavor caps fail `fitsWorker` against the step's current `task_spec` minimums. Cheap server-side: same comparison the scheduler already does.
[ ] concurrency=0 worker visualisation — when `worker.concurrency==0` the assignment SQL's `HAVING SUM < concurrency+prefetch` excludes the worker silently, so manually-recruited workers can sit idle forever until the operator notices. Plan: render the worker as yellow (same pause indicator) when `status=='P' OR concurrency==0`, with a tooltip that differentiates the two ("paused by operator" vs "concurrency 0 — set a positive value to start"). The play button on a `R+concurrency==0` worker opens a modal prompting the operator to raise concurrency before unpausing. Pure UI change — no server schema/status changes (which would force inventing a P→? auto-restore state machine).
[ ] implement some timeouts for workflow template scripts
[ ] implement debug mode
[ ] implement workflow strategy (sticky)
[ ] heterogeneous worker pools per step — currently a step has one `worker_pool` and one `task_spec`, which assumes every worker serving the step shares the same per-task budget shape. Some workloads would benefit from declaring N pools per step with paired task_specs (e.g. a NUMA-bound megahit step that recruits both EPYC 7451 boxes and Intel single-socket machines, each with its own `task_spec.numa` derivation). Goes beyond NUMA — same model unlocks "GPU pool + CPU pool serving the same step" and similar. Worth doing once a real workload demands it.
[ ] chaining workflow v2 — next iteration of the workflow_chain feature shipped in commit 49d6a19 (`specs/workflow_chain.md`). Scope TBD when a real need surfaces.
[ ] workflow ↔ tasks status drift — two symmetric shapes of `recomputeWorkflowStatus` not converging, both observed during the 09-10 metrics cleanup:
  - **R with 0 pending / 0 running / all tasks S** — should have flipped to S but didn't. Observed on `gpredomics0.IOScope3a.3` (workflow 764) and `.4` (769); `live=false`, so not the live latch. Suspect the last S→recompute call was missed (server restart mid-flight, or CTE's `total = COUNT(*) FILTER (NOT hidden)` returning 0 when every task row was hidden — `total=0` skips the transition).
  - **S / F with pending P or W tasks** — the reverse, observed on `upload-kraken2-db` (1351), `gpredomics-optuna-ICI-IOScope3d` (1751), and several others. The workflow was declared succeeded/failed while stragglers were still queued. Either the recompute fired on stale counts before all tasks landed, or the workflow was moved to S by a different code path (extend / retry?) that didn't re-check task state.

Both bugs feed the same Prometheus alert (`scitq_task_pending_seconds_max`), which is how we found them.

Diagnostic query for R-with-0-pending:
```
SELECT status, COUNT(*) FILTER (WHERE NOT hidden) AS visible, COUNT(*) AS total
  FROM task t JOIN step s ON s.step_id=t.step_id
 WHERE s.workflow_id = <ID> GROUP BY status;
```
If `visible=0` on all rows but `total>0`, the CTE at server.go:855 is the culprit — add a branch that flips R→S (or R→F if there are hidden F rows) when the workflow has tasks but none visible. Manual clear meanwhile: `UPDATE workflow SET status='S' WHERE workflow_id=<ID>`.

Diagnostic query for orphan pending in finalized workflows:
```
SELECT w.workflow_id, w.workflow_name, w.status AS wf_status,
       COUNT(*) FILTER (WHERE t.status='P') AS p, COUNT(*) FILTER (WHERE t.status='W') AS w
  FROM task t JOIN step s ON s.step_id=t.step_id JOIN workflow w ON w.workflow_id=s.workflow_id
 WHERE t.status IN ('P','W') AND NOT t.hidden AND w.status IN ('S','F')
 GROUP BY w.workflow_id, w.workflow_name, w.status ORDER BY 1;
```
Fix idea: `recomputeWorkflowStatus` should ALSO run in reverse — after any task transition into P/W, if the parent workflow is already S/F, either promote the workflow back to R or (better) terminate the stranded task automatically. Manual clear meanwhile: `UPDATE task SET status='F' WHERE task_id IN (...)` or delete the task rows.

