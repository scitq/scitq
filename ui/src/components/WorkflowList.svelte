<script lang="ts">
  import { stopPropagation } from 'svelte/legacy';

  import StepList from './StepList.svelte';
  import { RefreshCw, PauseCircle, CircleX, Eraser, ChevronRight, ChevronDown, HelpCircle } from 'lucide-svelte';
  import {delWorkflow} from '../lib/api';
  import { onMount } from 'svelte';
  import TemplateRunModal from './TemplateRunModal.svelte';
  import { wfCounters } from '../lib/wfCounters';

  let modalRunId: number | null = $state(null);
  function openModal(id: number) { modalRunId = id; }
  function closeModal() { modalRunId = null; }
  
  
  interface Props {
    /** List of workflows passed as a prop to the component */
    workflows?: Array<{workflowId: number, name: string}>;
    workersPerStepId: Map<number, taskqueue.Worker[]>;
  }

  let { workflows = [], workersPerStepId }: Props = $props();

  /**
   * Set tracking which workflows are currently expanded
   * @type {Set<number>}
   */
  let expandedWorkflows = $state(new Set<number>());

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

  /**
   * Describe what launched a workflow (template name@version, local
   * script name, or empty string for legacy rows without a template_run
   * link). Used as the hover tooltip on the workflow name.
   */
  function launchSummary(wf) {
    if (wf.templateName) {
      const v = wf.templateVersion ? '@' + wf.templateVersion : '';
      return 'template ' + wf.templateName + v;
    }
    if (wf.scriptName) {
      const sha = wf.scriptSha256 ? ' (' + wf.scriptSha256.slice(0, 8) + ')' : '';
      return 'script ' + wf.scriptName + sha;
    }
    return '';
  }
</script>

<!-- Workflows list container -->
{#if workflows && workflows.length > 0}
  <div class="wf-list">
    {#each workflows as wf (wf.workflowId)}
      <!-- Counter values come from the wfCounters sideband store rather
           than from `wf.*` so high-frequency step-stats deltas don't
           force the parent to do `workflows[idx] = updated`, which
           used to re-run the whole keyed-each + child $set cascade and
           crash the renderer. See ../lib/wfCounters.ts for context.
           Fallback to wf.* for the initial render before the store is
           seeded. -->
      {@const c = $wfCounters.get(wf.workflowId)}
      {@const totalTasks = c?.totalTasks ?? wf.totalTasks}
      {@const succeededTasks = c?.succeededTasks ?? wf.succeededTasks}
      {@const failedTasks = c?.failedTasks ?? wf.failedTasks}
      {@const runningTasks = c?.runningTasks ?? wf.runningTasks}
      {@const retryingTasks = c?.retryingTasks ?? wf.retryingTasks ?? 0}

      <!-- Individual workflow item -->
      <div class="wf-item" data-testid={`wf-${wf.workflowId}`}>
        <!-- Expand/collapse toggle button -->
        <button class="wf-chevron-btn" onclick={() => toggleWorkflow(wf.workflowId)}>
          {#if expandedWorkflows.has(wf.workflowId)}
            <ChevronDown class="wf-chevron" data-testid={`chevronDown-${wf.workflowId}`}/>
          {:else}
            <ChevronRight class="wf-chevron" data-testid={`chevronRight-${wf.workflowId}`}/>
          {/if}
        </button>

        <!-- Workflow information section -->
        <div class="wf-info">
          <span class="wf-id">#{wf.workflowId}</span>
          <span class="wf-status-dot {wf.status === 'S' ? 'wf-dot-green' : runningTasks > 0 ? 'wf-dot-blue' : failedTasks > 0 ? 'wf-dot-red' : 'wf-dot-gray'}"
                title={wf.status === 'S' ? 'Completed' : runningTasks > 0 ? `${runningTasks} active` : wf.status === 'F' ? `Stuck: ${failedTasks} failed` : failedTasks > 0 ? `${failedTasks} failed, no active tasks` : 'Idle'}></span>
          <a href={`#/tasks?workflowId=${wf.workflowId}`}
             class="wf-name"
             title={launchSummary(wf) ? `Launched by: ${launchSummary(wf)}` : undefined}>{wf.name}</a>
          {#if wf.templateRunId}
            <button
              class="wf-info-btn"
              onclick={stopPropagation(() => openModal(wf.templateRunId))}
              title="Show template run details"
              aria-label="Show template run details"
              data-testid={`info-btn-${wf.workflowId}`}
            ><HelpCircle size="16" /></button>
          {/if}
          {#if totalTasks > 0}
            {@const t = totalTasks}
            {@const pctSuccess = succeededTasks / t * 100}
            {@const pctFailed = failedTasks / t * 100}
            {@const pctRunning = runningTasks / t * 100}
            {@const pctRetrying = retryingTasks / t * 100}
            <div class="wf-progress-bar"
                 title={`${succeededTasks}/${t} succeeded, ${failedTasks} failed, ${runningTasks} running` + (retryingTasks ? `, ${retryingTasks} retrying` : '')}>
              <div class="wf-progress-success" style="width:{pctSuccess}%"></div>
              <div class="wf-progress-running" style="width:{pctRunning}%"></div>
              <div class="wf-progress-retrying" style="width:{pctRetrying}%"></div>
              <div class="wf-progress-failed" style="width:{pctFailed}%"></div>
            </div>
          {:else}
            <div class="wf-progress-bar"></div>
          {/if}
        </div>

        <!-- Workflow action buttons -->
        <div class="wf-actions">
          <button class="btn-action" title="Pause"><PauseCircle /></button>
          <button class="btn-action" title="Reset"><RefreshCw /></button>
          <button class="btn-action" title="Break"><CircleX /></button>
          <button class="btn-action" title="Clear" data-testid={`delete-btn-${wf.workflowId}`} onclick={() => delWorkflow(wf.workflowId)}><Eraser /></button>
        </div>
      </div>

      <!-- Steps list for expanded workflows -->
      {#if expandedWorkflows.has(wf.workflowId)}
        <div class="wf-steps">
          <StepList workflowId={wf.workflowId} workersPerStepId={workersPerStepId} />
        </div>
      {/if}
    {/each}
  </div>
{:else}
  <!-- Empty state message when no workflows exist -->
  <p class="workerCompo-empty-state">No Workflow found.</p>
{/if}

{#if modalRunId !== null}
  <TemplateRunModal templateRunId={modalRunId} onClose={closeModal} />
{/if}

<style>
  .wf-info-btn {
    background: transparent;
    border: none;
    padding: 2px 4px;
    margin-left: 4px;
    cursor: pointer;
    color: var(--text-secondary, var(--text-primary));
    display: inline-flex;
    align-items: center;
    border-radius: 4px;
  }
  .wf-info-btn:hover {
    color: var(--primary-color);
    background: var(--bg-secondary);
  }
</style>