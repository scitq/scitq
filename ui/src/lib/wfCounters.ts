import { writable } from 'svelte/store';

// Sideband store for per-workflow task counters.
//
// Why this exists: WorkflowPage receives step-stats deltas from the
// server at ~1/min on a busy production server. The natural place to
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
// The store is keyed by workflowId. Initial values are seeded from
// each workflow object when it's first loaded; subsequent step-stats
// deltas update the entry in place.

export interface WfCounters {
  totalTasks: number;
  succeededTasks: number;
  failedTasks: number;
  runningTasks: number;
  retryingTasks: number;
}

function createWfCountersStore() {
  const { subscribe, update, set } = writable<Map<number, WfCounters>>(new Map());

  return {
    subscribe,
    // Seed initial counters for a workflow (from getWorkFlow response).
    // Idempotent: overwrites any prior entry for the same id.
    seed(workflowId: number, c: WfCounters) {
      update((m) => {
        m.set(workflowId, { ...c });
        return m;
      });
    },
    // Seed many at once, e.g. on initial workflows load / "load more".
    seedMany(items: Array<{ workflowId: number } & WfCounters>) {
      update((m) => {
        for (const item of items) {
          m.set(item.workflowId, {
            totalTasks: item.totalTasks ?? 0,
            succeededTasks: item.succeededTasks ?? 0,
            failedTasks: item.failedTasks ?? 0,
            runningTasks: item.runningTasks ?? 0,
            retryingTasks: item.retryingTasks ?? 0,
          });
        }
        return m;
      });
    },
    // Apply a step-stats delta. Mirrors the old in-place
    // counter-update logic from WorkflowPage's handleMessage.
    applyDelta(workflowId: number, oldStatus: string | undefined, newStatus: string | undefined, isRetry: boolean) {
      update((m) => {
        const cur = m.get(workflowId);
        if (!cur) return m; // workflow not loaded → drop the delta
        const next: WfCounters = { ...cur };

        if (oldStatus === 'S') next.succeededTasks = Math.max(0, (next.succeededTasks || 0) - 1);
        else if (oldStatus === 'F') next.failedTasks = Math.max(0, (next.failedTasks || 0) - 1);
        else if (oldStatus && ['A', 'C', 'D', 'O', 'R', 'U', 'V'].includes(oldStatus)) next.runningTasks = Math.max(0, (next.runningTasks || 0) - 1);

        if (newStatus === 'S') next.succeededTasks = (next.succeededTasks || 0) + 1;
        else if (newStatus === 'F') next.failedTasks = (next.failedTasks || 0) + 1;
        else if (newStatus && ['A', 'C', 'D', 'O', 'R', 'U', 'V'].includes(newStatus)) next.runningTasks = (next.runningTasks || 0) + 1;

        if (isRetry && !oldStatus) next.retryingTasks = (next.retryingTasks || 0) + 1;
        if (isRetry && (newStatus === 'S' || newStatus === 'F')) next.retryingTasks = Math.max(0, (next.retryingTasks || 0) - 1);

        if (!oldStatus) next.totalTasks = (next.totalTasks || 0) + 1;

        m.set(workflowId, next);
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
