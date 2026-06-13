<script lang="ts">
  import { run, preventDefault, createBubbler, stopPropagation } from 'svelte/legacy';

  const bubble = createBubbler();
  import { onMount, onDestroy, tick } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import {
    getAllTasks,
    getWorkFlow,
    getWorkers,
    getSteps,
    streamTaskLogsOutput,
    streamTaskLogsErr,
    getLogsBatch,
    editAndRetryTask,
    editTaskCommand,
    signalTask
  } from '../lib/api';
  import TaskList from '../components/TaskList.svelte';
  import { LogChunk, TaskLog } from '../../gen/taskqueue';
  import '../styles/tasks.css';
  import { ArrowBigUp, Search, X} from 'lucide-svelte';

  // --- State Management ---
  // List of available workers
  let workers: Worker[] = $state([]);
  // List of available workflows
  let workflows: Workflow[] = $state([]);
  // All steps across workflows
  let allSteps: Step[] = $state([]);

  // --- Filtering State ---
  // Currently selected worker filter
  let selectedWorkerId: number = $state(undefined);
  // Currently selected workflow filter
  let selectedWfId: number = $state(undefined);
  // Currently selected step filter
  let selectedStepId: number = $state(undefined);
  // Currently selected command filter
  let selectedCommand: string = $state(undefined);
  // Currently selected status filter
  let selectedStatus: string = '';
  // Show hidden tasks toggle
  let showHidden: boolean = $state(false);
  // Status of the selected task (for log modal)
  let selectedTaskStatus: string = $state('');
  // Command of the selected task (for log modal)
  let selectedTaskCommand: String = $state(undefined);
  let editMode = $state(false);
  let editCommandText = $state('');
  let editError = $state('');
  // Current sorting method
  let sortBy: 'task' | 'worker' | 'wf' | 'step' = $state('task');
  // Steps for the currently selected workflow
  let workflowSteps: Step[] = $state([]);

  // --- Log Management ---
  // Number of log lines to load at once
  const CHUNK_SIZE = 50;
  // Whether more stdout logs are available to load
  let hasMoreStdout = $state(true);
  // Whether more stderr logs are available to load
  let hasMoreStderr = $state(true);
  
  // Tracks how many logs have been skipped for each task
  type LogSkip = {
    stdout: number;
    stderr: number;
  };
  let logSkipTracker: Record<number, LogSkip> = {};

  // Stores stdout logs by task ID
  let taskLogsOut: Record<number, TaskLog[]> = $state({});
  // Stores stderr logs by task ID
  let taskLogsErr: Record<number, TaskLog[]> = $state({});
  // Stores saved log chunks
  let taskLogsSaved: LogChunk[] = $state([]);
  // Tracks which tasks are currently being streamed
  const streamedTasks = new Set<number>();

  // --- UI State ---
  // Currently selected task ID (for log modal)
  let selectedTaskId: number | null = $state(null);
  // Whether log modal is visible
  let showLogModal = $state(false);
  // Logs to display in stdout panel
  let logsToShowOut: TaskLog[] = $state([]);
  // Logs to display in stderr panel
  let logsToShowErr: TaskLog[] = $state([]);
  // Reference to stdout pre element
  let stdoutPre: HTMLPreElement | null = $state(null);
  // Reference to stderr pre element
  let stderrPre: HTMLPreElement | null = $state(null);
  // Whether stdout panel is scrolled to bottom
  let isScrolledToBottomOut = $state(true);
  // Whether stderr panel is scrolled to bottom
  let isScrolledToBottomErr = $state(true);

  // --- Task List Pagination ---
  // Number of tasks to load at once
  const TASKS_CHUNK_SIZE = 25;
  // Currently displayed tasks
  let displayedTasks: Task[] = $state([]);
  // First loaded tasks (used for resetting search)
  let firstTasks: Task[] = [];
  // New tasks waiting to be displayed
  let pendingTasks: Task[] = $state([]);
  // Whether more tasks are available to load
  let hasMoreTasks = $state(true);
  // Whether tasks are currently loading
  let isLoading = $state(false);
  // Reference to tasks container element
  let tasksContainer: HTMLDivElement = $state();
  // Whether list is scrolled to top
  let isScrolledToTop = false;
  // Whether list is scrolled to bottom
  let isScrolledToBottom = false;
  // Last scroll position (for maintaining position during updates)
  let lastScrollPosition = 0;
  // Whether to show new tasks notification
  let showNewTasksNotification = $state(false);
  // Function to unsubscribe from WebSocket
  let unsubscribeWS: (() => void) | null = null;

