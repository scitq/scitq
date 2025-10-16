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
  // List of available workers
  let workers: Worker[] = [];
  // List of available workflows
  let workflows: Workflow[] = [];
  // All steps across workflows
  let allSteps: Step[] = [];

  // --- Filtering State ---
  // Currently selected worker filter
  let selectedWorkerId: number = undefined;
  // Currently selected workflow filter
  let selectedWfId: number = undefined;
  // Currently selected step filter
  let selectedStepId: number = undefined;
  // Currently selected command filter
  let selectedCommand: string = undefined;
  // Currently selected status filter
  let selectedStatus: string = '';
  // Status of the selected task (for log modal)
  let selectedTaskStatus: string = '';
  // Command of the selected task (for log modal)
  let selectedTaskCommand: String = undefined;
  // Current sorting method
  let sortBy: 'task' | 'worker' | 'wf' | 'step' = 'task';
  // Steps for the currently selected workflow
  let workflowSteps: Step[] = [];

  // --- Log Management ---
  // Number of log lines to load at once
  const CHUNK_SIZE = 50;
  // Whether more stdout logs are available to load
  let hasMoreStdout = true;
  // Whether more stderr logs are available to load
  let hasMoreStderr = true;
  
  // Tracks how many logs have been skipped for each task
  type LogSkip = {
    stdout: number;
    stderr: number;
  };
  let logSkipTracker: Record<number, LogSkip> = {};

  // Stores stdout logs by task ID
  let taskLogsOut: Record<number, TaskLog[]> = {};
  // Stores stderr logs by task ID
  let taskLogsErr: Record<number, TaskLog[]> = {};
  // Stores saved log chunks
  let taskLogsSaved: LogChunk[] = [];
  // Tracks which tasks are currently being streamed
  const streamedTasks = new Set<number>();

  // --- UI State ---
  // Currently selected task ID (for log modal)
  let selectedTaskId: number | null = null;
  // Whether log modal is visible
  let showLogModal = false;
  // Logs to display in stdout panel
  let logsToShowOut: TaskLog[] = [];
  // Logs to display in stderr panel
  let logsToShowErr: TaskLog[] = [];
  // Reference to stdout pre element
  let stdoutPre: HTMLPreElement | null = null;
  // Reference to stderr pre element
  let stderrPre: HTMLPreElement | null = null;
  // Whether stdout panel is scrolled to bottom
  let isScrolledToBottomOut = true;
  // Whether stderr panel is scrolled to bottom
  let isScrolledToBottomErr = true;

  // --- Task List Pagination ---
  // Number of tasks to load at once
  const TASKS_CHUNK_SIZE = 25;
  // Currently displayed tasks
  let displayedTasks: Task[] = [];
  // First loaded tasks (used for resetting search)
  let firstTasks: Task[] = [];
  // New tasks waiting to be displayed
  let pendingTasks: Task[] = [];
  // Whether more tasks are available to load
  let hasMoreTasks = true;
  // Whether tasks are currently loading
  let isLoading = false;
  // Reference to tasks container element
  let tasksContainer: HTMLDivElement;
  // Whether list is scrolled to top
  let isScrolledToTop = false;
  // Whether list is scrolled to bottom
  let isScrolledToBottom = false;
  // Last scroll position (for maintaining position during updates)
  let lastScrollPosition = 0;
  // Whether to show new tasks notification
  let showNewTasksNotification = false;
  // Function to unsubscribe from WebSocket
  let unsubscribeWS: (() => void) | null = null;

