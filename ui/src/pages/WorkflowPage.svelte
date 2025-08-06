<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getWorkFlow } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import WorkflowList from '../components/WorkflowList.svelte';
  import '../styles/workflow.css';

  let workflows = [];
  let pendingWorkflows = [];    
  let hasMoreWorkflows = true;
  let isLoading = false;
  let listContainer: HTMLDivElement;
  let isScrolledToTop = true;
  let newWorkflowsCount = 0;
  let showNewWorkflowsNotification = false;

  const WORKFLOWS_CHUNK_SIZE = 25;

  let unsubscribeWS: () => void;

  onMount(async () => {
    workflows = await getWorkFlow(undefined, WORKFLOWS_CHUNK_SIZE, 0);
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  onDestroy(() => {
    unsubscribeWS?.();
  });

function handleMessage(message) {
  if (message.type === 'workflow-created') {
    const newWf = message.payload;
    const existsInDisplayed = workflows.some(wf => wf.workflowId === newWf.workflowId);
    const existsInPending = pendingWorkflows.some(wf => wf.workflowId === newWf.workflowId);

    if (!existsInDisplayed && !existsInPending) {
      if (isScrolledToTop) {
        workflows = [newWf, ...workflows];
        console.log('workflow created via WebSocket:', message.payload.workflowId);
      } else {
        pendingWorkflows = [newWf, ...pendingWorkflows];
        newWorkflowsCount = pendingWorkflows.length;
        showNewWorkflowsNotification = true;
      }
    }
  }

  if (message.type === 'workflow-deleted') {
    const idToRemove = message.payload.workflowId;
    workflows = workflows.filter(wf => wf.workflowId !== idToRemove);
    pendingWorkflows = pendingWorkflows.filter(wf => wf.workflowId !== idToRemove);
    console.log('workflow removed via WebSocket:', idToRemove);
  }
}

function loadNewWorkflows() {
  workflows = [...pendingWorkflows, ...workflows];
  pendingWorkflows = [];
  newWorkflowsCount = 0;
  showNewWorkflowsNotification = false;
  if (listContainer) {
    listContainer.scrollTo({ top: 0, behavior: 'smooth' });
  }
}


  /**
   * Scroll event listener to update scroll position
   */
  async function handleScroll() {
    if (!listContainer) return;

    const { scrollTop } = listContainer;
    isScrolledToTop = scrollTop <= 10;

    if (isScrolledToTop && showNewWorkflowsNotification) {
      workflows = await getWorkFlow(undefined, workflows.length + newWorkflowsCount, 0);
      showNewWorkflowsNotification = false;
      newWorkflowsCount = 0;
    }
  }

</script>

<!-- ----------- UI ----------- -->
<div class="wf-container" data-testid="wf-page">
  <div class="workflow-content">
    
    <!-- New workflows notification -->
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

    <!-- Scrollable list container -->
    <div 
      class="workflow-list-scroll-container" 
      bind:this={listContainer} 
      on:scroll={handleScroll}
    >
      <WorkflowList {workflows} />
    </div>

  </div>
</div>
