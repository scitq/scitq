<script lang="ts">
  import { onMount } from 'svelte';
  import { getAllTasks, getWorkFlow, getWorkers } from '../lib/api';
  import TaskList from '../components/TaskList.svelte';
  import '../styles/tasks.css';

  let tasks: Task[] = [];

  // Dynamic filters
  let workers: Worker[] = [];
  let selectedWorkerId: number | '' = '';
  let workflows: Workflow[] = [];
  let selectedWfId: number | '' = '';
  let sortBy: 'task' | 'worker' | 'wf' = 'task';

  /**
   * Extract filters from URL hash query parameters.
   * Parses status, workerId, and workflowId from the URL hash.
   * @returns An object with optional status, workerId, and workflowId.
   */
  function getFiltersFromUrl() {
    const hash = window.location.hash;
    const queryPart = hash.includes('?') ? hash.split('?')[1] : '';
    const params = new URLSearchParams(queryPart);

    return {
      status: params.get('status') ?? undefined,
      workerId: params.get('workerId') ? Number(params.get('workerId')) : undefined,
      workflowId: params.get('workflowId') ? Number(params.get('workflowId')) : undefined
    };
  }

  /**
   * Loads tasks from API with optional filters.
   * Uses workerId, workflowId, status, and sortBy as parameters.
   * Updates the local tasks array with fetched data.
   * @param filters - Object containing optional status, workerId, workflowId.
   */
  async function loadTasks(filters: {
    status?: string;
    workerId?: number;
    workflowId?: number;
  }) {
    console.log('Calling getAllTasks with:', filters);
    tasks = await getAllTasks(
      filters.workerId,
      filters.workflowId,
      filters.status,
      sortBy
    );
  }

  /**
   * Updates task list and selected filters from current URL.
   * Parses filters from URL, updates selectedWorkerId and selectedWfId,
   * then loads filtered tasks.
   */
  async function updateTasksFromUrl() {
    const filters = getFiltersFromUrl();
    selectedWorkerId = filters.workerId ?? '';
    selectedWfId = filters.workflowId ?? '';
    await loadTasks(filters);
  }

  /**
   * Handles status filter button clicks.
   * Updates the URL hash with the selected filters and triggers filtering.
   * @param status - Optional status filter code (e.g., 'P', 'A', etc.).
   */
  function handleStatusClick(status?: string) {
    const query = new URLSearchParams();
    if (status) query.set('status', status);
    if (selectedWorkerId) query.set('workerId', selectedWorkerId.toString());
    if (selectedWfId) query.set('workflowId', selectedWfId.toString());

    window.location.hash = `/tasks?${query.toString()}`;
  }

  /**
   * Lifecycle method: on component mount,
   * fetch workers and workflows,
   * update tasks based on current URL filters,
   * and listen to URL hash changes for filtering updates.
   * Removes the event listener when component unmounts.
   */
  onMount(async () => {
    workers = await getWorkers();
    workflows = await getWorkFlow();
    
    await updateTasksFromUrl();
    window.addEventListener('hashchange', updateTasksFromUrl);
    return () => window.removeEventListener('hashchange', updateTasksFromUrl);
  });
</script>

<div class="tasks-container" data-testid="tasks-page">

  <!-- Filters -->
  <form class="filters-form" on:submit|preventDefault={() => handleStatusClick()}>
    <div class="filter-group">
      <label for="sortBy">Sort by</label>
      <select id="sortBy" bind:value={sortBy} on:change={() => handleStatusClick()}>
        <option value="task">Task</option>
        <option value="worker">Worker</option>
        <option value="wf">Workflow</option>
      </select>
    </div>

    <div class="filter-group">
      <label for="workerSelect">Worker</label>
      <select
        id="workerSelect"
        bind:value={selectedWorkerId}
        on:change={() => {
          selectedWorkerId = selectedWorkerId !== '' ? Number(selectedWorkerId) : '';
          handleStatusClick();
        }}
      >
        <option value="">All</option>
        {#each workers as worker}
          <option value={worker.workerId}>{worker.name}</option>
        {/each}
      </select>
    </div>

    <div class="filter-group">
      <label for="wfSelect">Workflow</label>
      <select id="wfSelect" bind:value={selectedWfId} on:change={() => handleStatusClick()}>
        <option value="">All</option>
        {#each workflows as wf}
          <option value={wf.workflowId}>{wf.name}</option>
        {/each}
      </select>
    </div>
  </form>

  <!-- Status buttons -->
  <div class="status-filters-bar status-tabs">
    <button class="status-all" on:click={() => handleStatusClick()}>All</button>
    <button class="status-pending" on:click={() => handleStatusClick('P')}>Pending</button>
    <button class="status-assigned" on:click={() => handleStatusClick('A')}>Assigned</button>
    <button class="status-accepted" on:click={() => handleStatusClick('C')}>Accepted</button>
    <button class="status-downloading" on:click={() => handleStatusClick('D')}>Downloading</button>
    <button class="status-waiting" on:click={() => handleStatusClick('W')}>Waiting</button>
    <button class="status-running" on:click={() => handleStatusClick('R')}>Running</button>
    <button class="status-uploading-as" on:click={() => handleStatusClick('U')}>Uploading (AS)</button>
    <button class="status-succeeded" on:click={() => handleStatusClick('S')}>Succeeded</button>
    <button class="status-uploading-af" on:click={() => handleStatusClick('V')}>Uploading (AF)</button>
    <button class="status-failed" on:click={() => handleStatusClick('F')}>Failed</button>
    <button class="status-suspended" on:click={() => handleStatusClick('Z')}>Suspended</button>
    <button class="status-canceled" on:click={() => handleStatusClick('X')}>Canceled</button>
  </div>

  <!-- Filtered task list -->
  <TaskList {tasks} />
</div>