// Handles incoming WebSocket messages for task updates (topic-aware and legacy)
async function handleWebSocketMessage(message) {
  // New envelope: task entity events
  if (message?.type === 'task') {
    const action = message.action;
    const p = message.payload || {};

    if (action === 'created') {
      const newTask = {
        taskId: p.taskId,
        command: p.command || '',
        status: p.status || 'P',
        stepId: p.stepId ?? null,
        workerId: p.workerId ?? null,
        workflowId: p.workflowId ?? null,
        taskName: p.taskName ?? null,
      };

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

      if (['R', 'U', 'V'].includes(newTask.status)) {
        startStreaming(newTask);
      }
      return;
    }

    if (action === 'updated') {
      const updatedTaskId = p.taskId;
      const newStatus = p.status;
      const workerId = p.workerId;

      const updateTaskInArray = (tasks: Task[]) =>
        tasks.map(task => {
          if (task.taskId === updatedTaskId) {
            const updatedTask = {
              ...task,
              status: newStatus ?? task.status,
            };
            if (workerId !== undefined) {
              updatedTask.workerId = workerId;
              if (workers.length > 0) {
                const worker = workers.find(w => w.workerId === workerId);
                if (worker) updatedTask.workerName = worker.name;
              }
            }
            return updatedTask;
          }
          return task;
        });

      displayedTasks = updateTaskInArray(displayedTasks);
      firstTasks = updateTaskInArray(firstTasks);
      pendingTasks = updateTaskInArray(pendingTasks);

      if (['R', 'U', 'V'].includes(newStatus)) {
        let task = displayedTasks.find(t => t.taskId === updatedTaskId) || pendingTasks.find(t => t.taskId === updatedTaskId);
        if (task && !streamedTasks.has(task.taskId)) {
          startStreaming(task);
        }
      }
      return;
    }

    if (action === 'deleted') {
      const id = p.taskId ?? message.id;
      if (typeof id === 'number') {
        displayedTasks = displayedTasks.filter(t => t.taskId !== id);
        firstTasks = firstTasks.filter(t => t.taskId !== id);
        pendingTasks = pendingTasks.filter(t => t.taskId !== id);
        delete taskLogsOut[id];
        delete taskLogsErr[id];
        delete logSkipTracker[id];
        streamedTasks.delete(id);
      }
      return;
    }
  }
}

  // --- Core Functions ---

  /**
   * Loads initial batch of tasks with current filters
   * @async
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
   * @returns {Object} Filter parameters
   */
  function getFiltersFromUrl() {
    const hash = window.location.hash;
    const queryPart = hash.includes('?') ? hash.split('?')[1] : '';
    const params = new URLSearchParams(queryPart);

    /**
     * Safely parses number from string
     * @param {string|null} value - Value to parse
     * @returns {number|undefined} Parsed number or undefined
     */
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
   */
  async function updateTasksFromUrl() {
    const filters = getFiltersFromUrl();

    selectedWorkerId = filters.workerId ?? undefined;
    selectedWfId = filters.workflowId ?? undefined;
    selectedStepId = filters.stepId ?? undefined;
    selectedStatus = filters.status ?? undefined;

    // Load steps if workflow is selected
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

        // Load logs for finished tasks
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
   * Loads newly arrived pending tasks
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

    // Add pending tasks to display
    displayedTasks = [...pendingTasks, ...displayedTasks];
    firstTasks = [...pendingTasks, ...firstTasks];
    pendingTasks = [];
    showNewTasksNotification = false;
    
    // Scroll to top
    if (tasksContainer) {
      tasksContainer.scrollTo({
        top: 0,
        behavior: 'smooth'
      });
    }
  }

  /**
   * Loads more tasks for infinite scroll
   * @async
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
            
            // Maintain scroll position
            if (tasksContainer && !isScrolledToTop) {
                tasksContainer.scrollTop = lastScrollPosition;
            }

            // Load logs for finished tasks
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
   * @param {string} [status] - Optional status filter
   */
  function handleStatusClick(status?: string) {
    const query = new URLSearchParams();

    if (status) query.set('status', status);
    if (selectedWorkerId !== undefined && selectedWorkerId !== '') query.set('workerId', String(selectedWorkerId));
    if (selectedWfId !== undefined && selectedWfId !== '') query.set('workflowId', String(selectedWfId));
    if (selectedStepId !== undefined && selectedStepId !== '') query.set('stepId', String(selectedStepId));
    if (selectedCommand) {
        query.set('command', encodeURIComponent(selectedCommand));
    }

    window.location.hash = `/tasks?${query.toString()}`;
  }

  /**
   * Handles workflow selection change
   * @async
   */
  async function handleWfClick() {
    selectedWfId = selectedWfId !== '' ? Number(selectedWfId) : '';
    workflowSteps = selectedWfId !== '' ? allSteps.filter(s => s.workflowId === selectedWfId) : [];
    selectedStepId = '';
    handleStatusClick();
  }

  /**
   * Sorts tasks based on current sort criteria
   * @returns {Task[]} Sorted tasks
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

    // Initialize log storage
    taskLogsOut[task.taskId] = taskLogsOut[task.taskId] ?? [];
    taskLogsErr[task.taskId] = taskLogsErr[task.taskId] ?? [];

    /**
     * Handles incoming log entries
     * @param {TaskLog} log - Log entry
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

      // Update saved logs
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

    // Start streaming both stdout and stderr
    streamTaskLogsOutput(task.taskId, handleLog);
    streamTaskLogsErr(task.taskId, handleLog);
  }

  /**
   * Loads older logs for a task
   * @async
   * @param {number} taskId - Task ID
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

    // Prepend new logs and update skip count
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
   * @param {'stdout'|'stderr'} logType - Which panel to scroll
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
  /**
   * Component mount lifecycle hook
   * @async
   */
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

      // Load logs for finished tasks
      const finishedTaskIds = displayedTasks
        .filter(task => !['R', 'U', 'V'].includes(task.status))
        .map(task => task.taskId);

      if (finishedTaskIds.length > 0) {
        taskLogsSaved = await getLogsBatch(finishedTaskIds, 2);
      }

      // Subscribe to task events only (topic-aware)
      unsubscribeWS = wsClient.subscribeWithTopics({ task: [] }, handleWebSocketMessage);

      // URL change listener
      window.addEventListener('hashchange', updateTasksFromUrl);

      // Initial scroll position check
      if (tasksContainer) {
        handleScroll();
      }
    } catch (error) {
      console.error("Initialization error:", error);
    }

    // Cleanup function
    return () => {
      if (unsubscribeWS) {
        unsubscribeWS();
        unsubscribeWS = null;
      }
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

    // Detect scroll position
    isScrolledToTop = scrollTop <= 10;

    // Load new tasks if scrolled to top
    if (isScrolledToTop && pendingTasks.length > 0) {
      loadNewTasks();
    }

    // Load more tasks if near bottom
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

<!-- Main Tasks Container -->
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

  <!-- Filters Section -->
  <form class="tasks-filters-form" on:submit|preventDefault={() => handleStatusClick()}>

    <!-- Command Search -->
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

    <!-- Sort By Dropdown -->
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

    <!-- Worker Filter -->
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

    <!-- Workflow Filter -->
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

    <!-- Step Filter (shown when workflow selected) -->
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

  <!-- Status Filter Tabs -->
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

  <!-- Task List Container -->
  <div class="tasks-list-container" data-testid="tasks-list" bind:this={tasksContainer} on:scroll={handleScroll}>

      <!-- Task List Component -->
      <TaskList 
        displayedTasks={displayedTasks}
        {taskLogsSaved}
        {workers}
        {workflows}
        {allSteps}
        onOpenModal={openLogModal}
      />

      <!-- Loading and Empty States -->
      {#if isLoading}
        <div class="loading-indicator">Loading...</div>
      {:else if !hasMoreTasks}
        <p class="workerCompo-empty-state">No more task.</p>
      {/if}

  </div>

</div>

<!-- Log Modal -->
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
      <div class="task-attributes" style="display:flex; flex-direction:column; margin-bottom:1em; gap:0.25em;">
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.container}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Container:</span>
            <code>{displayedTasks.find(t => t.taskId === selectedTaskId).container}</code>
          </div>
        {/if}
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.containerOptions}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Container options:</span>
            <code>{displayedTasks.find(t => t.taskId === selectedTaskId).containerOptions}</code>
          </div>
        {/if}
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.shell}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Shell:</span>
            <code>{displayedTasks.find(t => t.taskId === selectedTaskId).shell}</code>
          </div>
        {/if}
      </div>
      <p class="tasks-command-preview"> {selectedTaskCommand}</p>
      <div class="tasks-log-columns">
        <!-- Stdout Log Panel -->
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

        <!-- Stderr Log Panel -->
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