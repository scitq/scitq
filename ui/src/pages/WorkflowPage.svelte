<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getWorkFlow, getWorkers, getListUser } from '../lib/api';
  // Workers and mapping by stepId (passed down to WorkflowList → StepList)
  let workers: taskqueue.Worker[] = [];
  let workersPerStepId: Map<number, taskqueue.Worker[]> = $state(new Map());
  import { wsClient } from '../lib/wsClient';
  import { wfCounters } from '../lib/wfCounters';
  import WorkflowList from '../components/WorkflowList.svelte';
  import '../styles/workflow.css';

  // Array of currently displayed workflows
  // $state.raw — opt out of deep proxying. With 25+ workflows × ~15 field
  // reads per row in WorkflowList, $state([]) was deep-proxying every
  // element and Svelte 5's reactive scheduler tipped into pathological
  // (effectively-frozen) flushes on any list reassignment. Workflow
  // objects are replaced wholesale on update, not mutated in place, so
  // shallow reactivity is the right model here.
  let workflows = $state.raw([]);
  // Array of newly created workflows waiting to be displayed
  let pendingWorkflows = [];    
  // Flag indicating if more workflows are available to load
  let hasMoreWorkflows = $state(true);
  // Loading state flag
  let isLoading = $state(false);
  // Reference to the list container DOM element
  let listContainer: HTMLDivElement = $state();
  // Flag indicating if list is scrolled to top
  let isScrolledToTop = true;
  // Count of new workflows waiting to be displayed
  let newWorkflowsCount = $state(0);
  // Flag to show new workflows notification
  let showNewWorkflowsNotification = $state(false);

  // Number of workflows to load at once
  const WORKFLOWS_CHUNK_SIZE = 25;

  // Persistence: search filter goes to URL hash (link-shareable, survives reload),
  // pagination count + scroll go to sessionStorage (transient, restore-on-return).
  const ROUTE_HASH = '#/workflows';
  const STORAGE_KEY = 'wf:state';

  // URL hash is the source of truth for page-level filters (search, user)
  // so navigation/reload/link-share all preserve the same view. Empty
  // values are omitted from the hash to keep the URL minimal.
  function readUrlParam(name: string): string {
    const hash = window.location.hash;
    const qIdx = hash.indexOf('?');
    if (qIdx < 0) return '';
    return new URLSearchParams(hash.slice(qIdx + 1)).get(name) || '';
  }

  function writeUrlParams(updates: Record<string, string>) {
    const hash = window.location.hash;
    const qIdx = hash.indexOf('?');
    const params = new URLSearchParams(qIdx >= 0 ? hash.slice(qIdx + 1) : '');
    for (const [k, v] of Object.entries(updates)) {
      if (v === '' || v === null || v === undefined) params.delete(k);
      else params.set(k, v);
    }
    const qs = params.toString();
    const newHash = qs ? `${ROUTE_HASH}?${qs}` : ROUTE_HASH;
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
  let nameFilter = $state(readUrlParam('search'));
  let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

  // User filter, also in the URL hash (same survival/sharing semantics as
  // the search box). The sentinel "@me" means "the authenticated caller"
  // (the server resolves it from the session). Empty string = "All users".
  // Default on first visit: "@me".
  //   URL ?user=@me      → current user
  //   URL ?user=alice    → that user
  //   URL with no `user` → all users (explicit "All users" choice)
  // The first-load fallback (when the URL doesn't yet have a `user`
  // param at all) uses "@me" so a fresh visit shows the caller's own
  // workflows by default.
  const currentUsername = (typeof localStorage !== 'undefined' && localStorage.getItem('username')) || '';
  // Other users for the dropdown. Loaded once on mount via getListUser (any
  // logged-in user can list). We include everyone; selecting a user with
  // no workflows simply shows an empty list — cheap, no special server
  // round-trip to compute "users with workflows", and we explicitly avoid
  // deriving the list from already-loaded workflows since pagination
  // would silently hide users whose runs haven't been fetched yet.
  let otherUsers: string[] = $state([]);
  let userFilter: string = $state((() => {
    const hash = window.location.hash;
    const qIdx = hash.indexOf('?');
    const params = qIdx >= 0 ? new URLSearchParams(hash.slice(qIdx + 1)) : new URLSearchParams();
    if (params.has('user')) return params.get('user') || '';  // explicit empty = All users
    return '@me';                                              // never visited → default
  })());

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
    workflows = await getWorkFlow(nameFilter || undefined, targetCount, 0, userFilter || undefined);
    wfCounters.seedMany(workflows);
    // If we asked for N and got exactly N back, assume more might exist.
    // Worst case: a no-op "Load more" click (acceptable).
    hasMoreWorkflows = workflows.length === targetCount;
    workers = await getWorkers();
    workersPerStepId = mapWorkersToStepIds(workers);
    // Populate the user-filter dropdown. Exclude the current user from the
    // "other users" list so they don't appear twice (once as "me", once by name).
    try {
      const allUsers = await getListUser();
      otherUsers = allUsers
        .map(u => u.username)
        .filter(n => !!n && n !== currentUsername)
        .sort((a, b) => a.localeCompare(b));
    } catch {
      otherUsers = []; // dropdown degrades gracefully to "me / all users" only
    }
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
      const next = await getWorkFlow(nameFilter || undefined, WORKFLOWS_CHUNK_SIZE, workflows.length, userFilter || undefined);
      // Filter out any overlap caused by concurrent deletions/insertions.
      const seen = new Set(workflows.map(w => w.workflowId));
      const fresh = next.filter(w => !seen.has(w.workflowId));
      workflows = [...workflows, ...fresh];
      wfCounters.seedMany(fresh);
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
      writeUrlParams({ search: nameFilter });
      clearSessionState();
      isLoading = true;
      try {
        workflows = await getWorkFlow(nameFilter || undefined, WORKFLOWS_CHUNK_SIZE, 0, userFilter || undefined);
        wfCounters.seedMany(workflows);
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

  // Re-fetch when the user filter changes. No debounce — it's a discrete
  // dropdown event, not typing.
  async function onUserFilterChange() {
    // "All users" means: drop the URL param entirely. Otherwise write
    // the literal value (including "@me", so a shared link keeps the
    // "current user" semantics for whoever opens it).
    writeUrlParams({ user: userFilter });
    clearSessionState();
    isLoading = true;
    try {
      workflows = await getWorkFlow(nameFilter || undefined, WORKFLOWS_CHUNK_SIZE, 0, userFilter || undefined);
      hasMoreWorkflows = workflows.length === WORKFLOWS_CHUNK_SIZE;
      pendingWorkflows = [];
      showNewWorkflowsNotification = false;
      newWorkflowsCount = 0;
      if (listContainer) listContainer.scrollTop = 0;
    } finally {
      isLoading = false;
    }
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

        // Filter-match check. The server broadcasts workflow/created
        // for every new workflow regardless of subscriber filter, so
        // the client has to drop events that don't match the active
        // userFilter / nameFilter. Without this check the displayed
        // list silently grows with rows that don't match the user's
        // current view, which (a) is wrong semantically and (b) was
        // partly implicated in the sparse-list reactive-flush freeze.
        const effectiveUserFilter = userFilter === '@me' ? currentUsername : userFilter;
        const matchesUserFilter = !effectiveUserFilter || newWf.createdByUsername === effectiveUserFilter;
        const matchesNameFilter = !nameFilter || (typeof newWf.name === 'string' && newWf.name.toLowerCase().includes(nameFilter.toLowerCase()));
        if (!matchesUserFilter || !matchesNameFilter) return;

        const existsInDisplayed = workflows.some(wf => wf.workflowId === newWf.workflowId);
        const existsInPending = pendingWorkflows.some(wf => wf.workflowId === newWf.workflowId);
        if (!existsInDisplayed && !existsInPending) {
          if (isScrolledToTop) {
            workflows = [newWf, ...workflows];
            wfCounters.seed(newWf.workflowId, newWf);
            console.log('workflow created via WebSocket:', newWf.workflowId);
          } else {
            pendingWorkflows = [newWf, ...pendingWorkflows];
            wfCounters.seed(newWf.workflowId, newWf);
            newWorkflowsCount = pendingWorkflows.length;
            showNewWorkflowsNotification = true;
          }
        }
        return;
      }

      if (action === 'deleted') {
        const idToRemove = typeof p.workflowId === 'number' ? p.workflowId : message.id;
        if (typeof idToRemove === 'number') {
          // Same order rationale as the 'created' branch: reassign the
          // workflows array BEFORE notifying the wfCounters store.
          workflows = workflows.filter(wf => wf.workflowId !== idToRemove);
          pendingWorkflows = pendingWorkflows.filter(wf => wf.workflowId !== idToRemove);
          wfCounters.drop(idToRemove);
        }
        return;
      }

      if (action === 'status') {
        const wfId = message.id;
        const newStatus = p.status;
        if (typeof wfId === 'number' && typeof newStatus === 'string') {
          // Index-assignment, not whole-array map: only this row's
          // bindings re-evaluate. See StepList.svelte for the same
          // pattern + rationale (keyed-each `tl()` walk avoidance).
          // $state.raw: index-set on the raw array doesn't fire reactivity;
          // we have to reassign the array reference.
          const idx = workflows.findIndex(wf => wf.workflowId === wfId);
          if (idx !== -1) {
            const next = workflows.slice();
            next[idx] = { ...next[idx], status: newStatus };
            workflows = next;
          }
        }
        return;
      }
    }

    // Step-stats delta: update per-workflow counters via the sideband
    // store. We deliberately avoid `workflows[idx] = updated` here.
    // Invalidating the `workflows` array forces WorkflowList's
    // keyed-each to re-run block.p across every row, which in turn
    // calls $set on every child component (StepList, lucide icons,
    // ...). Svelte 4's `safe_not_equal` returns true for any object
    // prop, so each child treats its props as dirty even when refs
    // are unchanged. On the workflows page with megahit expanded that
    // cascade allocated 1+ GB in a single delta and crashed the
    // renderer. Writing to the wfCounters store only invalidates the
    // counter-reading bindings in WorkflowList's row template; the
    // keyed-each itself stays put and no child $set fires.
    if (message?.type === 'step-stats' && message?.action === 'delta') {
      const p = message.payload || {};
      const wfId = p.workflowId;
      if (typeof wfId !== 'number') return;
      wfCounters.applyDelta(wfId, p.oldStatus, p.newStatus, !!p.retried);
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
    // current name + user filters so the refresh doesn't silently widen.
    if (isScrolledToTop && showNewWorkflowsNotification) {
      workflows = await getWorkFlow(nameFilter || undefined, workflows.length + newWorkflowsCount, 0, userFilter || undefined);
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
        oninput={onSearchInput}
        placeholder="Search workflows by name…"
        aria-label="Search workflows by name"
        data-testid="wf-search-input"
      />
      <select
        class="wf-user-select"
        bind:value={userFilter}
        onchange={onUserFilterChange}
        aria-label="Filter workflows by user"
        data-testid="wf-user-select"
      >
        <option value="@me">{currentUsername ? `${currentUsername} (me)` : 'Me'}</option>
        <option value="">All users</option>
        {#each otherUsers as u}
          <option value={u}>{u}</option>
        {/each}
        <!-- Stale-URL safeguard: if the URL pins a username that's not in
             the loaded list (deleted user, mid-load before getListUser
             returns, …), surface it as an extra option so the <select>
             reflects the actual filter instead of silently snapping to the
             first option. -->
        {#if userFilter && userFilter !== '@me' && userFilter !== '' && userFilter !== currentUsername && !otherUsers.includes(userFilter)}
          <option value={userFilter}>{userFilter}</option>
        {/if}
      </select>
    </div>

    <!-- New workflows notification button -->
    {#if showNewWorkflowsNotification}
      <button 
        class="new-workflows-notification" 
        onclick={loadNewWorkflows}
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
      onscroll={handleScroll}
    >
      <WorkflowList {workflows} workersPerStepId={workersPerStepId} />

      {#if workflows.length > 0 && hasMoreWorkflows}
        <div class="load-more-row">
          <button
            class="load-more-btn"
            onclick={loadMoreWorkflows}
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
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }
  .wf-search-input {
    flex: 1;
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
  .wf-user-select {
    /* Fixed narrow width so the search box (flex: 1) gets the lion's
       share of the row. Long usernames are truncated visually in the
       collapsed state, but the open dropdown renders at content width
       so picking them is fine. */
    flex: 0 0 9rem;
    width: 9rem;
    padding: 0.3rem 0.5rem;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-primary);
    color: var(--text-primary);
    font-size: 0.9rem;
    box-sizing: border-box;
    cursor: pointer;
  }
  .wf-user-select:focus {
    outline: none;
    border-color: var(--primary-color);
  }
</style>