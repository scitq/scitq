import { writable } from 'svelte/store';

// Sideband store for per-workflow task counters.
//
// Why this exists: WorkflowPage receives step-stats snapshots from the
// server several times a second on a busy production server. The natural place to
// keep counters is on the workflow object itself, but `workflows[idx]
// = updated` invalidates the entire workflows array, which forces
// WorkflowList's keyed-each to re-run block.p for every row. Even
// with no real prop changes, Svelte 4's `safe_not_equal` treats every
// object/Map prop as "changed" and fires $set on every child
// component (StepList instance, lucide-svelte icons, ...). On the
// workflows page that cascade allocated 1+ GB in a single delta and
// crashed the renderer.
//
// By writing counter updates into this Map store instead, only the
// counter-reading bindings in WorkflowList re-evaluate. The
// keyed-each itself doesn't re-iterate (workflows isn't dirty), child
// component $set is not called, and the cascade stops.
//
// The store is keyed by workflowId. Values are seeded from each
// workflow object when it's loaded, then replaced by the workflow totals
// each step-stats snapshot carries. Both come from the server's step-stats
// aggregator, so they agree.

export interface WfCounters {
  totalTasks: number;
  succeededTasks: number;
  failedTasks: number;
  runningTasks: number;
  retryingTasks: number;
}

function counters(c: Partial<WfCounters>): WfCounters {
  return {
    totalTasks: c.totalTasks ?? 0,
    succeededTasks: c.succeededTasks ?? 0,
    failedTasks: c.failedTasks ?? 0,
    runningTasks: c.runningTasks ?? 0,
    retryingTasks: c.retryingTasks ?? 0,
  };
}

function createWfCountersStore() {
  const { subscribe, update, set } = writable<Map<number, WfCounters>>(new Map());

  return {
    subscribe,
    // Set a workflow's counters (from getWorkFlow or a snapshot).
    seed(workflowId: number, c: WfCounters) {
      update((m) => m.set(workflowId, counters(c)));
    },
    // Seed many at once, e.g. on initial workflows load / "load more".
    seedMany(items: Array<{ workflowId: number } & WfCounters>) {
      update((m) => {
        for (const item of items) m.set(item.workflowId, counters(item));
        return m;
      });
    },
    // Drop a workflow from the store (called when a workflow is
    // removed from the list, e.g. via delete).
    drop(workflowId: number) {
      update((m) => {
        m.delete(workflowId);
        return m;
      });
    },
    clear() {
      set(new Map());
    },
  };
}

export const wfCounters = createWfCountersStore();
