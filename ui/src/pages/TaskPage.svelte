<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { wsClient } from '../lib/wsClient';
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
  import { ArrowBigUp, Search, X} from 'lucide-svelte';

  // --- State Management ---
  let workers: Worker[] = [];
  let workflows: Workflow[] = [];
  let allSteps: Step[] = [];

  // Filtering and sorting state
  let selectedWorkerId: number = undefined;
  let selectedWfId: number = undefined;
  let selectedStepId: number = undefined;
  let selectedCommand: string = '';
  let selectedStatus: string = '';
  let selectedTaskStatus: string = '';
  let selectedTaskCommand: String = undefined;
  let sortBy: 'task' | 'worker' | 'wf' | 'step' = 'task';
  let workflowSteps: Step[] = [];

  // Log management state
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

  // UI state
  let selectedTaskId: number | null = null;
  let showLogModal = false;
  let logsToShowOut: TaskLog[] = [];
  let logsToShowErr: TaskLog[] = [];
  let stdoutPre: HTMLPreElement | null = null;
  let stderrPre: HTMLPreElement | null = null;
  let isScrolledToBottomOut = true;
  let isScrolledToBottomErr = true;

  // Task list pagination
  const TASKS_CHUNK_SIZE = 25;
  let displayedTasks: Task[] = [];
  let firstTasks: Task[] = [];
  let pendingTasks: Task[] = []; // New pending tasks
  let hasMoreTasks = true;
  let isLoading = false;
  let tasksContainer: HTMLDivElement;
  let isScrolledToTop = false;
  let isScrolledToBottom = false;
  let lastScrollPosition = 0;
  let showNewTasksNotification = false;
  let unsubscribeWS: () => void;

