<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as grpcWeb from 'grpc-web';
  import { getStepStats, delStep } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import { RefreshCw, PauseCircle, CircleX, Eraser } from 'lucide-svelte';
  import { formatDuration, showIfNonZero } from '../lib/format';

  /**
   * Workflow ID passed as a prop to the component
   * @type {number}
   */
  export let workflowId: number;
  export let workersPerStepId: Map<number, taskqueue.Worker[]> = new Map();

  // Local, reactive copy of the parent-provided map to avoid mutating props
  let workersByStep: Map<number, taskqueue.Worker[]> = new Map();
  $: if (workersPerStepId) {
    // Recreate the map to preserve Svelte reactivity and avoid sharing references
    workersByStep = new Map(workersPerStepId);
  }

  /**
   * Array of loaded steps for the workflow
   * @type {Array<Object>}
   */
  let steps = [];

  /**
   * Array of pending steps waiting to be loaded
   * @type {Array<Object>}
   */
  let pendingSteps = [];

  /**
   * Flag indicating if more steps are available to load
   * @type {boolean}
   */
  let hasMoreSteps = true;

  /**
   * Loading state flag
   * @type {boolean}
   */
  let isLoading = false;

  /**
   * Reference to the table container element
   * @type {HTMLDivElement}
   */
  let tableContainer: HTMLDivElement;

  /**
   * Flag indicating if the table is scrolled to top
   * @type {boolean}
   */
  let isScrolledToTop = true;

  /**
   * Count of new steps available
   * @type {number}
   */
  let newStepsCount = 0;

  /**
   * Flag to show new steps notification
   * @type {boolean}
   */
  let showNewStepsNotification = false;

  /**
   * Last scroll position of the table
   * @type {{top: number, left: number}}
   */
  let lastScrollPosition = { top: 0, left: 0 };

  /**
   * Number of steps to load at once
   * @type {number}
   * @constant
   */
  const STEPS_CHUNK_SIZE = 25;

  /**
   * Unsubscribe function for WebSocket messages
   * @type {Function}
   */
  let unsubscribeWS: () => void;

  // Track running tasks per step: stepId -> (taskId -> runStartedEpoch)
  const runningByStep: Map<number, Map<number, number>> = new Map();
  let runningTimer: any = null;

  // Convert Accum -> {average,min,max} helper
  function toStats(acc?: { count?: number; sum?: number; min?: number; max?: number }) {
    if (!acc || !acc.count || acc.count <= 0) return { average: 0, min: 0, max: 0 };
    const avg = acc.sum! / acc.count!;
    return { average: avg, min: acc.min ?? 0, max: acc.max ?? 0 };
  }

  // Recompute live running stats every 1s from runningByStep
  function recomputeRunningStats() {
    const now = Math.floor(Date.now() / 1000);
    for (const step of steps) {
      const rm = runningByStep.get(step.stepId);
      if (!rm || rm.size === 0) {
        // fall back to server-provided runningRun (static) if present
        if (step.runningRun) {
          step.currentRunStats = toStats(step.runningRun);
        } else {
          step.currentRunStats = { average: 0, min: 0, max: 0 };
        }
        continue;
      }
      let count = 0;
      let sum = 0;
      let min = Number.POSITIVE_INFINITY;
      let max = 0;
      rm.forEach((startEpoch) => {
        const elapsed = Math.max(0, now - startEpoch);
        sum += elapsed;
        if (elapsed < min) min = elapsed;
        if (elapsed > max) max = elapsed;
        count++;
      });
      if (count > 0) {
        step.currentRunStats = { average: sum / count, min, max };
      } else {
        step.currentRunStats = { average: 0, min: 0, max: 0 };
      }
    }
    // 🔁 Svelte reactivity nudge: we mutated nested props inside array items
    // Reassigning the array reference forces an update
    steps = steps;
  }

  // Safe percent helper for progress segments
  function pct(part?: number, total?: number): number {
    if (!total || total <= 0 || !part || part <= 0) return 0;
    const p = (part / total) * 100;
    return p < 0 ? 0 : p > 100 ? 100 : p;
  }

  /**
   * Component lifecycle hook that runs on mount
   * Loads initial steps and subscribes to WebSocket messages
   * @async
   * @returns {Promise<void>}
   */
  onMount(async () => {
    steps = await getStepStats({ workflowId });
    console.info('[StepList] mount: workflow', workflowId, 'initial steps:', (steps||[]).length);
    // Subscribe to step entity events, step-stats deltas, and worker events for this workflow
    unsubscribeWS = wsClient.subscribeWithTopics(
      { step: [workflowId], 'step-stats': [workflowId], worker: [] },
      handleMessage
    );
    console.info('[StepList] subscribed to topics:', { step: [workflowId], 'step-stats': [workflowId], worker: [] });
    // Prime derived stats from accumulators
    steps = (steps || []).map((s) => ({
      ...s,
      successRunStats: toStats(s.successRun),
      failedRunStats: toStats(s.failedRun),
      currentRunStats: toStats(s.runningRun),
    }));
    // Start periodic recompute for live running durations (updated via deltas)
    console.log('[StepList] setInterval for recomputeRunningStats activated');
    runningTimer = setInterval(recomputeRunningStats, 1000);
  });

  /**
   * Component lifecycle hook that runs on destroy
   * Unsubscribes from WebSocket messages
   * @returns {void}
   */
  onDestroy(() => {
    if (unsubscribeWS) {
      unsubscribeWS();
    }
    if (runningTimer) {
      clearInterval(runningTimer);
      runningTimer = null;
    }
  });

  /**
   * Marks a step as dirty to trigger Svelte reactivity
   * @param {number} stepId - The ID of the step to mark as dirty
   * @returns {void}
   */
  function markDirty(stepId: number) {
    const i = steps.findIndex(s => s.stepId === stepId);
    if (i !== -1) steps = [...steps.slice(0, i), { ...steps[i] }, ...steps.slice(i+1)];
  }


  // -- Workers map maintenance helpers --------------------------------------
  function removeWorkerEverywhere(workerId: number) {
    let changed = false;
    const next = new Map<number, taskqueue.Worker[]>();
    for (const [sid, arr] of workersByStep.entries()) {
      const filtered = (arr || []).filter(w => w.workerId !== workerId);
      next.set(sid, filtered);
      if (filtered.length !== (arr || []).length) changed = true;
    }
    if (changed) workersByStep = next; // nudge reactivity by replacing Map
    return changed;
  }

  function addWorkerToStep(stepId: number, worker: taskqueue.Worker) {
    const arr = workersByStep.get(stepId) || [];
    // Avoid duplicates
    const exists = arr.some(w => w.workerId === worker.workerId);
    const next = new Map(workersByStep);
    next.set(stepId, exists ? arr : [...arr, worker]);
    workersByStep = next; // reassign to trigger Svelte update
  }

  /**
   * Handles incoming WebSocket messages
   * Updates steps list based on message type
   * @param {Object} message - The WebSocket message
   * @property {string} type - Message type ('step-created' or 'step-deleted')
   * @property {Object} payload - Message payload containing step data
   * @returns {void}
   */
  function handleMessage(message) {
    // STEP entity events
    if (message.type === 'step') {
      if (message.action === 'created' && message.payload?.workflowId === workflowId) {
        const newStep = message.payload;
        const existsInSteps = steps.some((s) => s.stepId === newStep.stepId);
        const existsInPending = pendingSteps.some((s) => s.stepId === newStep.stepId);
        if (!existsInSteps && !existsInPending) {
          const stepObj = {
            stepId: newStep.stepId,
            stepName: newStep.name || newStep.stepName || '',
            totalTasks: 0,
            waitingTasks: 0,
            pendingTasks: 0,
            acceptedTasks: 0,
            runningTasks: 0,
            downloadingTasks: 0,
            successfulTasks: 0,
            failedTasks: 0,
            reallyFailedTasks: 0,
            download: { count: 0, sum: 0, min: 0, max: 0 },
            upload:   { count: 0, sum: 0, min: 0, max: 0 },
            successRun: { count: 0, sum: 0, min: 0, max: 0 },
            failedRun:  { count: 0, sum: 0, min: 0, max: 0 },
            runningRun: { count: 0, sum: 0, min: 0, max: 0 },
            successRunStats: { average: 0, min: 0, max: 0 },
            failedRunStats:  { average: 0, min: 0, max: 0 },
            currentRunStats: { average: 0, min: 0, max: 0 },
          };
          if (isScrolledToTop) {
            steps = [stepObj, ...steps];
          } else {
            pendingSteps = [stepObj, ...pendingSteps];
            newStepsCount = pendingSteps.length;
            showNewStepsNotification = true;
          }
        }
        return;
      }
      if (message.action === 'deleted') {
        const idToRemove = message.payload?.stepId ?? message.id;
        if (typeof idToRemove === 'number') {
          steps = steps.filter((s) => s.stepId !== idToRemove);
          pendingSteps = pendingSteps.filter((s) => s.stepId !== idToRemove);
          runningByStep.delete(idToRemove);
          // Remove any workers shown under this step
          const next = new Map(workersByStep);
          next.delete(idToRemove);
          workersByStep = next;
        }
        return;
      }
    }

    // STEP-STATS incremental deltas (id == workflowId)
    if (message.type === 'step-stats' && message.id === workflowId) {
      const p = message.payload || {};
      const stepId: number = p.stepId;
      const step = steps.find((s) => s.stepId === stepId);
      if (!step) return;

      const oldS: string | undefined = p.oldStatus;
      const newS: string | undefined = p.newStatus;
      const dur: number | undefined = p.duration;

      // Adjust counters: decrement old, increment new
      if (oldS) {
        switch (oldS) {
          case 'W': step.waitingTasks--; break;
          case 'P':
          case 'I': step.pendingTasks--; break;
          case 'A':
          case 'C':
          case 'D':
          case 'O': step.acceptedTasks--; break;
          case 'R': step.runningTasks--; break;
          case 'U':
          case 'V': step.uploadingTasks--; break;
          case 'S': step.successfulTasks--; break;
          case 'F': step.reallyFailedTasks--; if (step?.retried) { step.failedTasks++; } break;
        }
      }
      if (newS) {
        switch (newS) {
          case 'W': step.waitingTasks++; break;
          case 'P':
          case 'I': step.pendingTasks++; break;
          case 'A':
          case 'C':
          case 'D':
          case 'O': step.acceptedTasks++; break;
          case 'R': step.runningTasks++; break;
          case 'U':
          case 'V': step.uploadingTasks++; break;
          case 'S': step.successfulTasks++; break;
          case 'F': step.reallyFailedTasks++; break;
        }
      }

      // Total tasks counter
      if (message?.payload?.incrementTotal !== undefined) {
        step.totalTasks = (step.totalTasks || 0) + message.payload.incrementTotal;
      }

      // Running set maintenance
      if (newS === 'R') {
        if (typeof p.runStartedEpoch === 'number') {
          let m = runningByStep.get(stepId);
          if (!m) { m = new Map(); runningByStep.set(stepId, m); }
          m.set(p.taskId, p.runStartedEpoch);
        } else {
          console.warn('[StepList] Missing or invalid runStartedEpoch for R task:', {
            taskId: p.taskId,
            stepId,
            runStartedEpoch: p.runStartedEpoch,
            payload: p,
          });
        }
      }
      if (oldS === 'R' && newS !== 'R') {
        const m = runningByStep.get(stepId);
        if (m) { m.delete(p.taskId); }
      }

      // Accumulators
      const bumpMinMax = (acc, v) => {
        if (v == null) return;
        acc.sum = (acc.sum || 0) + v;
        if (!acc.min || v < acc.min) acc.min = v;
        if (!acc.max || v > acc.max) acc.max = v;
      };

      if (typeof dur === 'number') {
        if (newS === 'O') {
          bumpMinMax(step.download, dur);
        } else if (newS === 'U') {
          step.successRun.count = (step.successRun.count || 0) + 1;
          bumpMinMax(step.successRun, dur);
          step.successRunStats = toStats(step.successRun);
        } else if (newS === 'V') {
          step.failedRun.count = (step.failedRun.count || 0) + 1;
          bumpMinMax(step.failedRun, dur);
          step.failedRunStats = toStats(step.failedRun);
        } else if (newS === 'S' || newS === 'F') {
          bumpMinMax(step.upload, dur);
        }
      }

      // Start/end time bounds
      if (typeof p.startEpoch === 'number') {
        if (!step.startTime || p.startEpoch < step.startTime) {
          step.startTime = p.startEpoch;
        }
      }
      if (typeof p.endEpoch === 'number') {
        if (!step.endTime || p.endEpoch > step.endTime) {
          step.endTime = p.endEpoch;
        }
      }



      // Update running derived stats now; periodic timer will keep it fresh
      recomputeRunningStats();
      return;
    }

    // WORKER events: track assignment changes per step
    if (message.type === 'worker') {
      console.debug('[StepList] worker event:', message);
      const p = message.payload || {};
      const wid: number | undefined = p.workerId ?? p.id;
      const sid: number | undefined = p.stepId;

      if (typeof wid !== 'number') {
        return;
      }

      // Normalize a minimal worker object; keep name if provided
      const workerObj: taskqueue.Worker = {
        workerId: wid,
        name: p.name ?? `worker-${wid}`,
      } as any;

      switch (message.action) {
        case 'deleted':
          removeWorkerEverywhere(wid);
          steps = steps; // nudge reactivity in case only removal happened
          return;
        default:
          // If the event carries a stepId, keep it simple: ensure it’s there
          if (typeof sid === 'number') {
            removeWorkerEverywhere(wid);
            addWorkerToStep(sid, workerObj);
            steps = steps; // nudge reactivity in case only removal happened
            return;
          }
          return;
      }
    }

    console.warn('[StepList] ignoring unknown WS message:', message);
  }

  /**
   * Handles scroll events on the table container
   * Triggers loading more steps when near bottom
   * Tracks scroll position for new steps notification
   * @returns {void}
   */
  function handleScroll() {
    if (!tableContainer || isLoading) return;

    const { scrollTop, scrollHeight, clientHeight, scrollLeft } = tableContainer;

    const isVerticalScroll = Math.abs(scrollTop - lastScrollPosition.top) >
                             Math.abs(scrollLeft - lastScrollPosition.left);

    lastScrollPosition = { top: scrollTop, left: scrollLeft };

    if (!isVerticalScroll) return;

    isScrolledToTop = scrollTop <= 10;

    const threshold = 150;
    const distanceFromBottom = scrollHeight - (scrollTop + clientHeight);

    if (distanceFromBottom <= threshold && hasMoreSteps && !isLoading) {
      loadMoreSteps();
    }
  }

  /**
   * Loads additional steps when scrolling near bottom
   * @async
   * @returns {Promise<void>}
   */
  async function loadMoreSteps() {
    if (isLoading || !hasMoreSteps) return;
    isLoading = true;
    try {
      // Stats endpoint returns the whole set; disable infinite scroll for now.
      hasMoreSteps = false;
    } finally {
      isLoading = false;
    }
  }
