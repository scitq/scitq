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

  // Persistence: search filter goes to URL hash (link-shareable, survives reload),
  // pagination count + scroll go to sessionStorage (transient, restore-on-return).
  const ROUTE_HASH = '#/workflows';
  const STORAGE_KEY = 'wf:state';

  function readUrlSearch(): string {
    const hash = window.location.hash;
    const qIdx = hash.indexOf('?');
    if (qIdx < 0) return '';
    return new URLSearchParams(hash.slice(qIdx + 1)).get('search') || '';
  }

  function writeUrlSearch(value: string) {
    const newHash = value ? `${ROUTE_HASH}?search=${encodeURIComponent(value)}` : ROUTE_HASH;
    if (window.location.hash !== newHash) {
      history.replaceState(null, '', newHash);
    }
  }

  function readSessionState(): { loaded: number; scrollTop: number } | null {
    try {
      const raw = sessionStorage.getItem(STORAGE_KEY);
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  }

  function writeSessionState(loaded: number, scrollTop: number) {
    try {
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ loaded, scrollTop }));
    } catch {}
  }

  function clearSessionState() {
    try { sessionStorage.removeItem(STORAGE_KEY); } catch {}
  }

  // Name filter (case-insensitive substring match server-side). Initialized
  // from the URL so the filter survives navigation and is link-shareable.
  let nameFilter = readUrlSearch();
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

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
    // Restore loaded count from sessionStorage so coming back from a task
    // detail page doesn't reset the user to chunk 1.
    const saved = readSessionState();
    const targetCount = saved?.loaded && saved.loaded > 0 ? saved.loaded : WORKFLOWS_CHUNK_SIZE;
    workflows = await getWorkFlow(nameFilter || undefined, targetCount, 0);
    // If we asked for N and got exactly N back, assume more might exist.
    // Worst case: a no-op "Load more" click (acceptable).
    hasMoreWorkflows = workflows.length === targetCount;
    workers = await getWorkers();
    workersPerStepId = mapWorkersToStepIds(workers);
    // Subscribe to workflow + step-stats events
    unsubscribeWS = wsClient.subscribeWithTopics({ workflow: [], 'step-stats': [] }, handleMessage);
    // Restore scroll after the list has rendered.
    if (saved?.scrollTop && saved.scrollTop > 0) {
      requestAnimationFrame(() => {
        if (listContainer) listContainer.scrollTop = saved.scrollTop;
      });
    }
  });

  /**
   * Fetches the next page of workflows and appends them to the list.
   * Uses the current workflows.length as the offset, so it naturally picks
   * up where the last chunk left off even after WebSocket inserts/deletes.
   */
  async function loadMoreWorkflows() {
    if (isLoading || !hasMoreWorkflows) return;
    isLoading = true;
    try {
      const next = await getWorkFlow(nameFilter || undefined, WORKFLOWS_CHUNK_SIZE, workflows.length);
      // Filter out any overlap caused by concurrent deletions/insertions.
      const seen = new Set(workflows.map(w => w.workflowId));
      const fresh = next.filter(w => !seen.has(w.workflowId));
      workflows = [...workflows, ...fresh];
      hasMoreWorkflows = next.length === WORKFLOWS_CHUNK_SIZE;
    } finally {
      isLoading = false;
    }
  }

  // Debounced re-fetch on search input. 250ms feels responsive without
  // hammering the server while the user is typing. Also writes the filter
  // to the URL hash and clears the session-stored loaded count (which is
  // meaningless under a different filter).
  function onSearchInput() {
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(async () => {
      writeUrlSearch(nameFilter);
      clearSessionState();
      isLoading = true;
      try {
        workflows = await getWorkFlow(nameFilter || undefined, WORKFLOWS_CHUNK_SIZE, 0);
        hasMoreWorkflows = workflows.length === WORKFLOWS_CHUNK_SIZE;
        pendingWorkflows = [];
        showNewWorkflowsNotification = false;
        newWorkflowsCount = 0;
        if (listContainer) listContainer.scrollTop = 0;
      } finally {
        isLoading = false;
      }
    }, 250);
  }



  /**
   * Component cleanup - unsubscribes from WebSocket and persists list state
   * so navigating away and back restores loaded count + scroll position.
   */
  onDestroy(() => {
    if (unsubscribeWS) {
      unsubscribeWS();
      unsubscribeWS = null;
    }
    writeSessionState(workflows.length, listContainer?.scrollTop || 0);
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

    // If scrolled to top with pending workflows, refresh the list. Pass the
    // current name filter so an active search isn't silently dropped.
    if (isScrolledToTop && showNewWorkflowsNotification) {
      workflows = await getWorkFlow(nameFilter || undefined, workflows.length + newWorkflowsCount, 0);
      showNewWorkflowsNotification = false;
      newWorkflowsCount = 0;
    }
  }
</script>

<!-- ----------- MAIN CONTAINER ----------- -->
<div class="wf-container" data-testid="wf-page">
  <div class="workflow-content">

    <!-- Search bar -->
    <div class="wf-search-row">
      <input
        type="text"
        class="wf-search-input"
        bind:value={nameFilter}
        on:input={onSearchInput}
        placeholder="Search workflows by name…"
        aria-label="Search workflows by name"
        data-testid="wf-search-input"
      />
    </div>

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

      {#if workflows.length > 0 && hasMoreWorkflows}
        <div class="load-more-row">
          <button
            class="load-more-btn"
            on:click={loadMoreWorkflows}
            disabled={isLoading}
            data-testid="wf-load-more"
          >
            {isLoading ? 'Loading…' : 'Load more'}
          </button>
        </div>
      {/if}
    </div>

  </div>
</div>

<style>
  .load-more-row {
    display: flex;
    justify-content: center;
    padding: 0.75rem 0 1.5rem;
  }
  .load-more-btn {
    padding: 0.4rem 1rem;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    cursor: pointer;
    font-size: 0.9rem;
  }
  .load-more-btn:hover:not(:disabled) {
    background: var(--primary-color);
    color: var(--primary-text);
    border-color: var(--primary-color);
  }
  .load-more-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
  /* Pad-left clears the fixed hamburger button (30px wide at left:0).
     The hamburger is fixed at top:1rem with height 36px (its visual center
     sits ~34px from top), so the row needs enough vertical room for the
     search input's center to align with the hamburger's center. */
  .wf-search-row {
    padding: 0.6rem 0.5rem 0.4rem 2.4rem;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
  }
  .wf-search-input {
    width: 100%;
    padding: 0.3rem 0.5rem;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-primary);
    color: var(--text-primary);
    font-size: 0.9rem;
    box-sizing: border-box;
  }
  .wf-search-input:focus {
    outline: none;
    border-color: var(--primary-color);
  }
</style>