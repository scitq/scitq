<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getWorkFlow, getWorkers } from '../lib/api';
  // Workers and mapping by stepId (passed down to WorkflowList → StepList)
  let workers: taskqueue.Worker[] = [];
  let workersPerStepId: Map<number, taskqueue.Worker[]> = new Map();
  import { wsClient } from '../lib/wsClient';
  import WorkflowList from '../components/WorkflowList.svelte';
  import '../styles/workflow.css';

  // Array of currently displayed workflows
  let workflows = [];
  // Array of newly created workflows waiting to be displayed
  let pendingWorkflows = [];    
  // Flag indicating if more workflows are available to load
  let hasMoreWorkflows = true;
  // Loading state flag
  let isLoading = false;
  // Reference to the list container DOM element
  let listContainer: HTMLDivElement;
  // Flag indicating if list is scrolled to top
  let isScrolledToTop = true;
  // Count of new workflows waiting to be displayed
  let newWorkflowsCount = 0;
  // Flag to show new workflows notification
  let showNewWorkflowsNotification = false;

  // Number of workflows to load at once
  const WORKFLOWS_CHUNK_SIZE = 25;

  // WebSocket unsubscribe function
  let unsubscribeWS: (() => void) | null = null;

  function mapWorkersToStepIds(workers: taskqueue.Worker[]): Map<number, taskqueue.Worker[]> {
    const map = new Map<number, taskqueue.Worker[]>();
    for (const w of workers) {
      if (w.stepId == null) continue;
      const list = map.get(w.stepId);
      if (list) list.push(w);
      else map.set(w.stepId, [w]);
    }
    return map;
  }

  /**
   * Component initialization - loads initial workflows and sets up WebSocket
   * @async
   */
  onMount(async () => {
    workflows = await getWorkFlow(undefined, WORKFLOWS_CHUNK_SIZE, 0);
    workers = await getWorkers();
    workersPerStepId = mapWorkersToStepIds(workers);
    console.log('Loaded workers:', workers);
    console.log('Workers per stepId map:', workersPerStepId);
    // Subscribe to workflow + step-stats events
    unsubscribeWS = wsClient.subscribeWithTopics({ workflow: [], 'step-stats': [] }, handleMessage);
  });



  /**
   * Component cleanup - unsubscribes from WebSocket
   */
  onDestroy(() => {
    if (unsubscribeWS) {
      unsubscribeWS();
      unsubscribeWS = null;
    }
  });

  /**
   * Handles incoming WebSocket messages for workflow updates
   * @param {Object} message - WebSocket message
   * @param {string} message.type - Message type ('workflow-created' or 'workflow-deleted')
   * @param {Object} message.payload - Message payload containing workflow data
   */
  function handleMessage(message) {
    if (message?.type === 'workflow') {
      const action = message.action;
      const p = message.payload || {};

      if (action === 'created') {
        const newWf = p;
        const existsInDisplayed = workflows.some(wf => wf.workflowId === newWf.workflowId);
        const existsInPending = pendingWorkflows.some(wf => wf.workflowId === newWf.workflowId);
        if (!existsInDisplayed && !existsInPending) {
          if (isScrolledToTop) {
            workflows = [newWf, ...workflows];
            console.log('workflow created via WebSocket:', newWf.workflowId);
          } else {
            pendingWorkflows = [newWf, ...pendingWorkflows];
            newWorkflowsCount = pendingWorkflows.length;
            showNewWorkflowsNotification = true;
          }
        }
        return;
      }

      if (action === 'deleted') {
        const idToRemove = typeof p.workflowId === 'number' ? p.workflowId : message.id;
        if (typeof idToRemove === 'number') {
          workflows = workflows.filter(wf => wf.workflowId !== idToRemove);
          pendingWorkflows = pendingWorkflows.filter(wf => wf.workflowId !== idToRemove);
        }
        return;
      }

      if (action === 'status') {
        const wfId = message.id;
        const newStatus = p.status;
        if (typeof wfId === 'number' && typeof newStatus === 'string') {
          workflows = workflows.map(wf =>
            wf.workflowId === wfId ? { ...wf, status: newStatus } : wf
          );
        }
        return;
      }
    }

    // Step-stats delta: update workflow progress counters
    if (message?.type === 'step-stats' && message?.action === 'delta') {
      const p = message.payload || {};
      const wfId = p.workflowId;
      const oldStatus = p.oldStatus;
      const newStatus = p.newStatus;
      const isRetry = !!p.retried;
      if (typeof wfId !== 'number') return;

      workflows = workflows.map(wf => {
        if (wf.workflowId !== wfId) return wf;
        const updated = { ...wf };

        // Decrement old status bucket
        if (oldStatus === 'S') updated.succeededTasks = Math.max(0, (updated.succeededTasks || 0) - 1);
        else if (oldStatus === 'F') updated.failedTasks = Math.max(0, (updated.failedTasks || 0) - 1);
        else if (['A', 'C', 'D', 'O', 'R', 'U', 'V'].includes(oldStatus)) updated.runningTasks = Math.max(0, (updated.runningTasks || 0) - 1);

        // Increment new status bucket
        if (newStatus === 'S') updated.succeededTasks = (updated.succeededTasks || 0) + 1;
        else if (newStatus === 'F') updated.failedTasks = (updated.failedTasks || 0) + 1;
        else if (['A', 'C', 'D', 'O', 'R', 'U', 'V'].includes(newStatus)) updated.runningTasks = (updated.runningTasks || 0) + 1;

        // Retrying: increment on retry clone creation, decrement on terminal
        if (isRetry && !oldStatus) {
          updated.retryingTasks = (updated.retryingTasks || 0) + 1;
        }
        if (isRetry && (newStatus === 'S' || newStatus === 'F')) {
          updated.retryingTasks = Math.max(0, (updated.retryingTasks || 0) - 1);
        }

        // Total only increases on first submission (oldStatus is empty/null)
        if (!oldStatus) updated.totalTasks = (updated.totalTasks || 0) + 1;

        return updated;
      });
      return;
    }
  }

  /**
   * Loads pending workflows into main display
   */
  function loadNewWorkflows() {
    workflows = [...pendingWorkflows, ...workflows];
    pendingWorkflows = [];
    newWorkflowsCount = 0;
    showNewWorkflowsNotification = false;
    // Scroll to top after loading
    if (listContainer) {
      listContainer.scrollTo({ top: 0, behavior: 'smooth' });
    }
  }

  /**
   * Handles scroll events to manage workflow loading and position tracking
   * @async
   */
  async function handleScroll() {
    if (!listContainer) return;

    const { scrollTop } = listContainer;
    isScrolledToTop = scrollTop <= 10;

    // If scrolled to top with pending workflows, refresh the list
    if (isScrolledToTop && showNewWorkflowsNotification) {
      workflows = await getWorkFlow(undefined, workflows.length + newWorkflowsCount, 0);
      showNewWorkflowsNotification = false;
      newWorkflowsCount = 0;
    }
  }
</script>

<!-- ----------- MAIN CONTAINER ----------- -->
<div class="wf-container" data-testid="wf-page">
  <div class="workflow-content">
    
    <!-- New workflows notification button -->
    {#if showNewWorkflowsNotification}
      <button 
        class="new-workflows-notification" 
        on:click={loadNewWorkflows}
        aria-label={`View ${newWorkflowsCount} new workflow(s)`}
      >
        {newWorkflowsCount} new workflow(s) available
        <span class="show-new-btn">View</span>
      </button>
    {/if}

    <!-- Scrollable workflow list container -->
    <div 
      class="workflow-list-scroll-container" 
      bind:this={listContainer} 
      on:scroll={handleScroll}
    >
      <WorkflowList {workflows} workersPerStepId={workersPerStepId} />
    </div>

  </div>
</div>