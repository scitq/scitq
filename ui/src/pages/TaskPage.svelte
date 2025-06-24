<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    getAllTasks,
    getWorkFlow,
    getWorkers,
    getSteps,
    streamTaskLogsOutput,
    streamTaskLogsErr,
    getLogsBatch
  } from '../lib/api';
  import TaskList from '../components/TaskList.svelte';
  import { LogChunk, TaskLog } from '../../gen/taskqueue';
  import '../styles/tasks.css';
  import { ArrowBigUp } from 'lucide-svelte';

  // --- State variables ---

  let tasks: Task[] = [];
  let workers: Worker[] = [];
  let workflows: Workflow[] = [];
  let allSteps: Step[] = [];

  let selectedWorkerId: number | '' = '';
  let selectedWfId: number | '' = '';
  let selectedStepId: number | '' = '';
  let selectedCommand: string = '';
  let selectedStatus: string = '';

  let sortBy: 'task' | 'worker' | 'wf' | 'step' = 'task';

  let workflowSteps: Step[] = [];

  let intervalId: number;

  // Log handling
  const CHUNK_SIZE = 50;
  let hasMoreStdout = true;
  let hasMoreStderr = true;
  
  type LogSkip = {
    stdout: number;
    stderr: number;
  };
  let logSkipTracker: Record<number, LogSkip> = {};

  let taskLogsOut: Record<number, TaskLog[]> = {};
  let taskLogsErr: Record<number, TaskLog[]> = {};
  let taskLogsSaved: LogChunk[] = [];

  const streamedTasks = new Set<number>();

  // Modal and logs display
  let selectedTaskId: number | null = null;
  let showLogModal = false;
  let logsToShowOut: TaskLog[] = [];
  let logsToShowErr: TaskLog[] = [];

  let stdoutPre: HTMLPreElement | null = null;
  let stderrPre: HTMLPreElement | null = null;

  let isScrolledToBottomOut = true;
  let isScrolledToBottomErr = true;

  // --- Helper Functions ---

  /**
   * Parses filter parameters from URL hash
   * @returns {Object} Filters object containing:
   * @property {string|undefined} status - Task status filter
   * @property {number|undefined} workerId - Worker ID filter
   * @property {number|undefined} workflowId - Workflow ID filter
   * @property {number|undefined} stepId - Step ID filter
   */
  function getFiltersFromUrl() {
    const hash = window.location.hash;
    const queryPart = hash.includes('?') ? hash.split('?')[1] : '';
    const params = new URLSearchParams(queryPart);

    return {
      status: params.get('status') ?? undefined,
      workerId: params.get('workerId') ? Number(params.get('workerId')) : undefined,
      workflowId: params.get('workflowId') ? Number(params.get('workflowId')) : undefined,
      stepId: params.get('stepId') ? Number(params.get('stepId')) : undefined,
    };
  }

  /**
   * Updates task list based on current URL filters and sorting
   * @async
   */
  async function updateTasksFromUrl() {
    const filters = getFiltersFromUrl();

    selectedWorkerId = filters.workerId ?? '';
    selectedWfId = filters.workflowId ?? '';
    selectedStepId = filters.stepId ?? '';

    if (selectedWfId !== '') {
      workflowSteps = await getSteps(selectedWfId);
    } else {
      workflowSteps = [];
    }

    const latestTasks = await getAllTasks(
      filters.workerId,
      filters.workflowId,
      filters.stepId,
      filters.status,
      sortBy
    );

    if (Array.isArray(latestTasks)) {
      for (const task of latestTasks) {
        if (['R', 'U', 'V'].includes(task.status) && !streamedTasks.has(task.taskId)) {
          startStreaming(task);
        }
      }

      const oldIds = tasks.map(t => t.taskId).join(',');
      const newIds = latestTasks.map(t => t.taskId).join(',');
      const statusChanged = tasks.some((t, i) => t.status !== latestTasks[i]?.status);

      if (oldIds !== newIds || statusChanged) {
        tasks = latestTasks;
      }
    } else {
      console.error('getAllTasks() did not return an array:', latestTasks);
    }
  }

  /**
   * Updates URL hash with current filter parameters
   * @param {string} [status] - Optional status filter to apply
   */
  function handleStatusClick(status?: string) {
    const query = new URLSearchParams();

    if (status) query.set('status', status);
    if (selectedWorkerId) query.set('workerId', selectedWorkerId.toString());
    if (selectedWfId) query.set('workflowId', selectedWfId.toString());
    if (selectedStepId) query.set('stepId', selectedStepId.toString());

    window.location.hash = `/tasks?${query.toString()}`;
  }

  /**
   * Handles workflow selection change and updates related state
   * @async
   */
  async function handleWfClick() {
    selectedWfId = selectedWfId !== '' ? Number(selectedWfId) : '';
    workflowSteps = selectedWfId !== '' ? allSteps.filter(s => s.workflowId === selectedWfId) : [];
    selectedStepId = '';
    handleStatusClick();
  }

  /**
   * Starts streaming logs for a task
   * @param {Task} task - Task to stream logs for
   */
  function startStreaming(task: Task) {
    if (streamedTasks.has(task.taskId)) return;
    streamedTasks.add(task.taskId);

    taskLogsOut[task.taskId] = taskLogsOut[task.taskId] ?? [];
    taskLogsErr[task.taskId] = taskLogsErr[task.taskId] ?? [];

    /**
     * Handles incoming log entries
     * @param {TaskLog} log - The log entry to process
     */
    const handleLog = async (log: TaskLog) => {
      const maxLines = 50;

      if (log.logType === 'stdout') {
        taskLogsOut[task.taskId].push(log);
        if (taskLogsOut[task.taskId].length > maxLines) taskLogsOut[task.taskId].shift();
        taskLogsOut = { ...taskLogsOut };
      } else if (log.logType === 'stderr') {
        taskLogsErr[task.taskId].push(log);
        if (taskLogsErr[task.taskId].length > maxLines) taskLogsErr[task.taskId].shift();
        taskLogsErr = { ...taskLogsErr };
      }

      let savedEntry = taskLogsSaved.find(c => c.taskId === task.taskId);
      if (!savedEntry) {
        savedEntry = { taskId: task.taskId, stdout: [], stderr: [] };
        taskLogsSaved = [...taskLogsSaved, savedEntry];
      }

      if (log.logType === 'stdout') {
        savedEntry.stdout.push(log.logText);
        if (savedEntry.stdout.length > 2) savedEntry.stdout.shift();
      } else if (log.logType === 'stderr') {
        savedEntry.stderr.push(log.logText);
        if (savedEntry.stderr.length > 2) savedEntry.stderr.shift();
      }

      taskLogsSaved = [...taskLogsSaved];
      await tick();
    };

    streamTaskLogsOutput(task.taskId, handleLog);
    streamTaskLogsErr(task.taskId, handleLog);
  }

  /**
   * Loads older logs for a task in chunks
   * @async
   * @param {number} taskId - ID of task to load logs for
   * @param {'stdout'|'stderr'} logType - Type of logs to load
   */
  async function loadMoreLogs(taskId: number, logType: 'stdout' | 'stderr') {
    if (!logSkipTracker[taskId]) {
      logSkipTracker[taskId] = { stdout: 0, stderr: 0 };
    }

    const skip = logSkipTracker[taskId][logType];
    const logs = await getLogsBatch([taskId], CHUNK_SIZE, skip, logType) || [];
    const chunk = logs.find(l => l.taskId === taskId);

    if (!chunk) return;

    const newLogs = logType === 'stdout' 
      ? chunk.stdout.map(line => ({ logType: 'stdout', logText: line }))
      : chunk.stderr.map(line => ({ logType: 'stderr', logText: line }));

    if (newLogs.length === 0) {
      if (logType === 'stdout') hasMoreStdout = false;
      else hasMoreStderr = false;
      return;
    }

    if (logType === 'stdout') {
      taskLogsOut[taskId] = [...newLogs, ...(taskLogsOut[taskId] ?? [])];
      logSkipTracker[taskId].stdout += CHUNK_SIZE;
    } else {
      taskLogsErr[taskId] = [...newLogs, ...(taskLogsErr[taskId] ?? [])];
      logSkipTracker[taskId].stderr += CHUNK_SIZE;
    }

    await tick();
  }

  /**
   * Opens the log modal for a specific task
   * @async
   * @param {number} taskId - ID of task to show logs for
   */
  async function openLogModal(taskId: number) {
    isScrolledToBottomErr = true;
    isScrolledToBottomOut = true;
    selectedTaskId = taskId;

    const task = tasks.find(t => t.taskId === taskId);
    if (!task) return;

    selectedCommand = task.command;
    selectedStatus = task.status;

    if (['R', 'U', 'V'].includes(task.status)) {
      startStreaming(task);
    } else {
      taskLogsOut[taskId] = [];
      taskLogsErr[taskId] = [];
      logSkipTracker[taskId] = { stdout: 0, stderr: 0 };
      await loadMoreLogs(taskId, 'stdout');
      await loadMoreLogs(taskId, 'stderr');
    }

    showLogModal = true;
  }

  /**
   * Closes the log modal and resets related state
   */
  function closeLogModal() {
    showLogModal = false;
    selectedTaskId = null;
    selectedCommand = '';
    hasMoreStdout = true;
    hasMoreStderr = true;
  }

  /**
   * Scrolls the specified log panel to top
   * @param {'stdout'|'stderr'} logType - Which log panel to scroll
   */
  function scrollToTop(logType: 'stdout' | 'stderr') {
    if (logType === 'stdout' && stdoutPre) {
      stdoutPre.scrollTop = 0;
      stdoutPre.classList.add('scrolled-to-top');
      setTimeout(() => stdoutPre.classList.remove('scrolled-to-top'), 1000);
    } else if (logType === 'stderr' && stderrPre) {
      stderrPre.scrollTop = 0;
      stderrPre.classList.add('scrolled-to-top');
      setTimeout(() => stderrPre.classList.remove('scrolled-to-top'), 1000);
    }
  }

  // --- Lifecycle Hooks ---

  onMount(async () => {
    workers = await getWorkers();
    workflows = await getWorkFlow();
    const steps = await Promise.all(workflows.map(wf => getSteps(wf.workflowId)));
    allSteps = steps.flat();

    await updateTasksFromUrl();

    // Load saved logs for finished tasks
    const finishedTaskIds = tasks
      .filter(task => !['R', 'U', 'V'].includes(task.status))
      .map(task => task.taskId);

    if (finishedTaskIds.length > 0) {
      taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
    }

    window.addEventListener('hashchange', updateTasksFromUrl);
    intervalId = setInterval(updateTasksFromUrl, 1000);

    return () => {
      clearInterval(intervalId);
      window.removeEventListener('hashchange', updateTasksFromUrl);
    };
  });

  // --- Reactive Statements ---

  // Update logs to show when selectedTaskId changes
  $: if (selectedTaskId !== null) {
    logsToShowOut = taskLogsOut[selectedTaskId] ?? [];
    logsToShowErr = taskLogsErr[selectedTaskId] ?? [];
    console.log('Logs Out:', logsToShowOut.length, 'Logs Err:', logsToShowErr.length);
  }

  // Auto-scroll stdout to bottom when new logs appear
  $: if (logsToShowOut.length > 0) {
    tick().then(() => {
      setTimeout(() => {
        if (stdoutPre && isScrolledToBottomOut) stdoutPre.scrollTop = stdoutPre.scrollHeight;
      }, 0);
    });
  }

  // Auto-scroll stderr to bottom when new logs appear
  $: if (logsToShowErr.length > 0) {
    tick().then(() => {
      setTimeout(() => {
        if (stderrPre && isScrolledToBottomErr) stderrPre.scrollTop = stderrPre.scrollHeight;
      }, 0);
    });
  }
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
        <option value="step">Step</option>
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
      <select 
        id="wfSelect" 
        bind:value={selectedWfId} 
        on:change={() => {
          selectedWfId = selectedWfId !== '' ? Number(selectedWfId) : '';
          handleWfClick();
        }}
      >
        <option value="">All</option>
        {#each workflows as wf}
          <option value={wf.workflowId}>{wf.name}</option>
        {/each}
      </select>
    </div>

    {#if workflowSteps.length > 0}
      <div class="filter-group">
        <label for="stepSelect">Step</label>
        <select
          id="stepSelect"
          bind:value={selectedStepId}
          on:change={() => {
            selectedStepId = selectedStepId !== '' ? Number(selectedStepId) : '';
            handleStatusClick();
          }}
        >
          <option value="">All Steps</option>
          {#each workflowSteps as step}
            <option value={step.stepId}>{step.name}</option>
          {/each}
        </select>
      </div>
    {/if}

  </form>

  <!-- Status buttons -->
  <div class="status-filters-bar status-tabs">
    <button class="status-all" on:click={() => handleStatusClick()}>All</button>
    <button class="status-suspended" on:click={() => handleStatusClick('Z')}>Suspended</button>
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
    <button class="status-canceled" on:click={() => handleStatusClick('X')}>Canceled</button>
  </div>

  <!-- Filtered task list -->
  <TaskList {tasks} {taskLogsSaved} {workers} {workflows} {allSteps} onOpenModal={openLogModal} />
</div>

{#if showLogModal && selectedTaskId !== null}
  <div
    class="modal-backdrop"
    role="button"
    data-testid={`modal-log-${selectedTaskId}`}
    tabindex="0"
    aria-label="Close modal"
    on:click={closeLogModal}
    on:keydown={(e) => {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'Escape') closeLogModal();
    }}
  >
    <div
      class="modal terminal-style"
      role="dialog"
      aria-modal="true"
      tabindex="0"
      aria-labelledby="modal-title"
      on:click|stopPropagation
      on:keydown={(e) => {
        if (e.key === 'Escape') closeLogModal();
      }}
    >
      <h2 id="modal-title" class="modal-title">
        📜 Logs for Task {selectedTaskId}:
      </h2>
      <p class="command-preview"> {selectedCommand}</p>
      <div class="log-columns">
        {#if logsToShowOut.length > 0}
          <div class="log-block stdout-block">
            <h3 class="output-header">
              🟢 Output
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedStatus) && hasMoreStdout}
                <button
                  class="load-more-arrow"
                  on:click={() => { loadMoreLogs(selectedTaskId, 'stdout'); scrollToTop('stdout');; }}
                  title="Load more Output"
                  data-testid={`load-more-output-${selectedTaskId}`}
                  aria-label="Load more logs"
                >
                  <ArrowBigUp />
                </button>
              {/if}
            </h3>
              <pre
                bind:this={stdoutPre}
                class="log-pre"
                on:scroll={() => {
                  if (!stdoutPre) return;
                  // Threshold to consider if scrolled to bottom (e.g., 10px from bottom)
                  const threshold = 10;
                  const atBottom =
                    stdoutPre.scrollHeight - stdoutPre.scrollTop - stdoutPre.clientHeight < threshold;
                  isScrolledToBottomOut = atBottom;
                }}
              >
                {#each logsToShowOut as log}
{log.logText}
                {/each}
              </pre>
          </div>
        {/if}

        {#if logsToShowErr.length > 0}
          <div class="log-block stderr-block">
            <h3 class="error-header">
              🔴 Error
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedStatus) && hasMoreStderr}
                <button
                  class="load-more-arrow"
                  on:click={() => { loadMoreLogs(selectedTaskId, 'stderr'); scrollToTop('stderr');; }}
                  title="Load more Error"
                  aria-label="Load more error logs"
                >
                  <ArrowBigUp />
                </button>
              {/if}
            </h3>
              <pre
                bind:this={stderrPre}
                class="log-pre"
                on:scroll={() => {
                  if (!stderrPre) return;
                  // Threshold to consider if scrolled to bottom (e.g., 10px from bottom)
                  const threshold = 10;
                  const atBottom =
                    stderrPre.scrollHeight - stderrPre.scrollTop - stderrPre.clientHeight < threshold;
                  isScrolledToBottomErr = atBottom;
                }}
              >
                {#each logsToShowErr as log}
{log.logText}
                {/each}
              </pre>
          </div>
        {/if}
      </div>
      <button class="modal-close" on:click={closeLogModal}>Close</button>
    </div>
  </div>
{/if}