async function handleWebSocketMessage(message) {
  if (message.type === 'task-created') {
    // Create a complete task object with received data
    const newTask: Task = {
      taskId: message.payload.taskId,
      command: message.payload.command || '',
      status: message.payload.status || 'P',
      stepId: message.payload.stepId || null,
      workerId: null, // To be filled if available in the message
      workflowId: null, // To be filled if available in the message
      taskName: message.payload.taskName || null,
    };

    // Add to the appropriate list
    if (!isScrolledToTop) {
      pendingTasks = [...pendingTasks, newTask];
      showNewTasksNotification = true;
    } else {
      displayedTasks = [newTask, ...displayedTasks];
      firstTasks = [newTask, ...firstTasks];
      const finishedTaskIds = displayedTasks
        .filter(task => !['R', 'U', 'V'].includes(task.status))
        .map(task => task.taskId);

      if (finishedTaskIds.length > 0) {
        taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
      }
    }
    
    // Start streaming if needed
    if (['R', 'U', 'V'].includes(newTask.status)) {
      startStreaming(newTask);
    }
  }
  else if (message.type === 'task-updated') {
    const updatedTaskId = message.payload.taskId;
    const newStatus = message.payload.status;
    const workerId = message.payload.workerId;

    console.log('Task updated:', {updatedTaskId, newStatus, workerId});

    // Helper function to update a task in an array
    const updateTaskInArray = (tasks: Task[]) => {
      return tasks.map(task => {
        if (task.taskId === updatedTaskId) {
          const updatedTask = {
            ...task,
            status: newStatus
          };
          
          if (workerId !== undefined) {
            updatedTask.workerId = workerId;
            
            if (workers.length > 0) {
              const worker = workers.find(w => w.workerId === workerId);
              if (worker) {
                updatedTask.workerName = worker.name;
              }
            }
          }
          
          return updatedTask;
        }
        return task;
      });
    };

    // Update in displayedTasks
    displayedTasks = updateTaskInArray(displayedTasks);
    
    // Update in firstTasks
    firstTasks = updateTaskInArray(firstTasks);
    
    // Update in pendingTasks if the task is there
    pendingTasks = updateTaskInArray(pendingTasks);

    // Start streaming if needed
    if (['R', 'U', 'V'].includes(newStatus)) {
      // Check in displayedTasks first
      let task = displayedTasks.find(t => t.taskId === updatedTaskId);
      
      // If not found, check in pendingTasks
      if (!task) {
        task = pendingTasks.find(t => t.taskId === updatedTaskId);
      }
      
      if (task && !streamedTasks.has(task.taskId)) {
        startStreaming(task);
      }
    }
  }
}

  // --- Core Functions ---

  /**
   * Loads initial task batch with current filters
   * @async
   * @returns {Promise<void>} Resolves when initial load completes
   */
  async function loadInitialTasks() {
    displayedTasks = await getAllTasks(
      selectedWorkerId,
      selectedWfId,
      selectedStepId,
      selectedStatus,
      sortBy,
      selectedCommand,
      TASKS_CHUNK_SIZE,
      0
    );
    firstTasks = [...displayedTasks];
    hasMoreTasks = displayedTasks.length === TASKS_CHUNK_SIZE;
    
    // Start streaming for running tasks
    for (const task of displayedTasks) {
      if (['R', 'U', 'V'].includes(task.status) && !streamedTasks.has(task.taskId)) {
        startStreaming(task);
      }
    }
  }
  /**
   * Parses filter parameters from URL hash
   * @returns {Object} Filter parameters object
   */
  function getFiltersFromUrl() {
    const hash = window.location.hash;
    const queryPart = hash.includes('?') ? hash.split('?')[1] : '';
    const params = new URLSearchParams(queryPart);

    const parseNumber = (value: string | null): number | undefined => {
      const num = Number(value);
      return value && !isNaN(num) ? num : undefined;
    };
    return {
      status: params.get('status') ?? undefined,
      workerId: parseNumber(params.get('workerId')),
      workflowId: parseNumber(params.get('workflowId')),
      stepId: parseNumber(params.get('stepId')),
      command: params.get('command') ? decodeURIComponent(params.get('command')) : undefined
    };
  }

  /**
   * Updates task list based on URL filters
   * @async
   * @returns {Promise<void>} Resolves when update completes
   */
  async function updateTasksFromUrl() {
    const filters = getFiltersFromUrl();

    selectedWorkerId = filters.workerId ?? undefined;
    selectedWfId = filters.workflowId ?? undefined;
    selectedStepId = filters.stepId ?? undefined;
    selectedStatus = filters.status ?? undefined;

    if (selectedWfId !== undefined) {
      workflowSteps = await getSteps(selectedWfId);
    } else {
      workflowSteps = [];
    }

    isLoading = true;
    try {
      const initialLoad = await getAllTasks(
        selectedWorkerId,
        selectedWfId,
        selectedStepId,
        selectedStatus,
        sortBy,
        selectedCommand,
        TASKS_CHUNK_SIZE,
        0
      );
    
      if (Array.isArray(initialLoad)) {
        displayedTasks = initialLoad;
        if(!selectedCommand){
          firstTasks = initialLoad;
        }
        else {
          firstTasks = await getAllTasks(
            selectedWorkerId,
            selectedWfId,
            selectedStepId,
            selectedStatus,
            sortBy,
            undefined,
            TASKS_CHUNK_SIZE,
            0
           );
        }
        hasMoreTasks = initialLoad.length === TASKS_CHUNK_SIZE;
        
        // Start streaming for running tasks
        for (const task of initialLoad) {
        if (['R', 'U', 'V'].includes(task.status) && !streamedTasks.has(task.taskId)) {
          startStreaming(task);
        }
      }

        const finishedTaskIds = displayedTasks
          .filter(task => !['R', 'U', 'V'].includes(task.status))
          .map(task => task.taskId);

        if (finishedTaskIds.length > 0) {
          taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
        }
      }
    } finally {
      isLoading = false;
    }
  }

  /**
   * Loads newly arrived tasks
   */
  function loadNewTasks() {
    if (pendingTasks.length === 0) return;
    
    // Reset if in search mode
    if (selectedCommand) {
      selectedCommand = undefined;
      displayedTasks = [...firstTasks];
      hasMoreTasks = firstTasks.length === TASKS_CHUNK_SIZE;
      showNewTasksNotification = false;
      pendingTasks = [];
      return;
    }

    // Add pending tasks to the top
    displayedTasks = [...pendingTasks, ...displayedTasks];
    firstTasks = [...pendingTasks, ...firstTasks];
    pendingTasks = [];
    showNewTasksNotification = false;
    
    if (tasksContainer) {
      tasksContainer.scrollTo({
        top: 0,
        behavior: 'smooth'
      });
    }
  }

  /**
   * Loads additional tasks for infinite scroll
   * @async
   * @returns {Promise<void>} Resolves when loading completes
   */
  async function loadMoreTasks() {
    if (isLoading || !hasMoreTasks) return;
    
    isLoading = true;
    try {
        const additionalTasks = await getAllTasks(
            selectedWorkerId,
            selectedWfId,
            selectedStepId,
            selectedStatus,
            sortBy,
            selectedCommand,
            TASKS_CHUNK_SIZE,
            displayedTasks.length
        );

        if (additionalTasks.length > 0) {
            // Merge avoiding duplicates
            const mergedTasks = [...new Map([...displayedTasks, ...additionalTasks].map(task => [task.taskId, task])).values()];
            displayedTasks = mergedTasks;
            displayedTasks = handleSortBy();
            hasMoreTasks = additionalTasks.length === TASKS_CHUNK_SIZE;
            
            await tick();
            
            if (tasksContainer && !isScrolledToTop) {
                tasksContainer.scrollTop = lastScrollPosition;
            }

            const finishedTaskIds = displayedTasks
            .filter(task => !['R', 'U', 'V'].includes(task.status))
            .map(task => task.taskId);

          if (finishedTaskIds.length > 0) {
            taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
          }
        } else {
            hasMoreTasks = false;
        }
    } catch (error) {
        console.error("Error loading more tasks:", error);
    } finally {
        isLoading = false;
    }
  }

  /**
   * Updates URL with current filters
   * @param {string} [status] - Optional status filter to apply
   */
  function handleStatusClick(status?: string) {
    const query = new URLSearchParams();

    if (status) query.set('status', status);
    if (selectedWorkerId) query.set('workerId', selectedWorkerId.toString());
    if (selectedWfId) query.set('workflowId', selectedWfId.toString());
    if (selectedStepId) query.set('stepId', selectedStepId.toString());
    if (selectedCommand) {
        query.set('command', encodeURIComponent(selectedCommand));
    }

    window.location.hash = `/tasks?${query.toString()}`;
  }

  /**
   * Handles workflow selection change
   * @async
   * @returns {Promise<void>} Resolves when steps are loaded
   */
  async function handleWfClick() {
    selectedWfId = selectedWfId !== '' ? Number(selectedWfId) : '';
    workflowSteps = selectedWfId !== '' ? allSteps.filter(s => s.workflowId === selectedWfId) : [];
    selectedStepId = '';
    handleStatusClick();
  }

  /**
   * Sorts tasks based on current sort criteria
   * @returns {Task[]} Sorted task array
   */
  function handleSortBy() {
    if (!displayedTasks) return [];

    const sortedTasks = [...displayedTasks].sort((a, b) => {
      switch (sortBy) {
        case 'task':
          return (b.taskId ?? 0) - (a.taskId ?? 0);
        case 'worker':
          return (a.workerId ?? 0) - (b.workerId ?? 0);
        case 'wf':
          return (a.workflowId ?? 0) - (b.workflowId ?? 0);
        case 'step':
          return (a.stepId ?? 0) - (b.stepId ?? 0);
        default:
          return 0;
      }
    });
    
    return sortedTasks;
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
     * Processes incoming log entries
     * @param {TaskLog} log - Log entry to process
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
   * Loads older logs for a task
   * @async
   * @param {number} taskId - Task ID to load logs for
   * @param {'stdout'|'stderr'} logType - Log type to load
   * @returns {Promise<void>} Resolves when logs are loaded
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
   * Opens log modal for a task
   * @async
   * @param {number} taskId - Task ID to show logs for
   * @returns {Promise<void>} Resolves when modal is ready
   */
  async function openLogModal(taskId: number) {
    isScrolledToBottomErr = true;
    isScrolledToBottomOut = true;
    selectedTaskId = taskId;

    const task = displayedTasks.find(t => t.taskId === taskId);
    if (!task) return;

    selectedTaskCommand = task.command;
    selectedTaskStatus = task.status;

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
   * Closes log modal
   */
  function closeLogModal() {
    showLogModal = false;
    selectedTaskId = null;
    selectedTaskCommand = '';
    hasMoreStdout = true;
    hasMoreStderr = true;
  }

  /**
   * Scrolls log panel to top
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

    // --- Lifecycle Management ---
  onMount(async () => {
    try {
      // Parallel data loading
      const [workersData, workflowsData] = await Promise.all([
        getWorkers(),
        getWorkFlow()
      ]);
      
      workers = workersData;
      workflows = workflowsData;
      
      // Load steps for all workflows
      const allStepsArrays = await Promise.all(workflows.map(wf => getSteps(wf.workflowId)));
      allSteps = allStepsArrays.reduce((acc, steps) => acc.concat(steps), []);
      
      // Initial task load
      await updateTasksFromUrl();

      const finishedTaskIds = displayedTasks
        .filter(task => !['R', 'U', 'V'].includes(task.status))
        .map(task => task.taskId);

      if (finishedTaskIds.length > 0) {
        taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
      }

      // Setup WebSocket subscription
      unsubscribeWS = wsClient.subscribeToMessages(handleWebSocketMessage);

      // URL change listener
      window.addEventListener('hashchange', updateTasksFromUrl);

      // Initial scroll position check
      if (tasksContainer) {
        handleScroll();
      }
    } catch (error) {
      console.error("Initialization error:", error);
    }

    return () => {
      // Cleanup
      unsubscribeWS?.();
      window.removeEventListener('hashchange', updateTasksFromUrl);
    };
  });

  let lastScrollTrigger = 0;

  /**
   * Handles scroll events for infinite loading
   */
  function handleScroll() {
    if (!tasksContainer || isLoading) return;

    lastScrollPosition = tasksContainer.scrollTop;

    const { scrollTop, scrollHeight, clientHeight } = tasksContainer;
    const scrollPosition = scrollTop + clientHeight;
    const threshold = 150;

    // Detect top of page
    isScrolledToTop = scrollTop <= 10;

    // Load new tasks if scrolled to top
    if (isScrolledToTop && pendingTasks.length > 0) {
      loadNewTasks();
    }

    // Detect bottom of page
    const distanceFromBottom = scrollHeight - scrollPosition;
    if (distanceFromBottom <= threshold && hasMoreTasks && !isLoading) {
      loadMoreTasks();
    }
  }

  // --- Reactive Statements ---

  // Update logs when selected task changes
  $: if (selectedTaskId !== null) {
    logsToShowOut = taskLogsOut[selectedTaskId] ?? [];
    logsToShowErr = taskLogsErr[selectedTaskId] ?? [];
  }

  // Auto-scroll stdout when new logs arrive
  $: if (logsToShowOut.length > 0) {
    tick().then(() => {
      setTimeout(() => {
        if (stdoutPre && isScrolledToBottomOut) stdoutPre.scrollTop = stdoutPre.scrollHeight;
      }, 0);
    });
  }

  // Auto-scroll stderr when new logs arrive
  $: if (logsToShowErr.length > 0) {
    tick().then(() => {
      setTimeout(() => {
        if (stderrPre && isScrolledToBottomErr) stderrPre.scrollTop = stderrPre.scrollHeight;
      }, 0);
    });
  }
</script>

<div class="tasks-container" data-testid="tasks-page">
  {#if showNewTasksNotification}
    <div 
      class="new-tasks-notification"
      data-testid={`tasks-notification-${pendingTasks.length}`}
      on:click={loadNewTasks}
      on:keydown={e => e.key === 'Enter' && loadNewTasks()}
      tabindex="0"
      role="button"
      aria-label={`Show ${pendingTasks.length} new task${pendingTasks.length > 1 ? 's' : ''}`}
    >
      {pendingTasks.length} new task{pendingTasks.length > 1 ? 's' : ''} available
      <button class="show-new-btn" on:click={loadNewTasks}>Show</button>
    </div>
  {/if}


  <!-- Filters -->
  <form class="tasks-filters-form" on:submit|preventDefault={() => handleStatusClick()}>

    <div class="tasks-filter-group tasks-search-container {isLoading ? 'searching' : ''}">
      <label for="command">Command</label>
      <div class="search-input-wrapper">
          <input
            id="command"
            type="text"
            bind:value={selectedCommand}
            placeholder="Search commands..."
            aria-label="Search tasks by command"
            on:keydown={(e) => e.key === 'Enter' && handleStatusClick()}
          />
        <div class="search-icons">
          {#if selectedCommand}
            <button 
              type="button" 
              on:click={() => {
                selectedCommand = '';
                handleStatusClick();
              }}
              class="clear-button"
              aria-label="Clear search"
            >
              <X size={16}/>
            </button>
          {/if}
          <button 
            type="button" 
            on:click={() => handleStatusClick()}
            class="search-button"
            disabled={isLoading}
            aria-label={isLoading ? "Searching..." : "Search"}
          >
            {#if isLoading}
              <span class="loading-spinner" aria-hidden="true"></span>
            {:else}
              <Search size={16}/>
            {/if}
          </button>
        </div>
      </div>
    </div>

    <div class="tasks-filter-group">
      <label for="sortBy">Sort by</label>
      <select id="sortBy" bind:value={sortBy} on:change={() => {
          if (displayedTasks) {
              displayedTasks = handleSortBy();
          }
      }}>
        <option value="task">Task</option>
        <option value="worker">Worker</option>
        <option value="wf">Workflow</option>
        <option value="step">Step</option>
      </select>
    </div>

    <div class="tasks-filter-group">
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

    <div class="tasks-filter-group">
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
      <div class="tasks-filter-group">
        <label for="stepSelect">Step</label>
        <select
          id="stepSelect"
          bind:value={selectedStepId}
          on:change={() => {
            selectedStepId = selectedStepId !== '' ? Number(selectedStepId) : undefined;
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
  <div class="tasks-status-filters-bar tasks-status-tabs">
    <button class="tasks-status-all" on:click={() => handleStatusClick()}>All</button>
    <button class="tasks-status-pending" on:click={() => handleStatusClick('P')}>Pending</button>
    <button class="tasks-status-assigned" on:click={() => handleStatusClick('A')}>Assigned</button>
    <button class="tasks-status-accepted" on:click={() => handleStatusClick('C')}>Accepted</button>
    <button class="tasks-status-downloading" on:click={() => handleStatusClick('D')}>Downloading</button>
    <button class="tasks-status-running" on:click={() => handleStatusClick('R')}>Running</button>
    <button class="tasks-status-uploading-as" on:click={() => handleStatusClick('U')}>Uploading (AS)</button>
    <button class="tasks-status-succeeded" on:click={() => handleStatusClick('S')}>Succeeded</button>
    <button class="tasks-status-uploading-af" on:click={() => handleStatusClick('V')}>Uploading (AF)</button>
    <button class="tasks-status-failed" on:click={() => handleStatusClick('F')}>Failed</button>
    <button class="tasks-status-waiting" on:click={() => handleStatusClick('W')}>Waiting</button>
    <button class="tasks-status-suspended" on:click={() => handleStatusClick('Z')}>Suspended</button>
    <button class="tasks-status-canceled" on:click={() => handleStatusClick('X')}>Canceled</button>
  </div>

  <div class="tasks-list-container" bind:this={tasksContainer} on:scroll={handleScroll}>

      <!-- Filtered task list -->
  <TaskList 
    displayedTasks={displayedTasks}
    {taskLogsSaved}
    {workers}
    {workflows}
    {allSteps}
    onOpenModal={openLogModal}
  />

  {#if isLoading}
    <div class="loading-indicator">Loading...</div>
  {:else if !hasMoreTasks}
    <p class="workerCompo-empty-state">No more task.</p>
  {/if}

  </div>

</div>

{#if showLogModal && selectedTaskId !== null}
  <div
    class="tasks-modal-backdrop"
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
      class="tasks-modal terminal-style"
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
      <p class="tasks-command-preview"> {selectedTaskCommand}</p>
      <div class="tasks-log-columns">
        {#if logsToShowOut.length > 0}
          <div class="tasks-log-block tasks-stdout-block">
            <h3 class="tasks-output-header">
              🟢 Output
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedTaskStatus) && hasMoreStdout}
                <button
                  class="tasks-load-more-arrow"
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
                class="tasks-log-pre"
                on:scroll={() => {
                  if (!stdoutPre) return;
                  // Threshold to consider if scrolled to bottom (e.g., 10px from bottom)
                  const threshold = 10;
                  const atBottom =
                    stdoutPre.scrollHeight - stdoutPre.scrollTop - stdoutPre.clientHeight < threshold;
                  isScrolledToBottomOut = atBottom;
                }}
              >
                {#each logsToShowOut as log, i (log.logText + i)}
{log.logText}
                {/each}
              </pre>
          </div>
        {/if}

        {#if logsToShowErr.length > 0}
          <div class="tasks-log-block tasks-stderr-block">
            <h3 class="tasks-error-header">
              🔴 Error
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedTaskStatus) && hasMoreStderr}
                <button
                  class="tasks-load-more-arrow"
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
                class="tasks-log-pre"
                on:scroll={() => {
                  if (!stderrPre) return;
                  // Threshold to consider if scrolled to bottom (e.g., 10px from bottom)
                  const threshold = 10;
                  const atBottom =
                    stderrPre.scrollHeight - stderrPre.scrollTop - stderrPre.clientHeight < threshold;
                  isScrolledToBottomErr = atBottom;
                }}
              >
                {#each logsToShowErr as log, i (log.logText + i)}
{log.logText}
                {/each}
              </pre>
          </div>
        {/if}
      </div>
      <button class="tasks-modal-close" on:click={closeLogModal}>Close</button>
    </div>
  </div>
{/if}