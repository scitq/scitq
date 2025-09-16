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
    // Subscribe to workflow entity events only
    unsubscribeWS = wsClient.subscribeWithTopics({ workflow: [] }, handleMessage);
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
          console.log('workflow removed via WebSocket:', idToRemove);
        }
        return;
      }
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