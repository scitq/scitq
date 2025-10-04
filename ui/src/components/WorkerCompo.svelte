<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {getWorkerStatusClass,delWorker, getWorkerStatusText, getStats, formatBytesPair, getTasksCount, getStatus, updateWorkerStatus, getAllTaskStats} from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser, BarChart, FileDigit, ChevronDown, ChevronUp } from 'lucide-svelte';
  import LineChart from './LineChart.svelte';
  import '../styles/worker.css';
  import { WorkerStats } from '../../gen/taskqueue';



  /**
   * Array of worker objects to display
   * @type {Array<Object>}
   */
  export let workers = [];
  
  /**
   * Callback function when worker is updated
   * @type {(event: { detail: { workerId: number; updates: Partial<Worker> } }) => void}
   */
  export let onWorkerUpdated: (event: { detail: { workerId: number; updates: Partial<Worker> } }) => void = () => {};
    
  // State management
  /**
   * Map of worker statuses by worker ID
   * @type {Map<number, string>}
   */
  let statusMap = new Map<number, string>();
  /**
   * Processed workers data for display
   * @type {Array<Object>}
   */
  let displayWorkers = [];
  
  /**
   * Map of worker statistics by worker ID
   * @type {Record<number, taskqueue.WorkerStats>}
   */
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};
  
  /**
   * Count of tasks by worker ID and status
   * @type {Record<number, Record<string, number>>}
   */
  let tasksCount: Record<number, Record<string, number>> = {};
  
  /**
   * Count of all tasks by status
   * @type {Record<string, number>}
   */
  let allCount: Record<string, number> = {};
  /**
   * Total number of tasks
   * @type {number}
   */
  let totalCount = 0;
  
  // Set of worker IDs currently being acted upon (pause/delete)
  let acting = new Set<number>();
  
  /**
   * Current display mode ('table' or 'charts')
   * @type {'table' | 'charts'}
   */
  let displayMode: 'table' | 'charts' = 'table';
  
  /**
   * Flag indicating if data has been loaded
   * @type {boolean}
   */
  let hasLoaded = false;
  
  
  /**
   * Flag to show/hide advanced metrics
   * @type {boolean}
   */
  let showAdvancedMetrics = false;

  // Chart data
  /**
   * Maximum number of data points to keep in history
   * @type {number}
   */
  const MAX_HISTORY = 30;
  
  /**
   * History of disk I/O metrics
   * @type {Array<{time: Date, read: number, write: number}>}
   */
  let diskHistory: {time: Date, read: number, write: number}[] = [];
  
  /**
   * History of network I/O metrics
   * @type {Array<{time: Date, sent: number, received: number}>}
   */
  let networkHistory: {time: Date, sent: number, received: number}[] = [];
  
  /**
   * Data for disk chart visualization
   * @type {{line1: Array, line2: Array}}
   */
  let diskChartData = { line1: [], line2: [] };
  
  /**
   * Data for network chart visualization
   * @type {{line1: Array, line2: Array}}
   */
  let networkChartData = { line1: [], line2: [] };
  
  // Zoom controls
  /**
   * Current zoom level for disk chart
   * @type {number}
   */
  let diskZoom = 1;
  
  /**
   * Current zoom level for network chart
   * @type {number}
   */
  let networkZoom = 1;
  
  /**
   * Flag for auto-zoom on disk chart
   * @type {boolean}
   */
  let diskAutoZoom = true;
  
  /**
   * Flag for auto-zoom on network chart
   * @type {boolean}
   */
  let networkAutoZoom = true;

  /**
   * Configuration for zoom behavior
   * @type {Object}
   * @property {number} minZoom - Minimum zoom level
   * @property {number} maxZoom - Maximum zoom level
   * @property {number} sensitivity - Zoom sensitivity factor
   * @property {number} margin - Margin to prevent lines from touching
   */
  const ZOOM_CONFIG = {
    minZoom: 1,         // Minimum zoom level
    maxZoom: 15,        // Maximum zoom level
    sensitivity: 1.2,   // Zoom sensitivity
    margin: 0.1         // Margin around data
  };

  // --- WS unsubscribe reference for cleanup ---
  let unsubscribeWS: (() => void) | null = null;

  // Make reactive
  $: diskZoom, networkZoom, diskAutoZoom, networkAutoZoom;

  /**
   * Component mount lifecycle hook
   * Sets up WS event listeners (no periodic updateWorkerData).
   */
  onMount(() => {
    // On mount, load all task stats
    getAllTaskStats().then(({ perWorkerStatusCounts, globalStatusCounts, totalCount: t }) => {
      allCount = globalStatusCounts;
      tasksCount = perWorkerStatusCounts;
      totalCount = t;
    });

    // Validated, type-safe WS message handler
    const handleMessage = (msg: unknown) => {
      if (!msg || typeof msg !== 'object') return;
      const { type, action, id, payload } = msg as WSMessage;

      switch (type) {
        case 'worker':
          switch (action) {
            case 'status': {
              const status = (payload as any).status;
              if (typeof status === 'string') {
                statusMap.set(id, status);
                statusMap = new Map(statusMap);
              }
              break;
            }
            case 'stats': {
              const raw = (payload as any).stats;
              if (raw) {
                workersStatsMap[id] = WorkerStats.fromJson(raw);
                workersStatsMap = { ...workersStatsMap };
                updateHistoryFromStats();
              }
              break;
            }
            case 'deleted': {
              statusMap.set(id, 'X');
              statusMap = new Map(statusMap);
              break;
            }
            case 'deletionScheduled': {
              statusMap.set(id, 'D');
              statusMap = new Map(statusMap);
              break;
            }
            case 'created':
            case 'updated': {
              // Optional: refresh if worker list is live
              // for now do nothing
              break;
            }
          }
          break;

        case 'task':
          switch (action) {
            case 'status': {
              const workerId = payload.workerId;
              const oldStatus = payload.oldStatus;
              const newStatus = payload.status;

              // Update global allCount
              if (oldStatus) {
                allCount[oldStatus] = (allCount[oldStatus] || 0) - 1;
                if (allCount[oldStatus] < 0) {
                  allCount[oldStatus] = 0;
                  console.log(`Task count underflow for status ${oldStatus}: `,payload);
                }
              }
              allCount[newStatus] = (allCount[newStatus] || 0) + 1;
              allCount = { ...allCount };

              // Update per-worker tasksCount
              if (workerId !== undefined) {
                if (!tasksCount[workerId]) tasksCount[workerId] = {};
                if (oldStatus) {
                  tasksCount[workerId][oldStatus] = (tasksCount[workerId][oldStatus] || 0) - 1;
                  if (tasksCount[workerId][oldStatus] < 0) {
                    tasksCount[workerId][oldStatus] = 0;
                    console.log(`Task count underflow for worker ${workerId} [${oldStatus}]: `,payload);
                  }
                }
                if (newStatus) {
                  tasksCount[workerId][newStatus] = (tasksCount[workerId][newStatus] || 0) + 1;
                }
                tasksCount = { ...tasksCount };
              }
              break;
            }
            case 'created': {
              // Optionally update allCount for new task
              if (allCount) {
                allCount[payload.status] = (allCount[payload.status] || 0) + 1;
                allCount = { ...allCount };
              }
              totalCount++;
              break;
            }
            case 'updated':
              break;
            case 'deleted':
              allCount[payload.status]--;
              if (allCount[payload.status] < 0) allCount[payload.status] = 0;
              allCount = { ...allCount };

              if (tasksCount[payload.workerId]) {
                tasksCount[payload.workerId][payload.status]--;
                if (tasksCount[payload.workerId][payload.status] < 0)
                  tasksCount[payload.workerId][payload.status] = 0;
                tasksCount = { ...tasksCount };
              }

              totalCount--;
              break;
          }
          break;
      }
    };

    unsubscribeWS = wsClient.subscribeWithTopics({ worker: [], task: [] }, handleMessage);

    return () => {
      if (unsubscribeWS) unsubscribeWS();
      unsubscribeWS = null;
    };
  });

  // Reactive statements
  $: if (workers.length > 0 && !hasLoaded) {
    updateWorkerData();
    hasLoaded = true;
  }

  $: displayWorkers = workers.map(worker => ({
    ...worker,
    status: statusMap.get(worker.workerId) ?? worker.status
  }));

  $: diskChartData = prepareChartData(diskHistory, 'disk');
  $: networkChartData = prepareChartData(networkHistory, 'network');

  /**
   * Formats bytes into human-readable string
   * @param {number|bigint} bytes - The number of bytes to format
   * @returns {string} Formatted string with appropriate unit
   */
  function formatBytes(bytes: number | bigint): string {
    if (bytes === 0) return '0 B';
    const bytesNum = typeof bytes === 'bigint' ? Number(bytes) : bytes;
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytesNum) / Math.log(k));
    return parseFloat((bytesNum / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  /**
   * Calculates appropriate zoom level for automatic zoom
   * @param {Array} history - Data history array
   * @param {'disk'|'network'} type - Type of data being zoomed
   * @returns {number} Calculated zoom factor
   */
  function calculateAutoZoom(history: any[], type: 'disk' | 'network') {
    if (history.length < 2) return 1;

    const recentData = history.slice(-5); // Use recent points for stability
    const values = recentData.flatMap(d => 
      type === 'disk' ? [d.read || 0, d.write || 0] : [d.sent || 0, d.received || 0]
    ).filter(v => v > 0);

    if (values.length === 0) return 1;

    const max = Math.max(...values);
    const min = Math.min(...values);
    const range = max - min;
    const avg = values.reduce((a, b) => a + b, 0) / values.length;

    let zoomFactor = 1;
    
    if (range > 0) {
      const ratio = avg / range;
      zoomFactor = Math.min(
        ZOOM_CONFIG.maxZoom,
        Math.max(
          ZOOM_CONFIG.minZoom,
          ratio * ZOOM_CONFIG.sensitivity * 10
        )
      );
    }

    return zoomFactor;
  }

  /**
   * Prepares chart data for rendering
   * @param {Array} history - Data history array
   * @param {'disk'|'network'} type - Type of chart data
   * @returns {Object} Prepared chart data including lines and zoom info
   */
  function prepareChartData(history, type: 'disk' | 'network') {
    const isAutoZoom = type === 'disk' ? diskAutoZoom : networkAutoZoom;
    let currentZoom = type === 'disk' ? diskZoom : networkZoom;

    if (isAutoZoom) {
      currentZoom = calculateAutoZoom(history, type);
      if (type === 'disk') {
        diskZoom = currentZoom;
      } else {
        networkZoom = currentZoom;
      }
    }

    const dataValues = history.flatMap(d => 
      type === 'disk' ? [d.read || 0, d.write || 0] : [d.sent || 0, d.received || 0]
    );
    const dataMax = Math.max(1, ...dataValues);

    // Reduce margin to 5% to maximize space usage
    const displayMax = dataMax * 1.05 / currentZoom;

    // Prepare chart lines
    const line1 = [];
    const line2 = [];
    const chartHeight = 80;
    const chartWidth = 300;

    for (let i = 0; i < history.length; i++) {
      const d = history[i];
      const x = (i / (MAX_HISTORY-1)) * chartWidth;
      
      const calculateY = (value) => {
        const scaledValue = (value / displayMax) * chartHeight;
        return chartHeight - Math.min(chartHeight, Math.max(0, scaledValue));
      };
      
      if (type === 'disk') {
        line1.push(`${x},${calculateY(d.read || 0)}`);
        line2.push(`${x},${calculateY(d.write || 0)}`);
      } else {
        line1.push(`${x},${calculateY(d.sent || 0)}`);
        line2.push(`${x},${calculateY(d.received || 0)}`);
      }
    }

    return { line1, line2, currentZoom, displayMax };
  }

  /**
   * Toggles auto-zoom for a chart type
   * @param {'disk'|'network'} type - Type of chart to toggle
   */
  function toggleAutoZoom(type: 'disk' | 'network') {
    if (type === 'disk') {
      diskAutoZoom = !diskAutoZoom;
      // Reset zoom when enabling auto
      if (diskAutoZoom) diskZoom = 1;
    } else {
      networkAutoZoom = !networkAutoZoom;
      // Reset zoom when enabling auto
      if (networkAutoZoom) networkZoom = 1;
    }
    
    // Force immediate recalculation
    if (type === 'disk') {
      diskChartData = prepareChartData(diskHistory, 'disk');
      diskChartData = {...diskChartData}; // Force update
    } else {
      networkChartData = prepareChartData(networkHistory, 'network');
      networkChartData = {...networkChartData}; // Force update
    }
  }
  
  /**
   * Handles manual zoom for disk chart
   * @param {'in'|'out'|'reset'} direction - Zoom direction
   */
  function handleDiskZoom(direction: 'in' | 'out' | 'reset') {
    diskAutoZoom = false;
    if (direction === 'in') {
      diskZoom = Math.min(10, diskZoom + 0.5);
    } else if (direction === 'out') {
      diskZoom = Math.max(0.1, diskZoom - 0.5);
    } else {
      diskZoom = 1;
    }
    diskChartData = prepareChartData(diskHistory, 'disk');
  }

  /**
   * Handles manual zoom for network chart
   * @param {'in'|'out'|'reset'} direction - Zoom direction
   */
  function handleNetworkZoom(direction: 'in' | 'out' | 'reset') {
    networkAutoZoom = false;
    if (direction === 'in') {
      networkZoom = Math.min(10, networkZoom + 0.5);
    } else if (direction === 'out') {
      networkZoom = Math.max(0.1, networkZoom - 0.5);
    } else {
      networkZoom = 1;
    }
    networkChartData = prepareChartData(networkHistory, 'network');
  }

  /**
   * Updates worker data by fetching latest stats
   * @async
   */
  async function updateWorkerData() {
    if (workers.length === 0) return;

    try {
      const workerIds = workers.map(w => w.workerId);
      workersStatsMap = await getStats(workerIds);

      // Update history data
      const now = new Date();
      let totalRead = 0, totalWrite = 0, totalSent = 0, totalReceived = 0;

      workerIds.forEach(id => {
        const stats = workersStatsMap[id];
        if (stats?.diskIo) {
          totalRead += stats.diskIo.readBytesRate || 0;
          totalWrite += stats.diskIo.writeBytesRate || 0;
        }
        if (stats?.netIo) {
          totalSent += stats.netIo.sentBytesRate || 0;
          totalReceived += stats.netIo.recvBytesRate || 0;
        }
      });

      // Always update history
      diskHistory = [...diskHistory.slice(-MAX_HISTORY + 1), {
        time: now,
        read: totalRead,
        write: totalWrite
      }];

      networkHistory = [...networkHistory.slice(-MAX_HISTORY + 1), {
        time: now,
        sent: totalSent,
        received: totalReceived
      }];

      // Force chart updates
      diskChartData = prepareChartData(diskHistory, 'disk');
      networkChartData = prepareChartData(networkHistory, 'network');

      // Update status
      statusMap = new Map((await getStatus(workerIds)).map(s => [s.workerId, s.status]));

      // Replace per-worker and global task counts using getAllTaskStats
      const { perWorkerStatusCounts, globalStatusCounts, totalCount: t } = await getAllTaskStats();
      allCount = globalStatusCounts;
      tasksCount = perWorkerStatusCounts;
      totalCount = t;

    } catch (err) {
      console.error('Error loading data:', err);
    }
  }

  /**
   * Updates diskHistory and networkHistory from current workersStatsMap.
   */
  function updateHistoryFromStats() {
    const now = new Date();
    let totalRead = 0, totalWrite = 0, totalSent = 0, totalReceived = 0;

    for (const id in workersStatsMap) {
      const stats = workersStatsMap[id];
      if (stats?.diskIo) {
        totalRead += stats.diskIo.readBytesRate || 0;
        totalWrite += stats.diskIo.writeBytesRate || 0;
      }
      if (stats?.netIo) {
        totalSent += stats.netIo.sentBytesRate || 0;
        totalReceived += stats.netIo.recvBytesRate || 0;
      }
    }

    diskHistory = [...diskHistory.slice(-MAX_HISTORY + 1), {
      time: now,
      read: totalRead,
      write: totalWrite
    }];

    networkHistory = [...networkHistory.slice(-MAX_HISTORY + 1), {
      time: now,
      sent: totalSent,
      received: totalReceived
    }];

    diskChartData = prepareChartData(diskHistory, 'disk');
    networkChartData = prepareChartData(networkHistory, 'network');
  }

  /**
   * Updates a worker value and triggers update event
   * @param {Object} worker - Worker object
   * @param {'concurrency'|'prefetch'} field - Field to update
   * @param {number} delta - Amount to change
   */
  async function updateValue(worker, field: 'concurrency' | 'prefetch', delta: number) {
    const newValue = Math.max(0, worker[field] + delta);
    if (newValue !== worker[field]) {
      worker[field] = newValue;
      onWorkerUpdated({ detail: { workerId: worker.workerId, updates: { [field]: newValue } } });
    }
  }

  /**
   * Helper to compute the next status when pressing pause button.
   */
  function computeNextStatusForPause(current?: string): string | null {
    switch (current) {
      case 'R':
        return 'P'; // pause running -> Paused
      case 'O':
      case 'I':
        return 'Q'; // offline/idle -> Queued pause (or quiet)
      case 'P':
        return 'R'; // resume
      case 'Q':
        return 'O'; // un-quiet back to Offline (or Idle)
      default:
        return null;
    }
  }

  /**
   * Pause/resume/quiet/unquiet a worker.
   * @param {Object} worker - Worker object
   * @async
   */
  async function pauseWorker(worker): Promise<void> {
    const curr = statusMap.get(worker.workerId) ?? worker.status;
    const next = computeNextStatusForPause(curr);
    if (!next) return;

    if (acting.has(worker.workerId)) return;
    acting.add(worker.workerId);

    // optimistic UI update
    const prev = curr;
    statusMap.set(worker.workerId, next);
    statusMap = new Map(statusMap);

    try {
      await updateWorkerStatus({ workerId: worker.workerId, status: next });
    } catch (err) {
      console.error('Failed to update worker status', worker.workerId, next, err);
      // revert on failure
      statusMap.set(worker.workerId, prev);
      statusMap = new Map(statusMap);
    } finally {
      acting.delete(worker.workerId);
    }
  }

  /**
   * Deletes a worker and triggers delete event, with optimistic UI and duplicate guard.
   * @param {number} workerId - ID of worker to delete
   * @async
   */
  async function deleteWorker(workerId: number) {
    if (acting.has(workerId)) return;
    acting.add(workerId);

    const prev = statusMap.get(workerId);
    statusMap.set(workerId, 'D');
    statusMap = new Map(statusMap);

    try {
      await delWorker({ workerId });
      delete workersStatsMap[workerId];
      delete tasksCount[workerId];
    } catch (err) {
      console.error('Failed to delete worker', workerId, err);
      // revert optimistic status if needed
      if (prev) {
        statusMap.set(workerId, prev);
        statusMap = new Map(statusMap);
      }
    } finally {
      acting.delete(workerId);
    }
  }

// function to display task counts
function displayTasksCount(workerId: number, ...statuses: string[]): string {
  const n = statuses
    .map((s) => tasksCount[workerId]?.[s] || 0)
    .reduce((a, b) => a + b, 0);
  return n === 0 ? "" : n.toString();
}

</script>

{#if workers && workers.length > 0}

<div class="status-count-bar status-tabs">
    <a href="#/tasks"> All: {totalCount}</a>
    <a href="#/tasks?status=W">Waiting: {(allCount['W'] || 0)}</a>
    <a href="#/tasks?status=P" data-testid="pending-link-dashboard"> Pending: {(allCount['P'] || 0)}</a>
    <a href="#/tasks?status=A"> Assigned: {(allCount['A'] || 0)}</a>
    <a href="#/tasks?status=C">Accepted: {(allCount['C'] || 0)}</a>
    <a href="#/tasks?status=D">Downloading: {(allCount['D'] || 0)}</a>
    <a href="#/tasks?status=O">On hold: {(allCount['O'] || 0)}</a>
    <a href="#/tasks?status=R">Running: {(allCount['R'] || 0)}</a>
    <a href="#/tasks?status=U">Uploading: {(allCount['U'] || 0)+(allCount['V'] || 0)}</a>
    <a href="#/tasks?status=S">Succeeded: {(allCount['S'] || 0)}</a>
    <a href="#/tasks?status=F">Failed: {(allCount['F'] || 0)}</a>
    <a href="#/tasks?status=Z">Suspended: {(allCount['Z'] || 0)}</a>
    <a href="#/tasks?status=X">Canceled: {(allCount['X'] || 0)}</a>
  </div>

  <div class="workerCompo-table-wrapper">
    <div class="workerCompo-table-scroll-container">
      <table class="listTable workerCompo-table">
        <thead>
          <tr>
            {#if displayMode === 'table'}
              <!-- Table mode - show all columns -->
              <th>Name</th>
              <th>Wf.Step</th>
              <th>Status</th>
              <th>Concu</th>
              <th>Pref</th>
              <th>Starting</th>
              <th>Running</th>
              <th>Success</th>
              <th>Fail</th>
              <th>Inactive</th>
              <th>CPU</th>
              <th>Mem</th>
              <th>Disk Usage</th>
              {#if showAdvancedMetrics}
                <th>Disk R/W</th>
                <th>Network Sent/Recv</th>
              {/if}
              <th>Actions</th>
            {:else}
              <th>Name</th>
              <th>Wf.Step</th>
              <th>Status</th>
              <th>Concurrency</th>
              <th>Prefetch</th>
              <th>Starting</th>
              <th>Running</th>
              <th>Success</th>
              <th>Fail</th>
              <th>Inactive</th>
              <th colspan="7">Metrics</th>
              <th>Actions</th>
            {/if}
          </tr>
        </thead>
        <tbody>
          {#each displayWorkers as worker (worker.workerId)}
            <tr data-testid={`worker-${worker.workerId}`}>
              <td> <a href="#/tasks?workerId={worker.workerId}" data-testid={`worker-name-${worker.workerId}`} class="workerCompo-clickable">{worker.name}</a> </td>
              <td class="workerCompo-actions">
                <button class="btn-action" title="Edit"><Edit /></button>
              </td>
              <td>
                <div class="workerCompo-status-pill {getWorkerStatusClass(worker.status)}" title={getWorkerStatusText(worker.status)}></div>
              </td>

              <td class="value-cell">
                <div class="value-with-controls">
                  <span class="value">{worker.concurrency}</span>
                  <div class="controls">
                    <button 
                      class="small-btn" 
                      on:click={() => updateValue(worker, 'concurrency', 1)}
                      data-testid={`increase-concurrency-${worker.workerId}`}
                    >+</button>
                    <button 
                      class="small-btn" 
                      on:click={() => updateValue(worker, 'concurrency', -1)}
                      data-testid={`decrease-concurrency-${worker.workerId}`}
                    >-</button>
                  </div>
                </div>
              </td>

              <td class="value-cell">
                <div class="value-with-controls">
                  <span class="value">{worker.prefetch}</span>
                  <div class="controls">
                    <button 
                      class="small-btn" 
                      on:click={() => updateValue(worker, 'prefetch', 1)}
                      data-testid={`increase-prefetch-${worker.workerId}`}
                    >+</button>
                    <button 
                      class="small-btn" 
                      on:click={() => updateValue(worker, 'prefetch', -1)}
                      data-testid={`decrease-prefetch-${worker.workerId}`}
                    >-</button>
                  </div>
                </div>
              </td>
              <td
                data-testid={`tasks-awaiting-execution-${worker.workerId}`}
                title={`Pending: ${tasksCount[worker.workerId]?.pending}, Assigned: ${tasksCount[worker.workerId]?.assigned}, Accepted: ${tasksCount[worker.workerId]?.accepted}`}
              >
                {displayTasksCount(worker.workerId,'C','D','O')}
              </td>
              <td
                data-testid={`tasks-in-progress-${worker.workerId}`}
                title={`Downloading: ${tasksCount[worker.workerId]?.downloading}, Running: ${tasksCount[worker.workerId]?.running}`}
              >
                {displayTasksCount(worker.workerId,'R','U','V')}
              </td>

              <td
                data-testid={`successful-tasks-${worker.workerId}`}
                title={`UploadingSuccess: ${tasksCount[worker.workerId]?.uploadingSuccess}, Succeeded: ${tasksCount[worker.workerId]?.succeeded}`}
              >
                {displayTasksCount(worker.workerId,'S')}
              </td>

              <td
                data-testid={`failed-tasks-${worker.workerId}`}
                title={`UploadingFailure: ${tasksCount[worker.workerId]?.uploadingFailure}, Failed: ${tasksCount[worker.workerId]?.failed}`}
              >
                {displayTasksCount(worker.workerId,'F')}
              </td>

              <td
                data-testid={`inactive-tasks-${worker.workerId}`}
                title={`Waiting: ${tasksCount[worker.workerId]?.waiting}, Suspended: ${tasksCount[worker.workerId]?.suspended}, Canceled: ${tasksCount[worker.workerId]?.canceled}`}
              >
                {displayTasksCount(worker.workerId,'I','Z','X')}
              </td>

            <!-- Conditional columns based on display mode -->
            {#if workersStatsMap[worker.workerId]}
              {#if displayMode === 'table'}
                <!-- Table mode -->
                <td title={`IOWait: ${workersStatsMap[worker.workerId]?.iowaitPercent?.toFixed(1) ?? 'N/A'}%` + '\n' + `Load: ${workersStatsMap[worker.workerId]?.load1Min?.toFixed(1) ?? 'N/A'}`}>
                  {workersStatsMap[worker.workerId]?.cpuUsagePercent?.toFixed(1) ?? 'N/A'}%
                  {#if workersStatsMap[worker.workerId]?.iowaitPercent > 1}
                    <div style="color: red;">{workersStatsMap[worker.workerId]?.iowaitPercent?.toFixed(1) ?? 'N/A'}%</div>
                  {/if}
                </td>
                <td>{workersStatsMap[worker.workerId]?.memUsagePercent?.toFixed(1) ?? 'N/A'}%</td>
                <td>
                  <div class="scrollable-cell">
                    {#each workersStatsMap[worker.workerId].disks as disk, i (i)}
                      <div>{disk.deviceName}: {disk.usagePercent.toFixed(1) ?? 'N/A'}%</div>
                    {/each}
                  </div>
                </td>
                {#if showAdvancedMetrics}
                  <!-- Disk R/W column -->
                  <td>
                    {#if workersStatsMap[worker.workerId].diskIo}
                      <div>{formatBytesPair(workersStatsMap[worker.workerId].diskIo.readBytesRate, workersStatsMap[worker.workerId].diskIo.writeBytesRate) ?? 'N/A'}/s</div>
                      <div>{formatBytesPair(workersStatsMap[worker.workerId].diskIo.readBytesTotal, workersStatsMap[worker.workerId].diskIo.writeBytesTotal) ?? 'N/A'}</div>
                    {:else}
                      <div>N/A</div>
                    {/if}
                  </td>

                  <!-- Network Sent/Recv column -->
                  <td>
                    {#if workersStatsMap[worker.workerId].netIo}
                      <div>{formatBytesPair(workersStatsMap[worker.workerId].netIo.sentBytesRate, workersStatsMap[worker.workerId].netIo.recvBytesRate) ?? 'N/A'}/s</div>
                      <div>{formatBytesPair(workersStatsMap[worker.workerId].netIo.sentBytesTotal, workersStatsMap[worker.workerId].netIo.recvBytesTotal) ?? 'N/A'}</div>
                    {:else}
                      <div>N/A</div>
                    {/if}
                  </td>
                {/if}
              {:else}
                <!-- Chart mode -->
                <td colspan="7">
                  <div class="combined-metrics-container">
                    <!-- Left side - CPU, Load, RAM metrics -->
                    <div class="system-metrics">
                      <div class="metrics-charts">
                        <!-- First row - CPU and Load -->
                        <div class="metrics-row">
                          <!-- CPU -->
                          <div class="metric-chart">
                            <div class="chart-title">CPU: {workersStatsMap[worker.workerId].cpuUsagePercent?.toFixed(1) ?? 0}%</div>
                            <div class="chart-bar-container">
                              <div class="chart-bar" style={`width: ${workersStatsMap[worker.workerId].cpuUsagePercent ?? 0}%`}></div>
                            </div>
                          </div>

                          <!-- Load -->
                          <div class="metric-chart">
                            <div class="chart-title">Load: {workersStatsMap[worker.workerId].load1Min?.toFixed(1) ?? 0}</div>
                            <div class="chart-bar-container">
                              <div class="chart-bar" style={`width: ${Math.min(100, (workersStatsMap[worker.workerId].load1Min ?? 0) * 33.3)}%`}></div>
                            </div>
                          </div>
                        </div>
                      
                        <!-- Second row - RAM and IOWait -->
                        <div class="metrics-row">
                          <!-- RAM -->
                          <div class="metric-chart">
                            <div class="chart-title">RAM: {workersStatsMap[worker.workerId].memUsagePercent?.toFixed(1) ?? 0}%</div>
                            <div class="chart-bar-container">
                              <div class="chart-bar" style={`width: ${workersStatsMap[worker.workerId].memUsagePercent ?? 0}%`}></div>
                            </div>
                          </div>
                          
                          <!-- IOWait -->
                          <div class="metric-chart">
                            <div class="chart-title">IOWait: {workersStatsMap[worker.workerId].iowaitPercent?.toFixed(1) ?? 0}%</div>
                            <div class="chart-bar-container">
                              <div class="chart-bar" style={`width: ${workersStatsMap[worker.workerId].iowaitPercent ?? 0}%`}></div>
                            </div>
                          </div>
                        </div>
                        
                        <!-- Disk Usage -->
                        <div class="disk-chart-container">
                          <div class="chart-title">Disk Usage</div>
                          {#if workersStatsMap[worker.workerId]?.disks?.length > 0}
                            <div class="disk-bars scrollable-disks">
                              {#each workersStatsMap[worker.workerId].disks as disk, i (i)}
                                <div class="disk-bar-container">
                                  <div class="disk-info">
                                    <span class="disk-name">{disk.deviceName}</span>
                                    <span class="disk-percent">{disk.usagePercent.toFixed(1)}%</span>
                                  </div>
                                  <div class="disk-bar-background">
                                    <div class="disk-bar-fill {disk.usagePercent > 80 ? 'warning' : ''}" 
                                        style={`width: ${disk.usagePercent}%`}>
                                    </div>
                                  </div>
                                </div>
                              {/each}
                            </div>
                            {:else}
                            <div class="no-data">No disk usage data</div>
                          {/if}
                        </div>

                      </div>
                    </div>

                    <!-- Right side - Disk I/O and Network I/O charts -->
                    <div class="io-charts">
                      <div class="io-chart-container">
                        <div class="chart-title">Disk I/O</div>
                        {#if workersStatsMap[worker.workerId]?.diskIo}
                          <LineChart
                            data-testid="disk-chart"
                            line1={diskChartData.line1}
                            line2={diskChartData.line2}
                            color1="#36A2EB"
                            color2="#FF6384"
                            title1="Read" 
                            title2="Write"
                            value1={formatBytes(workersStatsMap[worker.workerId]?.diskIo?.readBytesRate || 0) + "/s"}
                            value2={formatBytes(workersStatsMap[worker.workerId]?.diskIo?.writeBytesRate || 0) + "/s"}
                            total1={formatBytes(workersStatsMap[worker.workerId]?.diskIo?.readBytesTotal || 0)}
                            total2={formatBytes(workersStatsMap[worker.workerId]?.diskIo?.writeBytesTotal || 0)}
                            zoomLevel={diskZoom}
                            autoZoom={diskAutoZoom}
                            onZoom={handleDiskZoom}
                            onToggleAutoZoom={() => toggleAutoZoom('disk')}
                            zoomIntensity={diskAutoZoom ? diskZoom/5 : 1}
                          />
                        {:else}
                          <div class="no-data">No disk data</div>
                        {/if}
                      </div>

                      <div class="io-chart-container" data-testid={`I/O-chart-${worker.workerId}`} >
                        <div class="chart-title">Network I/O</div>
                        {#if workersStatsMap[worker.workerId]?.netIo}
                          <LineChart 
                            data-testid="network-chart"
                            line1={networkChartData.line1}
                            line2={networkChartData.line2}
                            color1="#36A2EB"
                            color2="#FF6384"
                            title1="Sent" 
                            title2="Received"
                            value1={formatBytes(workersStatsMap[worker.workerId]?.netIo?.sentBytesRate || 0) + "/s"}
                            value2={formatBytes(workersStatsMap[worker.workerId]?.netIo?.recvBytesRate || 0) + "/s"}
                            total1={formatBytes(workersStatsMap[worker.workerId]?.netIo?.sentBytesTotal || 0)}
                            total2={formatBytes(workersStatsMap[worker.workerId]?.netIo?.recvBytesTotal || 0)}
                            zoomLevel={networkZoom}
                            autoZoom={networkAutoZoom}
                            onZoom={handleNetworkZoom}
                            onToggleAutoZoom={() => toggleAutoZoom('network')}
                            zoomIntensity={networkAutoZoom ? networkZoom/5 : 1}
                          />
                        {:else}
                          <div class="no-data">No network data</div>
                        {/if}
                      </div>
                    </div>
                  </div>
                </td>
              {/if}
            {:else}
              {#if showAdvancedMetrics}
                <td colspan="5">No statistics available</td>
              {:else}
                <td colspan="3">No statistics available</td>
              {/if}
            {/if}

              <td class="workerCompo-actions">
                <div class="action-row">
                  <button class="btn-action" title="Pause" on:click={() => pauseWorker(worker)} disabled={acting.has(worker.workerId)} data-testid={`pause-worker-${worker.workerId}`}>
                    <PauseCircle />
                  </button>
                  <button class="btn-action" title="Clean"><Eraser /></button>
                </div>
                <div class="action-row">
                  <button class="btn-action" title="Restart"><RefreshCw /></button>
                  <button class="btn-action" title="Delete" on:click={() => { deleteWorker(worker.workerId); }} data-testid={`delete-worker-${worker.workerId}`} disabled={acting.has(worker.workerId)}>
                    <Trash />
                  </button>
                </div>
                <div class="action-row">
                    {#if displayMode == 'charts'}
                      <button data-testid={`table-worker-${worker.workerId}`} class="btn-action" on:click={() => displayMode = 'table'} title="Numbers">
                        <FileDigit />
                      </button>
                    {:else if displayMode == 'table'}
                      <button data-testid={`charts-worker-${worker.workerId}`} class="btn-action"  on:click={() => displayMode = 'charts'} title="Charts">
                        <BarChart/>
                      </button>
                      <button class="btn-action" 
                      on:click={() => showAdvancedMetrics = !showAdvancedMetrics}
                      title={showAdvancedMetrics ? 'Hide advanced metrics' : 'Show advanced metrics'}
                      data-testid={showAdvancedMetrics ? 'hide-advanced-metrics' : 'advanced-metrics'}
                      >
                        {#if showAdvancedMetrics}
                          <ChevronUp/>
                        {:else}
                          <ChevronDown/>
                        {/if}
                      </button>
                    {/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{:else}
  <p class="workerCompo-empty-state">No workers found.</p>
{/if}