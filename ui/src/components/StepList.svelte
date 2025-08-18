<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as grpcWeb from 'grpc-web';
  import { getSteps, delStep } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import { RefreshCw, PauseCircle, CircleX, Eraser } from 'lucide-svelte';

  /**
   * Workflow ID passed as a prop to the component
   * @type {number}
   */
  export let workflowId: number;

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

  /**
   * Component lifecycle hook that runs on mount
   * Loads initial steps and subscribes to WebSocket messages
   * @async
   * @returns {Promise<void>}
   */
  onMount(async () => {
    steps = await getSteps(workflowId, STEPS_CHUNK_SIZE, 0);
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
      const newSteps = await getSteps(workflowId, STEPS_CHUNK_SIZE, steps.length);

      if (newSteps.length === 0) {
        hasMoreSteps = false;
      } else {
        steps = [...steps, ...newSteps];
      }
    } catch (error) {
      console.error("Failed to load more steps:", error);
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
            <th>Starting</th>
            <th>Progress</th>
            <th>Successes</th>
            <th>Fails</th>
            <th>Total</th>
            <th>Average duration [min-max]</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each steps as step (step.stepId)}
            <tr data-testid={`step-${step.stepId}`}>
              <td>{step.stepId}</td>
              <td><a href="#/tasks?workflowId={workflowId}&stepId={step.stepId}" class="workerCompo-clickable">{step.name}</a></td>
              <td>Worker</td>
              <td><div class="wf-progress-bar"></div></td>
              <td>Starting</td>
              <td>Progress</td>
              <td>Successes</td>
              <td>Fails</td>
              <td>Total</td>
              <td>Average duration [min-max]</td>
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