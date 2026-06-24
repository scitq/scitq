<script lang="ts">
  import { run, stopPropagation, createBubbler } from 'svelte/legacy';

  const bubble = createBubbler();
  import { onMount, onDestroy } from 'svelte';
  import {getWorkerStatusClass,delWorker, getWorkerStatusText, getStats, formatBytesPair, getTasksCount, getStatus, updateWorkerStatus, getAllTaskStats, updateWorkerConfig, requestWorkerUpgrade, listWorkerEvents, resetWorkerCounters, getWorkers} from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import { Edit, PauseCircle, PlayCircle, Trash, RefreshCw, Eraser, BarChart, FileDigit, ChevronDown, ChevronUp, Star, ArrowUpCircle } from 'lucide-svelte';
  import LineChart from './LineChart.svelte';
  import '../styles/worker.css';
  import { WorkerStats } from '../../gen/taskqueue';


  /**
   * Internal array of workers (stateful, managed here)
   */
  let internalWorkers: Array<Worker> = $state([]);
  
  
  interface Props {
    /**
   * Callback function when worker is updated
   * @type {(event: { detail: { workerId: number; updates: Partial<Worker> } }) => void}
   */
    onWorkerUpdated?: (event: { detail: { workerId: number; updates: Partial<Worker> } }) => void;
  }

  let { onWorkerUpdated = () => {} }: Props = $props();
  
  /**
   * Map of worker statistics by worker ID
   * @type {Record<number, taskqueue.WorkerStats>}
   */
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = $state({});

  /**
   * Count of tasks by worker ID and status
   * @type {Record<number, Record<string, number>>}
   */
  let tasksCount: Record<number, Record<string, number>> = $state({});
  
  /**
   * Count of all tasks by status
   * @type {Record<string, number>}
   */
  let allCount: Record<string, number> = $state({});
  /**
   * Total number of tasks
   * @type {number}
   */
  let totalCount = $state(0);
  
  // Set of worker IDs currently being acted upon (pause/delete)
  let acting = new Set<number>();
  
  /**
   * Per-worker display mode ('table' or 'charts'), keyed by workerId.
   * Workers not in the map have stats collapsed (no stats shown).
   */
  let workerDisplayMode: Record<number, 'table' | 'charts'> = $state({});

  /**
   * Flag indicating if data has been loaded
   * @type {boolean}
   */
  let hasLoaded = $state(false);


  /**
   * Per-worker advanced metrics toggle, keyed by workerId.
   */
  let workerAdvancedMetrics: Record<number, boolean> = {};

  // Chart data
  /**
   * Maximum number of data points to keep in history
   * @type {number}
   */
  const MAX_HISTORY = 30;
  
  /**
   * Per-worker history of disk I/O metrics
   * @type {Record<number, Array<{time: Date, read: number, write: number}>>}
   */
  let diskHistories: Record<number, {time: Date, read: number, write: number}[]> = $state({});
  /**
   * Per-worker history of network I/O metrics
   * @type {Record<number, Array<{time: Date, sent: number, received: number}>>}
   */
  let networkHistories: Record<number, {time: Date, sent: number, received: number}[]> = $state({});
  
  // Zoom controls
  /**
   * Current zoom level for disk chart
   * @type {number}
   */
  let diskZoom = $state(1);
  
  /**
   * Current zoom level for network chart
   * @type {number}
   */
  let networkZoom = $state(1);
  
  /**
   * Flag for auto-zoom on disk chart
   * @type {boolean}
   */
  let diskAutoZoom = $state(true);
  
  /**
   * Flag for auto-zoom on network chart
   * @type {boolean}
   */
  let networkAutoZoom = $state(true);

  // --- WS unsubscribe reference for cleanup ---
  let unsubscribeWS: (() => void) | null = null;



  /**
   * Component mount lifecycle hook
   * Sets up WS event listeners (no periodic updateWorkerData).
   */
  onMount(() => {
    // On mount, load all task stats and fetch workers
    getAllTaskStats().then(({ perWorkerStatusCounts, globalStatusCounts, totalCount: t }) => {
      allCount = globalStatusCounts;
      tasksCount = perWorkerStatusCounts;
      totalCount = t;
    });
    // Fetch initial workers
    import('../lib/api').then(api => {
      api.getWorkers().then(w => {
        internalWorkers = w;
      });
    });

    // WebSocket message handler
    const handleMessage = (msg: unknown) => {
      if (!msg || typeof msg !== 'object') return;
      const { type, action, id, payload } = msg as any;

      if (type === 'worker') {
        switch (action) {
          case 'status': {
            const status = (payload as any).status;
            if (typeof status === 'string') {
              internalWorkers = internalWorkers.map(w =>
                w.workerId === id ? { ...w, status } : w
              );
            }
            break;
          }
          case 'stats': {
            const raw = (payload as any).stats;
            if (raw) {
              // Use server-computed rates directly (computed worker-side with accurate timing)
              workersStatsMap[id] = WorkerStats.fromJson(raw);
              workersStatsMap = { ...workersStatsMap };
              updateHistoryForWorker(id);
            }
            break;
          }
          case 'deleted': {
            internalWorkers = internalWorkers.filter(w => w.workerId !== id);
            break;
          }
          case 'deletionScheduled': {
            internalWorkers = internalWorkers.map(w =>
              w.workerId === id ? { ...w, status: 'D' } : w
            );
            break;
          }
          case 'created': {
            const worker = (payload as any).worker || (payload as any);
            if (worker) {
              const exists = internalWorkers.some(w => w.workerId === worker.workerId);
              if (!exists) {
                internalWorkers = [...internalWorkers, worker];
              }
            }
            break;
          }
          case 'updated':
            // Update worker fields reactively, including stepId and stepName for step reassignment
            {
              const updates = payload as Partial<Worker>;
              internalWorkers = internalWorkers.map(w =>
                w.workerId === id
                  ? {
                      ...w,
                      concurrency: updates.concurrency ?? w.concurrency,
                      prefetch: updates.prefetch ?? w.prefetch,
                      stepId: updates.stepId ?? w.stepId,
                      stepName: updates.stepName ?? w.stepName
                    }
                  : w
              );
            }
            break;
        }
      } else if (type === 'task') {
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
            if (allCount) {
              allCount[payload.status] = (allCount[payload.status] || 0) + 1;
              allCount = { ...allCount };
            }
            totalCount++;
            break;
          }
          case 'updated':
            break;
          case 'hidden': {
            // Task became hidden (e.g. via retry/edit-and-retry). The
            // server now emits this so the per-worker counter for the
            // old task's status can decrement on the right worker —
            // without it, retrying a task in C/D/O/R/U/V/S/W/P leaves
            // that worker's bucket stuck (the hidden-F policy: hidden F
            // still counts in F, so we skip the decrement only when
            // oldStatus is F).
            const workerId = payload.workerId;
            const oldStatus = payload.oldStatus;
            if (oldStatus && oldStatus !== 'F') {
              allCount[oldStatus] = (allCount[oldStatus] || 0) - 1;
              if (allCount[oldStatus] < 0) allCount[oldStatus] = 0;
              allCount = { ...allCount };
              if (workerId !== undefined && tasksCount[workerId]) {
                tasksCount[workerId][oldStatus] = (tasksCount[workerId][oldStatus] || 0) - 1;
                if (tasksCount[workerId][oldStatus] < 0) tasksCount[workerId][oldStatus] = 0;
                tasksCount = { ...tasksCount };
              }
            }
            break;
          }
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
      }
    };

    unsubscribeWS = wsClient.subscribeWithTopics({ worker: [], task: [] }, handleMessage);

    return () => {
      if (unsubscribeWS) unsubscribeWS();
      unsubscribeWS = null;
    };
  });



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
   * Prepares chart data for rendering
   * @param {Array} history - Data history array
   * @param {'disk'|'network'} type - Type of chart data
   * @returns {Object} Prepared chart data including lines and zoom info
   */
  function prepareChartData(history, type: 'disk' | 'network') {
    const isAutoZoom = type === 'disk' ? diskAutoZoom : networkAutoZoom;
    let currentZoom = type === 'disk' ? diskZoom : networkZoom;

    const dataValues = history.flatMap(d =>
      type === 'disk' ? [d.read || 0, d.write || 0] : [d.sent || 0, d.received || 0]
    );
    const dataMax = Math.max(1, ...dataValues);

    // Auto-zoom = fit Y axis to data. Manual zoom = user explicitly
    // zooms in past the max (peaks clip — that's the user's choice).
    // Previous code computed an auto-zoom factor (avg/range) that
    // could divide displayMax below dataMax, so peaks clipped while
    // still in auto mode — visible as a flat top on the curve when
    // a new maximum arrived. With auto-zoom on we now ignore the
    // computed factor and always render with displayMax >= dataMax.
    let displayMax: number;
    if (isAutoZoom) {
      currentZoom = 1; // legend display only — keep "1x" while we autofit
      displayMax = dataMax * 1.05;
    } else {
      displayMax = dataMax * 1.05 / currentZoom;
    }

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
      if (diskAutoZoom) diskZoom = 1;
    } else {
      networkAutoZoom = !networkAutoZoom;
      if (networkAutoZoom) networkZoom = 1;
    }
    // No need to force recalculation here; chart data is computed per-worker inline.
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
    // Chart data is computed per-worker inline.
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
    // Chart data is computed per-worker inline.
  }

  /**
   * Updates worker data by fetching latest stats
   * @async
   */
  async function updateWorkerData() {
    if (internalWorkers.length === 0) return;

    try {
      const workerIds = internalWorkers.map(w => w.workerId);
      workersStatsMap = await getStats(workerIds);

      // Update per-worker history data
      const now = new Date();
      for (const id of workerIds) {
        const stats = workersStatsMap[id];
        // Disk I/O
        if (!diskHistories[id]) diskHistories[id] = [];
        diskHistories[id] = [
          ...diskHistories[id].slice(-MAX_HISTORY + 1),
          {
            time: now,
            read: stats?.diskIo?.readBytesRate || 0,
            write: stats?.diskIo?.writeBytesRate || 0
          }
        ];
        // Network I/O
        if (!networkHistories[id]) networkHistories[id] = [];
        networkHistories[id] = [
          ...networkHistories[id].slice(-MAX_HISTORY + 1),
          {
            time: now,
            sent: stats?.netIo?.sentBytesRate || 0,
            received: stats?.netIo?.recvBytesRate || 0
          }
        ];
      }

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
   * Updates per-worker disk and network histories from current workersStatsMap.
   */
  function updateHistoryForWorker(workerId: number) {
    const now = new Date();
    const stats = workersStatsMap[workerId];
    if (!stats) return;
    // Disk I/O
    if (!diskHistories[workerId]) diskHistories[workerId] = [];
    diskHistories[workerId] = [
      ...diskHistories[workerId].slice(-MAX_HISTORY + 1),
      {
        time: now,
        read: stats?.diskIo?.readBytesRate || 0,
        write: stats?.diskIo?.writeBytesRate || 0
      }
    ];
    // Network I/O
    if (!networkHistories[workerId]) networkHistories[workerId] = [];
    networkHistories[workerId] = [
      ...networkHistories[workerId].slice(-MAX_HISTORY + 1),
      {
        time: now,
        sent: stats?.netIo?.sentBytesRate || 0,
        received: stats?.netIo?.recvBytesRate || 0
      }
    ];
    // Trigger Svelte reactivity
    diskHistories = { ...diskHistories };
    networkHistories = { ...networkHistories };
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
      // Also update internalWorkers to reflect change
      internalWorkers = internalWorkers.map(w =>
        w.workerId === worker.workerId ? { ...w, [field]: newValue } : w
      );
      // Perform gRPC update
      try {
        await updateWorkerConfig(worker.workerId, { [field]: newValue });
      } catch (err) {
        console.error('Failed to update worker config', worker.workerId, field, newValue, err);
      }
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

  // A "running" worker with concurrency=0 is excluded from assignment
  // by the SQL's `HAVING SUM < concurrency+prefetch` clause — silently.
  // The operator sees a green-dot worker that picks up nothing. To make
  // that condition discoverable, treat it as a visual variant of
  // "paused": yellow dot, play icon on the action button, and a click
  // that opens the raise-concurrency modal rather than firing P→R.
  // The DB status itself is left alone — see ROADMAP rationale (no
  // P→? auto-restore state machine to invent).
  function isConcurrencyZeroIdle(worker): boolean {
    return worker?.status === 'R' && (worker?.concurrency ?? 0) === 0;
  }

  function workerEffectiveStatusClass(worker): string {
    if (isConcurrencyZeroIdle(worker)) return 'paused';
    return getWorkerStatusClass(worker?.status ?? '');
  }

  function workerEffectiveStatusText(worker): string {
    if (isConcurrencyZeroIdle(worker)) {
      return 'Concurrency 0 — set a positive value to start';
    }
    if (worker?.status === 'P' && (worker?.concurrency ?? 0) === 0) {
      return 'Paused (concurrency is also 0 — raise it before unpausing)';
    }
    return getWorkerStatusText(worker?.status ?? '');
  }

  // Show play icon whenever the worker is effectively idle from the
  // operator's perspective — true pause OR concurrency=0 — so the
  // action they probably want next is "make it work again".
  function showPlayIcon(worker): boolean {
    return worker?.status === 'P' || isConcurrencyZeroIdle(worker);
  }

  // Raise-concurrency modal state. Opened when the operator clicks
  // play/pause on a worker whose concurrency is 0 — proceeding with a
  // plain P→R would leave the worker yellow because the SQL would
  // still skip it. The modal forces the operator to set a value first.
  let raiseConcModalWorker: any = $state(null);
  let raiseConcModalValue: number = $state(1);

  function openRaiseConcurrencyModal(worker) {
    raiseConcModalWorker = worker;
    raiseConcModalValue = 1;
  }

  function closeRaiseConcurrencyModal() {
    raiseConcModalWorker = null;
  }

  async function confirmRaiseConcurrency() {
    const worker = raiseConcModalWorker;
    if (!worker) return;
    const newConc = Math.max(1, Math.floor(raiseConcModalValue || 1));
    try {
      await updateWorkerConfig(worker.workerId, { concurrency: newConc });
      // If the worker was Paused, also unpause it — the operator asked
      // to "play". If it was R+concurrency==0, leave status alone (it
      // already is R; raising concurrency is sufficient to start work).
      if (worker.status === 'P') {
        await updateWorkerStatus({ workerId: worker.workerId, status: 'R' });
      }
      internalWorkers = internalWorkers.map(w =>
        w.workerId === worker.workerId
          ? { ...w, concurrency: newConc, status: worker.status === 'P' ? 'R' : w.status }
          : w
      );
    } catch (err) {
      console.error('Failed to raise concurrency / unpause', worker.workerId, err);
    } finally {
      closeRaiseConcurrencyModal();
    }
  }

  /**
   * Pause/resume/quiet/unquiet a worker.
   * @param {Object} worker - Worker object
   * @async
   */
  async function pauseWorker(worker): Promise<void> {
    const curr = worker.status;

    // Special case: the operator clicked "play" on a worker that the
    // scheduler is silently ignoring because of concurrency=0. A plain
    // status update wouldn't make tasks flow — the SQL would still
    // skip the worker. Open the raise-concurrency modal so the
    // operator sets a value, then the unpause is folded into the
    // confirm path. Covers both R+conc=0 (worker visible as yellow
    // but DB-paused=false) and P+conc=0 (truly paused AND zero
    // capacity — even unpausing won't help).
    if ((curr === 'R' || curr === 'P') && (worker.concurrency ?? 0) === 0) {
      openRaiseConcurrencyModal(worker);
      return;
    }

    const next = computeNextStatusForPause(curr);
    if (!next) return;

    if (acting.has(worker.workerId)) return;
    acting.add(worker.workerId);

    // optimistic UI update
    const prev = curr;
    internalWorkers = internalWorkers.map(w =>
      w.workerId === worker.workerId ? { ...w, status: next } : w
    );

    try {
      await updateWorkerStatus({ workerId: worker.workerId, status: next });
    } catch (err) {
      console.error('Failed to update worker status', worker.workerId, next, err);
      // revert on failure
      internalWorkers = internalWorkers.map(w =>
        w.workerId === worker.workerId ? { ...w, status: prev } : w
      );
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

    // optimistic UI update
    const prevWorker = internalWorkers.find(w => w.workerId === workerId);
    internalWorkers = internalWorkers.map(w =>
      w.workerId === workerId ? { ...w, status: 'D' } : w
    );

    try {
      await delWorker({ workerId });
      delete workersStatsMap[workerId];
      delete tasksCount[workerId];
      // Actually remove from list after confirmed deletion
      internalWorkers = internalWorkers.filter(w => w.workerId !== workerId);
    } catch (err) {
      console.error('Failed to delete worker', workerId, err);
      // revert optimistic status if needed
      if (prevWorker) {
        internalWorkers = internalWorkers.map(w =>
          w.workerId === workerId ? { ...prevWorker } : w
        );
      }
    } finally {
      acting.delete(workerId);
    }
  }

  /**
   * Phase II: operator-triggered upgrade of one worker. Plain click is
   * normal (idle-wait); shift-click is emergency (drain in-flight then
   * upgrade). If a request is already pending, either click cancels.
   * See specs/worker_autoupgrade.md.
   */
  async function upgradeWorker(worker: any, ev: MouseEvent) {
    if (acting.has(worker.workerId)) return;
    const pending = worker.upgradeRequested;
    let mode: 'normal' | 'emergency' | 'cancel';
    if (pending) {
      mode = 'cancel';
    } else if (ev.shiftKey) {
      mode = 'emergency';
    } else {
      mode = 'normal';
    }
    acting.add(worker.workerId);
    // Optimistic local update.
    const previous = worker.upgradeRequested;
    internalWorkers = internalWorkers.map(w =>
      w.workerId === worker.workerId
        ? { ...w, upgradeRequested: mode === 'cancel' ? '' : mode }
        : w
    );
    try {
      await requestWorkerUpgrade(mode, [worker.workerId]);
    } catch (err) {
      // Roll back the optimistic update.
      internalWorkers = internalWorkers.map(w =>
        w.workerId === worker.workerId ? { ...w, upgradeRequested: previous } : w
      );
    } finally {
      acting.delete(worker.workerId);
    }
  }

  /** State for workflow/step editor */
  let editingWorkflowStepFor: number | null = $state(null);

  let workflowOptions: Array<any> = $state([]);
  let workflowOffset: number = $state(0);
  let selectedWorkflowId: number | null = $state(null);
  let workflowSearch: string = $state("");
  let workflowSearchTimer: ReturnType<typeof setTimeout> | null = null;
  const WORKFLOW_PAGE = 50;

  let stepOptions: Array<any> = $state([]);
  let selectedStepId: number | null = $state(null);

  let loadingWorkflows: boolean = $state(false);
  let loadingSteps: boolean = false;

  /** Worker-events viewer state (the info/warning badge opens this). */
  let eventsWorker: any = $state(null);          // worker whose events are shown, or null
  let workerEvents: Array<any> = $state([]);
  let eventsLoading: boolean = $state(false);
  // Inline status line for the Clear/Reset buttons. Shown above the
  // events list. Cleared on next panel open / on success refresh.
  let eventsStatus: string = $state('');
  let eventsBusy: boolean = $state(false);
  // Transient "flash" feedback for the inline Success/Fail reset
  // buttons. Map workerId -> 'S' | 'F' | null while the cell briefly
  // pulses pale yellow after a successful reset; cleared by a
  // setTimeout. Pure UI state; no server impact.
  let cellFlash: Record<number, 'S' | 'F' | null> = $state({});

  async function openWorkerEvents(worker: any) {
    eventsWorker = worker;
    eventsLoading = true;
    workerEvents = [];
    eventsStatus = '';
    eventsBusy = false;
    try {
      workerEvents = await listWorkerEvents(worker.workerId, 100);
    } catch (err) {
      console.error('Failed to load worker events', worker.workerId, err);
    } finally {
      eventsLoading = false;
    }
  }

  function closeWorkerEvents() {
    eventsWorker = null;
    workerEvents = [];
    eventsStatus = '';
    eventsBusy = false;
  }

  // Re-syncs eventsWorker and internalWorkers from a fresh ListWorkers
  // fetch so the badge / counters reflect actual server state. Used by
  // the Clear/Reset handlers because we've seen at least one prod
  // deployment where the RPC reply gets mangled in transit but the
  // server-side write still lands — the user observed "click does
  // nothing, refresh works", which is exactly the resync this avoids.
  async function refreshWorkerFromServer(wid: number): Promise<{pendingWarnings: number; recentFailures: number} | null> {
    try {
      const fresh = await getWorkers();
      const w = fresh.find((x: any) => x.workerId === wid) as any;
      if (!w) return null;
      if (eventsWorker && eventsWorker.workerId === wid) {
        eventsWorker = w;
      }
      internalWorkers = internalWorkers.map(x => x.workerId === wid ? w : x);
      return {
        pendingWarnings: w.pendingWarnings ?? 0,
        recentFailures: w.recentFailures ?? 0,
      };
    } catch (err) {
      console.error('Failed to re-fetch worker after counter reset', wid, err);
      return null;
    }
  }

  async function handleClearWarnings() {
    if (!eventsWorker || eventsBusy) return;
    const wid = eventsWorker.workerId;
    eventsBusy = true;
    eventsStatus = 'Clearing warnings…';
    let rpcErr: unknown = null;
    try {
      await resetWorkerCounters(wid, { clearWarnings: true });
    } catch (err) {
      rpcErr = err;
      console.error('resetWorkerCounters(clearWarnings) RPC error for worker', wid, err);
    }
    // Re-fetch unconditionally: the server may have applied the write
    // even if the RPC reply didn't make it back to the browser
    // (alpha2-style proxy quirk). Trust the DB, not the local optimism.
    const fresh = await refreshWorkerFromServer(wid);
    if (fresh && fresh.pendingWarnings === 0) {
      eventsStatus = 'Warnings cleared.';
      try { workerEvents = await listWorkerEvents(wid, 100); } catch (e) { console.error(e); }
    } else if (rpcErr) {
      eventsStatus = 'Could not clear warnings — the server did not acknowledge. Check server logs.';
    } else {
      eventsStatus = '';
    }
    eventsBusy = false;
  }

  // Re-fetches per-worker task counts from the server. Used after a
  // reset that targets the dashboard F / S columns — those numbers
  // live in tasksCount[][], populated by getAllTaskStats, not on the
  // worker row itself.
  async function refreshTaskCountsFromServer() {
    try {
      const { perWorkerStatusCounts, globalStatusCounts, totalCount: t } = await getAllTaskStats();
      tasksCount = perWorkerStatusCounts;
      allCount = globalStatusCounts;
      totalCount = t;
    } catch (err) {
      console.error('Failed to re-fetch task stats after reset', err);
    }
  }

  // Inline cell reset for Success / Fail columns. Called by the tiny
  // grey button anchored to the bottom-right of each cell.  Defensive
  // re-fetch after the RPC, same as the warnings ack: the server may
  // have applied the write even if the reply gets mangled in transit
  // (alpha2-style proxy quirk).
  async function resetCellCounter(workerId: number, kind: 'S' | 'F') {
    if (cellFlash[workerId] === kind) return; // debounce double-click
    try {
      await resetWorkerCounters(workerId, kind === 'F'
        ? { clearFailures: true }
        : { clearSuccesses: true });
    } catch (err) {
      console.error(`resetWorkerCounters(${kind}) RPC error for worker`, workerId, err);
    }
    // Refresh dashboard counts and the worker row (badge counter for F).
    await refreshTaskCountsFromServer();
    if (kind === 'F') await refreshWorkerFromServer(workerId);
    // Pale-yellow flash so the operator sees the click landed even
    // when the count was already 0.
    cellFlash = { ...cellFlash, [workerId]: kind };
    setTimeout(() => {
      cellFlash = { ...cellFlash, [workerId]: null };
    }, 700);
  }

  /**
   * Loads initial workflows lazily (first page only)
   */
  async function loadWorkflows() {
    if (loadingWorkflows) return;
    loadingWorkflows = true;

    try {
      const api = await import('../lib/api');
      const res = await api.listWorkflows({
        nameLike: workflowSearch.trim() || undefined,
        limit: WORKFLOW_PAGE,
        offset: workflowOffset,
      });

      const newList = res?.workflows ?? [];
      workflowOptions = [...workflowOptions, ...newList];

      workflowOffset += newList.length;
    } catch (err) {
      console.error("Failed to load workflows:", err);
    } finally {
      loadingWorkflows = false;
    }
  }

  /** Reset the list and reload from the first page for the current search term. */
  async function resetAndLoadWorkflows() {
    workflowOptions = [];
    workflowOffset = 0;
    await loadWorkflows();
  }

  /** Debounced handler for the workflow search box. */
  function onWorkflowSearchInput() {
    if (workflowSearchTimer) clearTimeout(workflowSearchTimer);
    workflowSearchTimer = setTimeout(() => {
      resetAndLoadWorkflows();
    }, 250);
  }


// function to display task counts
function displayTasksCount(workerId: number, ...statuses: string[]): string {
  const n = statuses
    .map((s) => tasksCount[workerId]?.[s] || 0)
    .reduce((a, b) => a + b, 0);
  return n === 0 ? "" : n.toString();
}

  /**
   * Loads steps for a specific workflow, replacing any previously loaded list.
   * Called when user selects a workflow during inline editing.
   */
  async function loadStepsForWorkflow(workflowId: number | null) {
    if (!workflowId) {
      stepOptions = [];
      selectedStepId = null;
      return;
    }

    if (loadingSteps) return;
    loadingSteps = true;

    try {
      const api = await import('../lib/api');
      const res = await api.listSteps({ workflowId });

      stepOptions = res?.steps ?? [];

      // Auto-select first step only if nothing is selected
      if (selectedStepId === null && stepOptions.length > 0) {
        selectedStepId = stepOptions[0].stepId;
      }
    } catch (err) {
      console.error("Failed to load steps for workflow", workflowId, err);
    } finally {
      loadingSteps = false;
    }
  }

  // Make reactive
  run(() => {
    diskZoom, networkZoom, diskAutoZoom, networkAutoZoom;
  });
  // Reactive statements
  run(() => {
    if (internalWorkers.length > 0 && !hasLoaded) {
      updateWorkerData();
      hasLoaded = true;
    }
  });
</script>

{#if internalWorkers && internalWorkers.length > 0}

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
            <!-- Table-mode columns always shown; stats content is per-worker -->
              <th>Name</th>
              <th>Wf.Step</th>
              <th>Scope</th>
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
              <th>Actions</th>
            {#if false}
              <th>Name</th>
              <th>Wf.Step</th>
              <th>Scope</th>
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
          {#each internalWorkers as worker (worker.workerId)}
            <tr data-testid={`worker-${worker.workerId}`}>
              <td>
                <a
                  href="#/tasks?workerId={worker.workerId}"
                  data-testid={`worker-name-${worker.workerId}`}
                  class="workerCompo-clickable"
                  title="{worker.flavor || 'unknown'}{worker.flavorCpu ? ` — ${worker.flavorCpu} CPU` : ''}{worker.flavorMem ? `, ${Math.round(worker.flavorMem)}GB mem` : ''}{worker.flavorDisk ? `, ${Math.round(worker.flavorDisk)}GB disk` : ''}{worker.region ? ` (${worker.region})` : ''}"
                >
                  {worker.name}
                </a>
                <!-- Phase I: upgrade-status badge. See specs/worker_autoupgrade.md. -->
                {#if worker.upgradeStatus === 'needs_upgrade'}
                  <span
                    class="worker-upgrade-badge worker-upgrade-stale"
                    title="Worker is running an older binary than the server.&#10;Worker: {worker.version || '?'} ({(worker.commit || '').slice(0,7) || '?'}) on {worker.buildArch || '?'}.&#10;Redeploy this worker to update."
                  >stale</span>
                {:else if worker.upgradeStatus === 'unsupported_arch'}
                  <span
                    class="worker-upgrade-badge worker-upgrade-arch"
                    title="Worker arch ({worker.buildArch || '?'}) is not the auto-upgrade target (linux/amd64). Upgrade is operator-initiated for this worker."
                  >arch</span>
                {:else if worker.upgradeStatus === 'unknown'}
                  <span
                    class="worker-upgrade-badge worker-upgrade-unknown"
                    title="Worker did not report build identity (likely a pre-Phase-I build). Will resolve once the worker restarts on a newer client."
                  >?</span>
                {/if}
                <!-- Phase II: pending operator-triggered upgrade. -->
                {#if worker.upgradeRequested === 'normal'}
                  <span
                    class="worker-upgrade-badge worker-upgrade-pending"
                    title="Upgrade pending — worker will swap binaries once it goes idle."
                  >upg pending</span>
                {:else if worker.upgradeRequested === 'emergency'}
                  <span
                    class="worker-upgrade-badge worker-upgrade-pending-emergency"
                    title="Emergency upgrade pending — worker is draining in-flight tasks, then will swap binaries (30 min hard cap)."
                  >upg drain</span>
                {/if}
                <!-- Worker-events badge: ⓘ normally, ⚠ when this worker
                     has been failing tasks (>=2 since last success) OR has
                     unread warning-level events (e.g. no-fit). Click to
                     open the events panel; the operator can ack from there. -->
                {#if (worker.recentFailures ?? 0) >= 2 || (worker.pendingWarnings ?? 0) > 0}
                  <button
                    type="button"
                    class="worker-events-badge worker-events-warn"
                    title="{worker.recentFailures ?? 0} failure(s) since last success, {worker.pendingWarnings ?? 0} unread warning(s). Click to view & acknowledge."
                    onclick={stopPropagation(() => openWorkerEvents(worker))}
                  >⚠</button>
                {:else}
                  <button
                    type="button"
                    class="worker-events-badge worker-events-info"
                    title="Worker events"
                    onclick={stopPropagation(() => openWorkerEvents(worker))}
                  >ⓘ</button>
                {/if}
              </td>
              <td class="workerCompo-wfstep">
  {#if editingWorkflowStepFor === worker.workerId}
    <!-- WORKFLOW SELECTOR -->
    <div class="step-edit-block">
      <label>
        Workflow:
        <input
          type="text"
          placeholder="search…"
          bind:value={workflowSearch}
          oninput={onWorkflowSearchInput}
          class="wf-search"
        />
        <select
          bind:value={selectedWorkflowId}
          onchange={() => loadStepsForWorkflow(selectedWorkflowId !== null && Number(selectedWorkflowId) > 0 ? Number(selectedWorkflowId) : null)}
        >
          <option value={null}>-- select workflow --</option>
          <option value={-1}>-- Clear --</option>
          {#each workflowOptions as wf}
            <option value={wf.workflowId}>{wf.name}</option>
          {/each}
        </select>
      </label>

      <button
        class="btn-action"
        onclick={loadWorkflows}
        disabled={loadingWorkflows || Number(selectedWorkflowId) === -1}
        title="Load more workflows"
      >+</button>

      <!-- STEP SELECTOR (hidden when "Clear" is selected) -->
      {#if Number(selectedWorkflowId) !== -1}
        <label>
          Step:
          <select bind:value={selectedStepId}>
            <option value={null}>-- select step --</option>
            {#each stepOptions as st}
              <option value={st.stepId}>{st.name}</option>
            {/each}
          </select>
        </label>
      {/if}

      <!-- SAVE / CANCEL -->
      <button
        class="btn-action"
        title="Save"
        onclick={async () => {
          if (Number(selectedWorkflowId) === -1) {
            await updateWorkerConfig(worker.workerId, { clearStep: true });
            internalWorkers = internalWorkers.map(w =>
              w.workerId === worker.workerId
                ? { ...w, stepId: undefined, stepName: undefined, workflowId: undefined, workflowName: undefined }
                : w
            );
            editingWorkflowStepFor = null;
            return;
          }
          const stepIdNum = selectedStepId !== null ? Number(selectedStepId) : null;
          const workflowIdNum = selectedWorkflowId !== null ? Number(selectedWorkflowId) : null;
          if (stepIdNum !== null) {
            await updateWorkerConfig(worker.workerId, { stepId: stepIdNum });
            internalWorkers = internalWorkers.map(w =>
              w.workerId === worker.workerId
                ? {
                    ...w,
                    stepId: stepIdNum,
                    stepName: stepOptions.find(s => s.stepId === stepIdNum)?.name,
                    workflowId: workflowIdNum,
                    workflowName: workflowOptions.find(wf => wf.workflowId === workflowIdNum)?.name
                  }
                : w
            );
          }
          editingWorkflowStepFor = null;
        }}
      >✓</button>

      <button
        class="btn-action"
        title="Cancel"
        onclick={() => {
          editingWorkflowStepFor = null;
          stepOptions = [];
        }}
      >✗</button>
    </div>
  {:else}
    <div class="wf-step-cell">
      <span class="wf-step-text" title="{worker.workflowName} › {worker.stepName}">
        {#if worker.workflowName}
          {worker.workflowName.length > 20
            ? `${worker.workflowName.slice(0, 20)}…`
            : worker.workflowName}
          › {worker.stepName}
        {:else}
          {worker.stepName}
        {/if}
      </span>

      <button
        class="btn-action"
        title="Edit workflow/step"
        onclick={() => {
          editingWorkflowStepFor = worker.workerId;
          selectedWorkflowId = worker.workflowId ?? null;
          selectedStepId = worker.stepId ?? null;
          workflowSearch = "";
          workflowOptions = [];
          workflowOffset = 0;
          loadWorkflows();
          if (selectedWorkflowId) loadStepsForWorkflow(selectedWorkflowId);
        }}
      ><Edit /></button>
    </div>
  {/if}
</td>
              <td>{worker.recyclableScope}</td>
              <td>
                <div class="workerCompo-status-pill {workerEffectiveStatusClass(worker)}" title={workerEffectiveStatusText(worker)}></div>
              </td>

              <td class="value-cell">
                <div class="value-with-controls">
                  <span class="value">{worker.concurrency}</span>
                  <div class="controls">
                    <button 
                      class="small-btn" 
                      onclick={() => updateValue(worker, 'concurrency', 1)}
                      data-testid={`increase-concurrency-${worker.workerId}`}
                    >+</button>
                    <button 
                      class="small-btn" 
                      onclick={() => updateValue(worker, 'concurrency', -1)}
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
                      onclick={() => updateValue(worker, 'prefetch', 1)}
                      data-testid={`increase-prefetch-${worker.workerId}`}
                    >+</button>
                    <button 
                      class="small-btn" 
                      onclick={() => updateValue(worker, 'prefetch', -1)}
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
                class="counter-cell {cellFlash[worker.workerId] === 'S' ? 'counter-cell-flash' : ''}"
                data-testid={`successful-tasks-${worker.workerId}`}
                title={`UploadingSuccess: ${tasksCount[worker.workerId]?.uploadingSuccess}, Succeeded: ${tasksCount[worker.workerId]?.succeeded}`}
              >
                {displayTasksCount(worker.workerId,'S')}
                {#if (tasksCount[worker.workerId]?.['S'] ?? 0) > 0}
                  <button
                    type="button"
                    class="counter-reset"
                    title="reset count"
                    aria-label="Reset success count"
                    onclick={stopPropagation(() => resetCellCounter(worker.workerId, 'S'))}
                  ></button>
                {/if}
              </td>

              <td
                class="counter-cell {cellFlash[worker.workerId] === 'F' ? 'counter-cell-flash' : ''}"
                data-testid={`failed-tasks-${worker.workerId}`}
                title={`UploadingFailure: ${tasksCount[worker.workerId]?.uploadingFailure}, Failed: ${tasksCount[worker.workerId]?.failed}`}
              >
                {displayTasksCount(worker.workerId,'F')}
                {#if (tasksCount[worker.workerId]?.['F'] ?? 0) > 0}
                  <button
                    type="button"
                    class="counter-reset"
                    title="reset count"
                    aria-label="Reset failure count"
                    onclick={stopPropagation(() => resetCellCounter(worker.workerId, 'F'))}
                  ></button>
                {/if}
              </td>

              <td
                data-testid={`inactive-tasks-${worker.workerId}`}
                title={`Waiting: ${tasksCount[worker.workerId]?.waiting}, Suspended: ${tasksCount[worker.workerId]?.suspended}, Canceled: ${tasksCount[worker.workerId]?.canceled}`}
              >
                {displayTasksCount(worker.workerId,'I','Z','X')}
              </td>

            <!-- Per-worker stats: table by default, charts when toggled -->
            {#if workersStatsMap[worker.workerId]}
              {#if workerDisplayMode[worker.workerId] !== 'charts'}
                <!-- Table mode -->
                <td title={`IOWait: ${workersStatsMap[worker.workerId]?.iowaitPercent?.toFixed(1) ?? 'N/A'}%\n` + (workersStatsMap[worker.workerId]?.numCpus > 0 ? `Load/CPU: ${((workersStatsMap[worker.workerId]?.load1Min ?? 0) / workersStatsMap[worker.workerId].numCpus * 100).toFixed(0)}% (${workersStatsMap[worker.workerId].numCpus} CPUs)` : `Load: ${workersStatsMap[worker.workerId]?.load1Min?.toFixed(1) ?? 'N/A'}`)}>
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
                {#if workerAdvancedMetrics[worker.workerId]}
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

                          <!-- Load per CPU -->
                          <div class="metric-chart">
                            {#if workersStatsMap[worker.workerId].numCpus > 0}
                              <div class="chart-title">Load/CPU: {((workersStatsMap[worker.workerId].load1Min ?? 0) / workersStatsMap[worker.workerId].numCpus * 100).toFixed(0)}%</div>
                              <div class="chart-bar-container">
                                <div class="chart-bar" style={`width: ${Math.min(100, (workersStatsMap[worker.workerId].load1Min ?? 0) / workersStatsMap[worker.workerId].numCpus * 100)}%`}></div>
                              </div>
                            {:else}
                              <div class="chart-title">Load: {workersStatsMap[worker.workerId].load1Min?.toFixed(1) ?? 0}</div>
                              <div class="chart-bar-container">
                                <div class="chart-bar" style={`width: ${Math.min(100, (workersStatsMap[worker.workerId].load1Min ?? 0) * 10)}%`}></div>
                              </div>
                            {/if}
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
                          {#key diskHistories[worker.workerId]}
                            {@const diskChartData = prepareChartData(diskHistories[worker.workerId] || [], 'disk')}
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
                              zoomIntensity={diskAutoZoom ? diskZoom / 5 : 1}
                            />
                          {/key}
                        {:else}
                          <div class="no-data">No disk data</div>
                        {/if}
                      </div>

                      <div class="io-chart-container" data-testid={`I/O-chart-${worker.workerId}`} >
                        <div class="chart-title">Network I/O</div>
                        {#if workersStatsMap[worker.workerId]?.netIo}
                          {#key networkHistories[worker.workerId]}
                            {@const networkChartData = prepareChartData(networkHistories[worker.workerId] || [], 'network')}
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
                              zoomIntensity={networkAutoZoom ? networkZoom / 5 : 1}
                            />
                          {/key}
                        {:else}
                          <div class="no-data">No network data</div>
                        {/if}
                      </div>
                    </div>
                  </div>
                </td>
              {/if}
            {:else}
              <td colspan="3">No statistics available</td>
            {/if}

              <td class="workerCompo-actions">
                <div class="action-row">
                  <button
                    class="btn-action"
                    title={showPlayIcon(worker)
                      ? (isConcurrencyZeroIdle(worker)
                          ? 'Start (concurrency is 0 — will prompt for a value)'
                          : 'Resume')
                      : 'Pause'}
                    onclick={() => pauseWorker(worker)}
                    disabled={acting.has(worker.workerId)}
                    data-testid={`pause-worker-${worker.workerId}`}
                  >
                    {#if showPlayIcon(worker)}
                      <PlayCircle />
                    {:else}
                      <PauseCircle />
                    {/if}
                  </button>
                  <button class="btn-action" title="Clean"><Eraser /></button>
                </div>
                <div class="action-row">
                  <button class="btn-action" title="Restart"><RefreshCw /></button>
                  <button class="btn-action" title="Delete" onclick={() => { deleteWorker(worker.workerId); }} data-testid={`delete-worker-${worker.workerId}`} disabled={acting.has(worker.workerId)}>
                    <Trash />
                  </button>
                  <button
                    class="btn-action"
                    class:btn-permanent-active={worker.isPermanent}
                    title={worker.isPermanent ? 'Unset permanent' : 'Set permanent'}
                    onclick={async () => {
                      await updateWorkerConfig(worker.workerId, { isPermanent: !worker.isPermanent });
                      worker.isPermanent = !worker.isPermanent;
                    }}
                  >
                    <Star />
                  </button>
                  {#if worker.upgradeStatus !== 'unsupported_arch'}
                    <button
                      class="btn-action"
                      class:btn-upgrade-pending={worker.upgradeRequested === 'normal'}
                      class:btn-upgrade-emergency={worker.upgradeRequested === 'emergency'}
                      title={worker.upgradeRequested
                        ? `Upgrade pending (${worker.upgradeRequested}) — click to cancel`
                        : 'Upgrade worker (click=normal idle-wait, shift-click=emergency drain)'}
                      onclick={(ev) => upgradeWorker(worker, ev)}
                      disabled={acting.has(worker.workerId)}
                      data-testid={`upgrade-worker-${worker.workerId}`}
                    >
                      <ArrowUpCircle />
                    </button>
                  {/if}
                </div>
                <div class="action-row">
                    {#if workerDisplayMode[worker.workerId] === 'charts'}
                      <button data-testid={`table-worker-${worker.workerId}`} class="btn-action" onclick={() => { workerDisplayMode[worker.workerId] = 'table'; workerDisplayMode = workerDisplayMode; }} title="Numbers">
                        <FileDigit />
                      </button>
                    {:else}
                      <button data-testid={`charts-worker-${worker.workerId}`} class="btn-action" onclick={() => { workerDisplayMode[worker.workerId] = 'charts'; workerDisplayMode = workerDisplayMode; }} title="Charts">
                        <BarChart/>
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

<!-- Worker-events viewer (opened from the ℹ︎/⚠ badge in the worker name cell). -->
{#if eventsWorker}
  <div class="worker-events-overlay" onclick={closeWorkerEvents} role="presentation">
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
    <!-- The on:click here is purely containment: it stops the overlay click
         (which would close the dialog) from bubbling when the user clicks
         INSIDE the panel. It's not a user action — no keyboard equivalent
         needed. tabindex="-1" lets the dialog be focused programmatically
         (e.g. for screen-reader announce) without being in the tab order. -->
    <div class="worker-events-panel" onclick={stopPropagation(bubble('click'))} role="dialog" aria-modal="true" tabindex="-1">
      <div class="worker-events-header">
        <span>
          Events — <strong>{eventsWorker.name}</strong>
          {#if (eventsWorker.recentFailures ?? 0) >= 2}
            <span class="worker-events-warn-text">⚠ {eventsWorker.recentFailures} failures since last success</span>
          {/if}
          {#if (eventsWorker.pendingWarnings ?? 0) > 0}
            <span class="worker-events-warn-text">⚠ {eventsWorker.pendingWarnings} unread warning{(eventsWorker.pendingWarnings ?? 0) > 1 ? 's' : ''}</span>
          {/if}
        </span>
        <span class="worker-events-actions">
          {#if (eventsWorker.pendingWarnings ?? 0) > 0}
            <button
              type="button"
              class="worker-events-ack"
              disabled={eventsBusy}
              title="Acknowledge all unread warnings on this worker"
              onclick={() => handleClearWarnings()}
            >Clear warnings</button>
          {/if}
          <button type="button" class="worker-events-close" onclick={closeWorkerEvents} title="Close">✕</button>
        </span>
      </div>
      {#if eventsStatus}
        <div class="worker-events-status">{eventsStatus}</div>
      {/if}
      <div class="worker-events-body">
        {#if eventsLoading}
          <p class="worker-events-empty">Loading…</p>
        {:else if workerEvents.length === 0}
          <p class="worker-events-empty">No events recorded for this worker.</p>
        {:else}
          {#each workerEvents as ev}
            <div class="worker-events-row worker-events-level-{ev.level}">
              <span class="worker-events-time">{ev.createdAt}</span>
              <span class="worker-events-lvl">{ev.level}</span>
              <span class="worker-events-class">{ev.eventClass}</span>
              <span class="worker-events-msg">{ev.message}</span>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if raiseConcModalWorker}
  <!-- Raise-concurrency modal. Opened when the operator clicks
       play/pause on a worker with concurrency=0. Without raising the
       value first, the scheduler would keep skipping the worker
       (the assignment SQL's HAVING SUM < concurrency+prefetch
       clause excludes it silently). See ROADMAP entry
       "concurrency=0 worker visualisation" for the rationale on
       handling this in the UI rather than auto-flipping status
       server-side. -->
  <div
    class="raise-conc-modal-backdrop"
    onclick={closeRaiseConcurrencyModal}
    onkeydown={(e) => { if (e.key === 'Escape') closeRaiseConcurrencyModal(); }}
    role="presentation"
  >
    <div
      class="raise-conc-modal"
      onclick={stopPropagation(bubble('click'))}
      onkeydown={(e) => { if (e.key === 'Escape') closeRaiseConcurrencyModal(); }}
      role="dialog"
      aria-modal="true"
      aria-labelledby="raise-conc-title"
      tabindex="-1"
    >
      <h3 id="raise-conc-title">Set concurrency for {raiseConcModalWorker.name}</h3>
      <p class="raise-conc-hint">
        This worker has <strong>concurrency 0</strong>, so the scheduler
        skips it. Set a positive value to let it pick up tasks.
        {#if raiseConcModalWorker.status === 'P'}
          The worker is also paused — confirming will both raise
          concurrency and resume it.
        {/if}
      </p>
      <label class="raise-conc-field">
        Concurrency
        <input
          type="number"
          min="1"
          bind:value={raiseConcModalValue}
          data-testid="raise-conc-input"
        />
      </label>
      <div class="raise-conc-actions">
        <button onclick={closeRaiseConcurrencyModal}>Cancel</button>
        <button
          class="raise-conc-confirm"
          onclick={confirmRaiseConcurrency}
          data-testid="raise-conc-confirm"
        >
          {raiseConcModalWorker.status === 'P' ? 'Set & resume' : 'Set concurrency'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .raise-conc-modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }
  .raise-conc-modal {
    background: var(--background, #222);
    color: var(--text, #eee);
    padding: 1.25rem 1.5rem;
    border-radius: 6px;
    min-width: 320px;
    max-width: 480px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
  }
  .raise-conc-modal h3 {
    margin: 0 0 0.75rem 0;
    font-size: 1.05rem;
  }
  .raise-conc-hint {
    margin: 0 0 1rem 0;
    font-size: 0.9rem;
    opacity: 0.85;
  }
  .raise-conc-field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    margin-bottom: 1rem;
  }
  .raise-conc-field input {
    padding: 0.4rem 0.5rem;
    font-size: 1rem;
    border-radius: 4px;
    border: 1px solid rgba(255, 255, 255, 0.2);
    background: rgba(0, 0, 0, 0.25);
    color: inherit;
  }
  .raise-conc-actions {
    display: flex;
    gap: 0.5rem;
    justify-content: flex-end;
  }
  .raise-conc-actions button {
    padding: 0.4rem 0.85rem;
    border-radius: 4px;
    cursor: pointer;
  }
  .raise-conc-confirm {
    background: #2c5cdd;
    color: white;
    border: 1px solid #1f48b8;
  }
</style>