</script>

<!-- Steps container component -->
<div class="steps-container">
  {#if steps && steps.length > 0}
    <div class="steps-table-wrapper" bind:this={tableContainer} on:scroll={handleScroll}>
      {#if showNewStepsNotification}
        <button class="new-steps-notification" on:click={loadNewSteps}>
          {newStepsCount} new step{newStepsCount > 1 ? 's' : ''} available
          <span class="show-new-btn">Show</span>
        </button>
      {/if}

      <!-- Steps table -->
      <table class="listTable">
        <thead>
          <tr>
            <th>#</th>
            <th>Name</th>
            <th>Workers</th>
            <th>Progress</th>
            <th>Queued</th>
            <th>Starting</th>
            <th>Running</th>
            <th>Success</th>
            <th>Fail</th>
            <th>Total</th>
            <th>Average duration [min-max]</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each steps as step (step.stepId)}
            <tr data-testid={`step-${step.stepId}`}>
              <td>{step.stepId}</td>
              <td><a href="#/tasks?stepId={step.stepId}" class="workerCompo-clickable">{step.stepName}</a></td>
              <td>
                {#each workersByStep.get(step.stepId) || [] as worker (worker.workerId)}
                  <div class="worker-badge" title={`Worker ID: ${worker.workerId}`}>
                    <a href="#/tasks?workerId={worker.workerId}" class="workerCompo-clickable">{worker.name}</a>
                  </div>
                {/each}
              </td>
      <td><div class="wf-progress-bar">
                <div class="wf-progress">
                  <!-- success segment -->
                  <div class="wf-progress__segment wf-progress__segment--success"
                       style="width: {pct(step.successfulTasks, step.totalTasks)}%; transform: translateX(0%);"></div>

                  <!-- running segment: starts after success -->
                  <div class="wf-progress__segment wf-progress__segment--running"
                       style="left: {pct(step.successfulTasks, step.totalTasks)}%; width: {pct(step.runningTasks, step.totalTasks)}%;"></div>

                  <!-- fail segment: starts after success + running -->
                  <div class="wf-progress__segment wf-progress__segment--fail"
                       style="left: {pct(step.successfulTasks + step.runningTasks, step.totalTasks)}%; width: {pct(step.reallyFailedTasks, step.totalTasks)}%;"></div>
                </div>
              </div></td>
              <td>{showIfNonZero(step.waitingTasks + step.pendingTasks) }</td> 
              <td>{showIfNonZero(step.acceptedTasks) }</td>
              <td>{showIfNonZero(step.runningTasks) }</td>
              <td class="success-cell">{showIfNonZero(step.successfulTasks) }</td>
              <td class="fail-cell">
                <span class="really-failed">{showIfNonZero(step.reallyFailedTasks)}</span>
                <span class="retried-failed">{showIfNonZero(step.failedTasks)}</span>
              </td>
              <td>{showIfNonZero(step.totalTasks) }</td>
              <td class="duration-cell">
                {#if step.runningTasks > 0}
                  <div class="duration-grid duration-running">
                    <span class="label">Running:</span>
                    <span class="avg tabnum">{formatDuration(step.currentRunStats?.average)}</span>
                    {#if step.runningTasks > 1}
                      <span class="bracket">[</span>
                      <span class="min tabnum">{formatDuration(step.currentRunStats?.min)}</span>
                      <span class="dash">–</span>
                      <span class="max tabnum">{formatDuration(step.currentRunStats?.max)}</span>
                      <span class="bracket">]</span>
                    {/if}
                  </div>
                {/if}
                {#if step.successfulTasks > 0}
                  <div class="duration-grid duration-success">
                    <span class="label">Success:</span>
                    <span class="avg tabnum">{formatDuration(step.successRunStats?.average)}</span>
                    {#if step.successfulTasks > 1}
                      <span class="bracket">[</span>
                      <span class="min tabnum">{formatDuration(step.successRunStats?.min)}</span>
                      <span class="dash">–</span>
                      <span class="max tabnum">{formatDuration(step.successRunStats?.max)}</span>
                      <span class="bracket">]</span>
                    {/if}
                  </div>
                {/if}
                {#if step.failedTasks > 0}
                  <div class="duration-grid duration-fail">
                    <span class="label">Fail:</span>
                    <span class="avg tabnum">{formatDuration(step.failedRunStats?.average)}</span>
                    {#if step.failedTasks > 1}
                      <span class="bracket">[</span>
                      <span class="min tabnum">{formatDuration(step.failedRunStats?.min)}</span>
                      <span class="dash">–</span>
                      <span class="max tabnum">{formatDuration(step.failedRunStats?.max)}</span>
                      <span class="bracket">]</span>
                    {/if}
                  </div>
                {/if}
              </td>
              <td class="workerCompo-actions">
                <button class="btn-action" title="Pause"><PauseCircle /></button>
                <button class="btn-action" title="Reset"><RefreshCw /></button>
                <button class="btn-action" title="Break"><CircleX /></button>
                <button class="btn-action" title="Clear" data-testid={`delete-step-${step.stepId}`} on:click={() => delStep(step.stepId)}><Eraser /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p>No steps found for workflow #{workflowId}</p>
  {/if}
</div>
<style>
  .success-cell {
    color: lightgreen;
    font-weight: bold;
  }
  .really-failed {
    color: red;
    font-weight: bold;
    margin-right: 4px;
  }
  .retried-failed {
    color: orange;
    font-weight: bold;
  }
  .duration-cell {
    text-align: left;
  }
  .duration-grid {
    display: grid;
    grid-template-columns: 70px 70px 4px 60px 10px 60px 4px;
    column-gap: 4px;
    align-items: baseline;
    white-space: nowrap;
  }
  .tabnum {
    font-variant-numeric: tabular-nums;
  }
  .duration-success {
    color: lightgreen;
  }
  .duration-fail {
    color: red;
  }
  .duration-running {
    color: white;
  }
</style>