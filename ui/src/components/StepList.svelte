<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import * as grpcWeb from 'grpc-web';
  import { getSteps, delStep } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import { RefreshCw, PauseCircle, CircleX, Eraser } from 'lucide-svelte';

  export let workflowId: number;

  let steps = [];
  let pendingSteps = [];
  let hasMoreSteps = true;
  let isLoading = false;
  let tableContainer: HTMLDivElement;
  let isScrolledToTop = true;
  let newStepsCount = 0;
  let showNewStepsNotification = false;
  let lastScrollPosition = { top: 0, left: 0 };

  const STEPS_CHUNK_SIZE = 25;

  let unsubscribeWS: () => void;

  onMount(async () => {
    steps = await getSteps(workflowId, STEPS_CHUNK_SIZE, 0);
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  onDestroy(() => {
    unsubscribeWS?.();
  });

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

  function loadNewSteps() {
    steps = [...pendingSteps, ...steps];
    pendingSteps = [];
    newStepsCount = 0;
    showNewStepsNotification = false;

    if (tableContainer) {
      tableContainer.scrollTo({ top: 0, behavior: 'smooth' });
    }
  }

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


<div class="steps-container">
  {#if steps && steps.length > 0}
    <div class="steps-table-wrapper" bind:this={tableContainer} on:scroll={handleScroll}>
      {#if showNewStepsNotification}
        <button class="new-steps-notification" on:click={loadNewSteps}>
          {newStepsCount} new step{newStepsCount > 1 ? 's' : ''} available
          <span class="show-new-btn">Show</span>
        </button>
      {/if}

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
                <button class="btn-action" title="Clear" on:click={() => delStep(step.stepId)}><Eraser /></button>
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