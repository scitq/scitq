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
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  /**
   * Component lifecycle hook that runs on destroy
   * Unsubscribes from WebSocket messages
   * @returns {void}
   */
  onDestroy(() => {
    unsubscribeWS?.();
  });

  /**
   * Handles incoming WebSocket messages
   * Updates steps list based on message type
   * @param {Object} message - The WebSocket message
   * @property {string} type - Message type ('step-created' or 'step-deleted')
   * @property {Object} payload - Message payload containing step data
   * @returns {void}
   */
  function handleMessage(message) {
    if (message.type === 'step-created' && message.payload.workflowId === workflowId) {
      const newStep = message.payload;
      const existsInSteps = steps.some(s => s.stepId === newStep.stepId);
      const existsInPending = pendingSteps.some(s => s.stepId === newStep.stepId);

      if (!existsInSteps && !existsInPending) {
        if (isScrolledToTop) {
          steps = [newStep, ...steps];
        } else {
          pendingSteps = [newStep, ...pendingSteps];
          newStepsCount = pendingSteps.length;
          showNewStepsNotification = true;
        }
      }
    }

    if (message.type === 'step-deleted') {
      const idToRemove = message.payload.stepId;
      steps = steps.filter(s => s.stepId !== idToRemove);
      pendingSteps = pendingSteps.filter(s => s.stepId !== idToRemove);
    }
  }

  /**
   * Loads pending steps into the main steps list
   * Resets notification and scrolls to top
   * @returns {void}
   */
  function loadNewSteps() {
    steps = [...pendingSteps, ...steps];
    pendingSteps = [];
    newStepsCount = 0;
    showNewStepsNotification = false;

    if (tableContainer) {
      tableContainer.scrollTo({ top: 0, behavior: 'smooth' });
    }
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
                {#each workersPerStepId.get(step.stepId) || [] as worker (worker.workerId)}
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

                  <!-- fail segment: starts after success -->
                  <div class="wf-progress__segment wf-progress__segment--fail"
                       style="width: {pct(step.failedTasks, step.totalTasks)}%; transform: translateX({pct(step.successfulTasks, step.totalTasks)}%);"></div>

                  <!-- optional running segment: starts after success+fail -->
                  <div class="wf-progress__segment wf-progress__segment--running"
                       style="width: {pct(step.runningTasks, step.totalTasks)}%; transform: translateX({pct(step.successfulTasks, step.totalTasks) + pct(step.failedTasks, step.totalTasks)}%);"></div>
                </div>
              </div></td>
              <td>{showIfNonZero(step.waitingTasks + step.pendingTasks) }</td> 
              <td>{showIfNonZero(step.acceptedTasks + step.onholdTasks) }</td>
              <td>{showIfNonZero(step.runningTasks) }</td>
              <td class="success-cell">{showIfNonZero(step.successfulTasks) }</td>
              <td class="fail-cell">{showIfNonZero(step.failedTasks) }</td>
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
  .fail-cell {
    color: red;
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