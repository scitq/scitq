<script lang="ts"> 
  import StepList from './StepList.svelte';
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown } from 'lucide-svelte';
  import {delWorkflow} from '../lib/api';
  import { onMount } from 'svelte';
  
  /**
   * List of workflows passed as a prop to the component
   * @type {Array<{workflowId: number, name: string}>}
   */
  export let workflows = [];

  /**
   * Set tracking which workflows are currently expanded
   * @type {Set<number>}
   */
  let expandedWorkflows = new Set<number>();

  /**
   * Toggles the expanded state of a workflow
   * @param {number} workflowId - The ID of the workflow to toggle
   */
  function toggleWorkflow(workflowId: number): void {
    if (expandedWorkflows.has(workflowId)) {
      expandedWorkflows.delete(workflowId);
    } else {
      expandedWorkflows.add(workflowId);
    }

    // Force reactivity by creating a new Set
    expandedWorkflows = new Set(expandedWorkflows);
  }

  /**
   * Component lifecycle hook that runs on mount
   * Checks URL for workflow ID to auto-expand and scroll to
   */
  onMount(() => {
    const hash = window.location.hash;
    const params = new URLSearchParams(hash.split('?')[1]);
    const openId = params.get('open');

    if (openId) {
      const workflowId = parseInt(openId);
      if (!isNaN(workflowId)) {
        expandedWorkflows.add(workflowId);
        expandedWorkflows = new Set(expandedWorkflows); // trigger reactivity

        // Scroll to the workflow element (if found)
        setTimeout(() => {
          const el = document.querySelector(`[data-testid="wf-${workflowId}"]`);
          if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }, 0);
      }
    }
  });
</script>

<!-- Workflows list container -->
{#if workflows && workflows.length > 0}
  <div class="wf-list">
    {#each workflows as wf (wf.workflowId)}
      <!-- Individual workflow item -->
      <div class="wf-item" data-testid={`wf-${wf.workflowId}`}>
        <!-- Expand/collapse toggle button -->
        <button class="wf-chevron-btn" on:click={() => toggleWorkflow(wf.workflowId)}>
          {#if expandedWorkflows.has(wf.workflowId)}
            <ChevronDown class="wf-chevron" data-testid={`chevronDown-${wf.workflowId}`}/>
          {:else}
            <ChevronRight class="wf-chevron" data-testid={`chevronRight-${wf.workflowId}`}/>
          {/if}
        </button>

        <!-- Workflow information section -->
        <div class="wf-info">
          <span class="wf-id">#{wf.workflowId}</span>
          <a href={`#/tasks?workflowId=${wf.workflowId}`} class="wf-name">{wf.name}</a>
          <div class="wf-progress-bar"></div> <!-- Placeholder for future progress bar -->
        </div>

        <!-- Workflow action buttons -->
        <div class="wf-actions">
          <button class="btn-action" title="Pause"><PauseCircle /></button>
          <button class="btn-action" title="Reset"><RefreshCw /></button>
          <button class="btn-action" title="Break"><CircleX /></button>
          <button class="btn-action" title="Clear" data-testid={`delete-btn-${wf.workflowId}`} on:click={() => delWorkflow(wf.workflowId)}><Eraser /></button>
        </div>
      </div>

      <!-- Steps list for expanded workflows -->
      {#if expandedWorkflows.has(wf.workflowId)}
        <div class="wf-steps">
          <StepList workflowId={wf.workflowId} />
        </div>
      {/if}
    {/each}
  </div>
{:else}
  <!-- Empty state message when no workflows exist -->
  <p class="workerCompo-empty-state">No Workflow found.</p>
{/if}