// Handles incoming WebSocket messages for task updates (topic-aware and legacy)
async function handleWebSocketMessage(message) {
  // New envelope: task entity events
  if (message?.type === 'task') {
    const action = message.action;
    const p = message.payload || {};

    // Helper to check if a task passes current filters
    function passesFilters(task) {
      if (selectedWorkerId && task.workerId !== selectedWorkerId) return false;
      if (selectedWfId && task.workflowId !== selectedWfId) return false;
      if (selectedStepId && task.stepId !== selectedStepId) return false;
      if (selectedStatus && task.status !== selectedStatus) return false;
      return true;
    }

    // --- 1️⃣ Task created ---
    if (action === 'created') {
      if (!passesFilters(p)) return;

      const exists = displayedTasks.some(t => t.taskId === p.taskId);
      if (!exists) {
        displayedTasks = [p, ...displayedTasks];
        if (['R', 'U', 'V'].includes(p.status)) startStreaming(p);
      }
      return;
    }

    // --- 2️⃣ Task deleted ---
    if (action === 'deleted') {
      displayedTasks = displayedTasks.filter(t => t.taskId !== p.taskId);
      stopStreaming(p.taskId);
      return;
    }

    // --- 3️⃣ Task edited (EditTask / EditAndRetryTask) ---
    // Payload carries only the fields that were actually updated.
    // Overlay them onto the local row so reopening the modal shows the
    // fresh command instead of the pre-edit cache.
    if (action === 'edited') {
      const idx = displayedTasks.findIndex(t => t.taskId === p.taskId);
      if (idx !== -1) {
        const current = displayedTasks[idx];
        const patch = {};
        for (const k of ['command', 'container', 'containerOptions', 'shell',
                         'status', 'output', 'publish', 'retry',
                         'input', 'resource']) {
          if (p[k] !== undefined) patch[k] = p[k];
        }
        displayedTasks[idx] = { ...current, ...patch };
        displayedTasks = [...displayedTasks];
      }
      return;
    }

    // --- 4️⃣ Task status ---
    if (action === 'status') {
      if (p.oldStatus === 'F' && p.status === 'P' && p.parentTaskId) {
        // Retry case
        const parentIdx = displayedTasks.findIndex(t => t.taskId === p.parentTaskId);
        if (parentIdx !== -1) {
          const parent = displayedTasks[parentIdx];
          const merged = {
            ...p,
            taskName: parent.taskName,
            command: parent.command,
            output: parent.output,
            shell: parent.shell,
            container: parent.container,
            containerOptions: parent.containerOptions,
            input: parent.input,
            resource: parent.resource,
            workflowId: parent.workflowId,
            stepId: parent.stepId,
            workerId: undefined,
          };
          displayedTasks.splice(parentIdx, 1);
          displayedTasks = [merged, ...displayedTasks];
          startStreaming(merged);
        }
        return;
      }

      // Regular status change (only mutate status/workerId; do not overwrite other fields)
      const id = (p && typeof p.taskId !== 'undefined') ? p.taskId : message?.id;
      const idx = displayedTasks.findIndex(t => t.taskId === id);
      if (idx !== -1) {
        const current = displayedTasks[idx];
        const next = {
          ...current,
          status: typeof p.status === 'string' ? p.status : current.status,
          workerId: (typeof p.workerId === 'number') ? p.workerId : current.workerId,
        };
        displayedTasks[idx] = next;
        displayedTasks = [...displayedTasks];
      } else {
        // If the task isn't currently listed (due to filters/pagination), ignore status-only events
        // to avoid creating partial/incorrect rows. It will appear via a "created" event or next refresh.
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
      0,
      showHidden
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
      showHidden: params.get('showHidden') === 'true',
      command: params.get('command') ? decodeURIComponent(params.get('command')) : undefined
    };
  }

  /**
   * Synchronously syncs the JS filter state with the URL hash. Kept separate
   * from updateTasksFromUrl so we can call it before any awaits in onMount —
   * otherwise a user clicking a status tab while workers/workflows are still
   * loading would rebuild the hash from undefined state and silently drop
   * filters like workflowId.
   */
  function syncFiltersFromUrl() {
    const filters = getFiltersFromUrl();
    selectedWorkerId = filters.workerId ?? undefined;
    selectedWfId = filters.workflowId ?? undefined;
    selectedStepId = filters.stepId ?? undefined;
    selectedStatus = filters.status ?? undefined;
    showHidden = filters.showHidden ?? false;
  }

  /**
   * Updates task list based on URL filters
   * @async
   */
  async function updateTasksFromUrl() {
    syncFiltersFromUrl();

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
        0,
        showHidden
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
            0,
            showHidden
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
            displayedTasks.length,
            showHidden
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
   * Stops streaming logs for a given task
   * @param {number} taskId - Task ID
   */
  function stopStreaming(taskId: number) {
    streamedTasks.delete(taskId);
    delete taskLogsOut[taskId];
    delete taskLogsErr[taskId];
    delete logSkipTracker[taskId];
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
    editMode = false;
    editCommandText = '';
    editError = '';
    hasMoreStdout = true;
    hasMoreStderr = true;
  }

  function startEditMode() {
    editMode = true;
    editCommandText = String(selectedTaskCommand);
    editError = '';
  }

  async function submitEditAndRetry() {
    if (!selectedTaskId) return;
    editError = '';
    try {
      // Terminal states (F/S): clone-and-retry flow — creates a fresh
      // task_id with the new command.
      // Not-yet-assigned (P/W): the worker hasn't picked the task up.
      // A plain in-place edit is enough — when PingAndTakeNewTasks
      // eventually delivers the task, the worker reads the fresh
      // command directly from that response. Server-side SignalTask
      // rejects these statuses (its WHERE clause is R/D/O/C only), so
      // sending B here would 400.
      // In flight (C/D/O): worker has the task with the stale command
      // cached locally. Edit updates the DB; signal B tells the worker
      // to re-read at its launch checkpoint before exec.
      // Running (R): UI gate hides the button — see the {#if} below.
      // editAndRetryTask creates a NEW task_id, so the local list will
      // be refreshed when the WS event arrives. For the in-place edit
      // paths (P/W and C/D/O) there is no such WS event today — the
      // server stores the new command in DB but doesn't broadcast it
      // — so reopening the modal would otherwise show the stale
      // cached command. Patch the local row optimistically: the DB
      // is the authority, our edit succeeded, mirror it.
      const inPlace = !(selectedTaskStatus === 'F' || selectedTaskStatus === 'S');
      if (selectedTaskStatus === 'F' || selectedTaskStatus === 'S') {
        await editAndRetryTask(selectedTaskId, editCommandText);
      } else if (selectedTaskStatus === 'P' || selectedTaskStatus === 'W') {
        await editTaskCommand(selectedTaskId, editCommandText);
      } else {
        // C / D / O
        await editTaskCommand(selectedTaskId, editCommandText);
        await signalTask(selectedTaskId, 'B');
      }
      if (inPlace) {
        const idx = displayedTasks.findIndex(t => t.taskId === selectedTaskId);
        if (idx !== -1) {
          displayedTasks[idx] = { ...displayedTasks[idx], command: editCommandText };
          displayedTasks = [...displayedTasks];
        }
      }
      editMode = false;
      closeLogModal();
    } catch (err) {
      editError = err.message || 'Failed to edit task';
    }
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
      // Pull filters from the URL *synchronously* so any button the user
      // clicks during the async data loads below already sees them. Without
      // this, handleStatusClick would rebuild the hash from undefined state
      // and drop e.g. workflowId — the bug where a quick click on a status
      // tab turns a filtered list into a global one.
      syncFiltersFromUrl();

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
  run(() => {
    if (selectedTaskId !== null) {
      logsToShowOut = taskLogsOut[selectedTaskId] ?? [];
      logsToShowErr = taskLogsErr[selectedTaskId] ?? [];
    }
  });

  // Auto-scroll stdout when new logs arrive
  run(() => {
    if (logsToShowOut.length > 0) {
      tick().then(() => {
        setTimeout(() => {
          if (stdoutPre && isScrolledToBottomOut) stdoutPre.scrollTop = stdoutPre.scrollHeight;
        }, 0);
      });
    }
  });

  // Auto-scroll stderr when new logs arrive
  run(() => {
    if (logsToShowErr.length > 0) {
      tick().then(() => {
        setTimeout(() => {
          if (stderrPre && isScrolledToBottomErr) stderrPre.scrollTop = stderrPre.scrollHeight;
        }, 0);
      });
    }
  });
</script>

<!-- Main Tasks Container -->
<div class="tasks-container" data-testid="tasks-page">
  {#if showNewTasksNotification}
    <div 
      class="new-tasks-notification"
      data-testid={`tasks-notification-${pendingTasks.length}`}
      onclick={loadNewTasks}
      onkeydown={e => e.key === 'Enter' && loadNewTasks()}
      tabindex="0"
      role="button"
      aria-label={`Show ${pendingTasks.length} new task${pendingTasks.length > 1 ? 's' : ''}`}
    >
    {pendingTasks.length} new task{pendingTasks.length > 1 ? 's' : ''} available
      <button class="show-new-btn" onclick={loadNewTasks}>Show</button>
    </div>
  {/if}

  <!-- Filters Section -->
  <form class="tasks-filters-form" onsubmit={preventDefault(() => handleStatusClick())}>

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
            onkeydown={(e) => e.key === 'Enter' && handleStatusClick()}
          />
        <div class="search-icons">
          {#if selectedCommand}
            <button 
              type="button" 
              onclick={() => {
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
            onclick={() => handleStatusClick()}
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
      <select id="sortBy" bind:value={sortBy} onchange={() => {
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
        onchange={() => {
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
        onchange={() => {
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
          onchange={() => {
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
    <button class="tasks-status-all" onclick={() => handleStatusClick()}>All</button>
    <button class="tasks-status-pending" onclick={() => handleStatusClick('P')}>Pending</button>
    <button class="tasks-status-assigned" onclick={() => handleStatusClick('A')}>Assigned</button>
    <button class="tasks-status-accepted" onclick={() => handleStatusClick('C')}>Accepted</button>
    <button class="tasks-status-downloading" onclick={() => handleStatusClick('D')}>Downloading</button>
    <button class="tasks-status-on-hold" onclick={() => handleStatusClick('O')}>On-hold</button>
    <button class="tasks-status-running" onclick={() => handleStatusClick('R')}>Running</button>
    <button class="tasks-status-uploading-as" onclick={() => handleStatusClick('U')}>Uploading (AS)</button>
    <button class="tasks-status-succeeded" onclick={() => handleStatusClick('S')}>Succeeded</button>
    <button class="tasks-status-uploading-af" onclick={() => handleStatusClick('V')}>Uploading (AF)</button>
    <button class="tasks-status-failed" onclick={() => handleStatusClick('F')}>Failed</button>
    <button class="tasks-status-waiting" onclick={() => handleStatusClick('W')}>Waiting</button>
    <button class="tasks-status-suspended" onclick={() => handleStatusClick('Z')}>Suspended</button>
    <button class="tasks-status-canceled" onclick={() => handleStatusClick('X')}>Canceled</button>
    <label style="margin-left:1em;display:flex;align-items:center;gap:0.3em;font-size:0.85em;color:gray;cursor:pointer;">
      <input type="checkbox" bind:checked={showHidden} onchange={loadInitialTasks} />
      Show hidden
    </label>
  </div>

  <!-- Task List Container -->
  <div class="tasks-list-container" data-testid="tasks-list" bind:this={tasksContainer} onscroll={handleScroll}>

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
    onclick={closeLogModal}
    onkeydown={(e) => {
      // Only react to keys that landed on the backdrop itself —
      // otherwise Enter/Space typed inside the Edit & Retry textarea
      // bubbles up here and closes the modal mid-edit. Escape still
      // closes from anywhere (the inner modal also handles it).
      if (e.target !== e.currentTarget) return;
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'Escape') closeLogModal();
    }}
  >
    <div
      class="tasks-modal terminal-style"
      role="dialog"
      aria-modal="true"
      tabindex="0"
      aria-labelledby="modal-title"
      onclick={stopPropagation(bubble('click'))}
      onkeydown={(e) => {
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
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.input?.length}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Inputs:</span>
            <code style="font-size:0.85em;word-break:break-all;">{displayedTasks.find(t => t.taskId === selectedTaskId).input.join(', ')}</code>
          </div>
        {/if}
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.resource?.length}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Resources:</span>
            <code style="font-size:0.85em;word-break:break-all;">{displayedTasks.find(t => t.taskId === selectedTaskId).resource.join(', ')}</code>
          </div>
        {/if}
        {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.publish}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Publish:</span>
            <code style="font-size:0.85em;word-break:break-all;">{displayedTasks.find(t => t.taskId === selectedTaskId).publish}</code>
          </div>
          <!-- Output (workspace) shown only for failed tasks when publish exists —
               on failure, output goes to workspace not publish, useful for debugging -->
          {#if displayedTasks.find(t => t.taskId === selectedTaskId)?.status === 'F' && displayedTasks.find(t => t.taskId === selectedTaskId)?.output}
            <div>
              <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Output (workspace):</span>
              <code style="font-size:0.85em;word-break:break-all;">{displayedTasks.find(t => t.taskId === selectedTaskId).output}</code>
            </div>
          {/if}
        {:else if displayedTasks.find(t => t.taskId === selectedTaskId)?.output}
          <div>
            <span style="color:gray;font-size:0.9em;margin-right:0.25em;">Output:</span>
            <code style="font-size:0.85em;word-break:break-all;">{displayedTasks.find(t => t.taskId === selectedTaskId).output}</code>
          </div>
        {/if}
      </div>
      {#if editMode}
        <div style="margin:0.5em 0;">
          <textarea
            bind:value={editCommandText}
            style="width:100%;min-height:200px;font-family:monospace;font-size:0.85em;padding:0.5em;border:1px solid #666;border-radius:4px;background:#1a1a2e;color:#e0e0e0;"
          ></textarea>
          {#if editError}
            <p style="color:#ff6b6b;margin:0.25em 0;">{editError}</p>
          {/if}
        </div>
      {:else}
        <p class="tasks-command-preview"> {selectedTaskCommand}</p>
      {/if}
      <div class="tasks-log-columns">
        <!-- Stdout Log Panel -->
        {#if logsToShowOut.length > 0}
          <div class="tasks-log-block tasks-stdout-block">
            <h3 class="tasks-output-header">
              🟢 Stdout
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedTaskStatus) && hasMoreStdout}
                <button
                  class="tasks-load-more-arrow"
                  onclick={() => { loadMoreLogs(selectedTaskId, 'stdout'); scrollToTop('stdout');; }}
                  title="Load more Stdout"
                  data-testid={`load-more-stdout-${selectedTaskId}`}
                  aria-label="Load more logs"
                >
                  <ArrowBigUp />
                </button>
              {/if}
            </h3>
              <pre
                bind:this={stdoutPre}
                class="tasks-log-pre"
                onscroll={() => {
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
              🔴 Stderr
              {#if selectedTaskId !== null && !['R', 'U', 'V'].includes(selectedTaskStatus) && hasMoreStderr}
                <button
                  class="tasks-load-more-arrow"
                  onclick={() => { loadMoreLogs(selectedTaskId, 'stderr'); scrollToTop('stderr');; }}
                  title="Load more Stderr"
                  aria-label="Load more stderr logs"
                >
                  <ArrowBigUp />
                </button>
              {/if}
            </h3>
              <pre
                bind:this={stderrPre}
                class="tasks-log-pre"
                onscroll={() => {
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
      <div style="display:flex;gap:0.5em;justify-content:flex-end;margin-top:0.5em;">
        {#if editMode}
          <button class="tasks-modal-close" style="background:#4caf50;" onclick={submitEditAndRetry}>
            {selectedTaskStatus === 'F' || selectedTaskStatus === 'S' ? 'Save & Retry' : 'Save'}
          </button>
          <button class="tasks-modal-close" onclick={() => { editMode = false; editError = ''; }}>Cancel</button>
        {:else}
          <!-- Edit button on every non-terminal-running state:
               F/S → "Edit & Retry" creates a new task_id (clone-and-retry).
               P/W/C/D/O → "Edit" patches the existing task in place; the
                           submit handler also pings the worker (signal B)
                           so it re-reads at its launch checkpoint.
               R → no button; in-flight container can't be hot-swapped.
                   Operator kills first, then edits the resulting F. -->
          {#if selectedTaskStatus === 'F' || selectedTaskStatus === 'S'}
            <button class="tasks-modal-close" style="background:#ff9800;" onclick={startEditMode}>Edit & Retry</button>
          {:else if selectedTaskStatus === 'P' || selectedTaskStatus === 'W' || selectedTaskStatus === 'C' || selectedTaskStatus === 'D' || selectedTaskStatus === 'O'}
            <button class="tasks-modal-close" style="background:#ff9800;" onclick={startEditMode}>Edit</button>
          {/if}
          <button class="tasks-modal-close" onclick={closeLogModal}>Close</button>
        {/if}
      </div>
    </div>
  </div>
{/if}