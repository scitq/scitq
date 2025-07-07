<script lang="ts">
  import { onMount } from 'svelte';
  import {getWorkerStatusClass, getWorkerStatusText, getStats, formatBytesPair, getTasksCount, getStatus} from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser, BarChart, FileDigit } from 'lucide-svelte';
  import LineChart from './LineChart.svelte';
  import '../styles/worker.css';

  export let workers = [];
  export let onWorkerUpdated: (event: { detail: { workerId: number; updates: Partial<Worker> } }) => void = () => {};
  export let onWorkerDeleted: (event: { detail: { workerId: number } }) => void = () => {};

  // State management
  let displayWorkers = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};
  let tasksCount: Record<number, Record<string, number>> = {};
  let tasksAllCount;
  let statusMap = new Map<number, string>();
  let displayMode: 'table' | 'charts' = 'table';
  let hasLoaded = false;
  let interval;

  // Chart data
  const MAX_HISTORY = 30;
  let diskHistory: {time: Date, read: number, write: number}[] = [];
  let networkHistory: {time: Date, sent: number, received: number}[] = [];
  let diskChartData = { line1: [], line2: [] };
  let networkChartData = { line1: [], line2: [] };
  
  // Zoom controls
  let diskZoom = 1;
  let networkZoom = 1;
  let diskAutoZoom = true;
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

  // Make reactive
  $: diskZoom, networkZoom, diskAutoZoom, networkAutoZoom;

  // Lifecycle hooks
  onMount(() => {
    updateWorkerData();
    interval = setInterval(updateWorkerData, 5000);
    return () => clearInterval(interval);
  });

  // Reactive statements
  $: if (workers.length > 0 && !hasLoaded) {
    updateWorkerData();
    hasLoaded = true;
  }

  $: displayWorkers = workers.map(worker => ({
    ...worker,
    status: statusMap.get(worker.workerId) ?? 'unknown'
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

      // Update status and tasks
      statusMap = new Map((await getStatus(workerIds)).map(s => [s.workerId, s.status]));
      
      for (const id of workerIds) {
        tasksCount[id] = await getTasksCount(id);
      }
      tasksAllCount = await getTasksCount();

    } catch (err) {
      console.error('Error loading data:', err);
    }
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
   * Deletes a worker and triggers delete event
   * @param {number} workerId - ID of worker to delete
   */
  async function deleteWorker(workerId: number) {
    onWorkerDeleted({ detail: { workerId } });
    delete workersStatsMap[workerId];
    delete tasksCount[workerId];
  }
</script>

{#if workers && workers.length > 0}

<div class="status-count-bar status-tabs">
    <a href="#/tasks"> All: {tasksAllCount?.all ?? 0}</a>
    <a href="#/tasks?status=P" data-testid="pending-link-dashboard"> Pending: {tasksAllCount?.pending ?? 0}</a>
    <a href="#/tasks?status=A"> Assigned: {tasksAllCount?.assigned ?? 0}</a>
    <a href="#/tasks?status=C">Accepted: {tasksAllCount?.accepted ?? 0}</a>
    <a href="#/tasks?status=D">Downloading: {tasksAllCount?.downloading ?? 0}</a>
    <a href="#/tasks?status=R">Running: {tasksAllCount?.running ?? 0}</a>
    <a href="#/tasks?status=U">Uploading (AS): {tasksAllCount?.uploadingSuccess ?? 0}</a>
    <a href="#/tasks?status=S">Succeeded: {tasksAllCount?.succeeded ?? 0}</a>
    <a href="#/tasks?status=V">Uploading (AF): {tasksAllCount?.uploadingFailure ?? 0}</a>
    <a href="#/tasks?status=F">Failed: {tasksAllCount?.failed ?? 0}</a>
    <a href="#/tasks?status=W">Waiting: {tasksAllCount?.waiting ?? 0}</a>
    <a href="#/tasks?status=Z">Suspended: {tasksAllCount?.suspended ?? 0}</a>
    <a href="#/tasks?status=X">Canceled: {tasksAllCount?.canceled ?? 0}</a>
  </div>

  <div class="workerCompo-table-wrapper">
    <table class="listTable workerCompo-table">
      <thead>
        <tr>
          {#if displayMode === 'table'}
            <!-- Table mode - show all columns -->
            <th>Name</th>
            <th>Wf.Step</th>
            <th>Status</th>
            <th>Concurrency</th>
            <th>Prefetch</th>
            <th>Starting</th>
            <th>Progress</th>
            <th>Success</th>
            <th>Fail</th>
            <th>Inactive</th>
            <th>CPU%</th>
            <th>Mem%</th>
            <th>Load</th>
            <th>IOWait</th>
            <th>Disk Usage%</th>
            <th>Disk R/W</th>
            <th>Network Sent/Recv</th>
            <th>Actions</th>
          {:else}
            <th>Name</th>
            <th>Wf.Step</th>
            <th>Status</th>
            <th>Concurrency</th>
            <th>Prefetch</th>
            <th>Starting</th>
            <th>Progress</th>
            <th>Success</th>
            <th>Fail</th>
            <th>Inactive</th>
            <th colspan="7">Metrics</th>
            <th>Actions</th>
          {/if}
        </tr>
      </thead>
      <tbody>
        {#each workers as worker (worker.workerId)}
          <tr data-testid={`worker-${worker.workerId}`}>
            <td> <a href="#/tasks?workerId={worker.workerId}" class="workerCompo-clickable">{worker.name}</a> </td>
            <td class="workerCompo-actions">
              <button class="btn-action" title="Edit"><Edit /></button>
            </td>
            <td>
              <div class="workerCompo-status-pill {getWorkerStatusClass(worker.status)}" title={getWorkerStatusText(worker.status)}></div>
            </td>

            <td>
              <button class="small-btn" data-testid={`decrease-concurrency-${worker.workerId}`} on:click={() => updateValue(worker, 'concurrency', -1)}>-</button>
              {worker.concurrency}
              <button class="small-btn" data-testid={`increase-concurrency-${worker.workerId}`} on:click={() => updateValue(worker, 'concurrency', 1)}>+</button>
            </td>

            <td>
              <button class="small-btn" data-testid={`decrease-prefetch-${worker.workerId}`} on:click={() => updateValue(worker, 'prefetch', -1)}>-</button>
              {worker.prefetch}
              <button class="small-btn" data-testid={`increase-prefetch-${worker.workerId}`} on:click={() => updateValue(worker, 'prefetch', 1)}>+</button>
            </td>

            <td
              data-testid={`tasks-awaiting-execution-${worker.workerId}`}
              title={`Pending: ${tasksCount[worker.workerId]?.pending}, Assigned: ${tasksCount[worker.workerId]?.assigned}, Accepted: ${tasksCount[worker.workerId]?.accepted}`}
            >
              {(tasksCount[worker.workerId]?.pending ?? 0)
              + (tasksCount[worker.workerId]?.assigned ?? 0)
              + (tasksCount[worker.workerId]?.accepted ?? 0)}
            </td>
            <td
              data-testid={`tasks-in-progress-${worker.workerId}`}
              title={`Downloading: ${tasksCount[worker.workerId]?.downloading}, Running: ${tasksCount[worker.workerId]?.running}`}
            >
              {(tasksCount[worker.workerId]?.downloading ?? 0)
              + (tasksCount[worker.workerId]?.running ?? 0)}
            </td>

            <td
              data-testid={`successful-tasks-${worker.workerId}`}
              title={`UploadingSuccess: ${tasksCount[worker.workerId]?.uploadingSuccess}, Succeeded: ${tasksCount[worker.workerId]?.succeeded}`}
            >
              {(tasksCount[worker.workerId]?.uploadingSuccess ?? 0) + (tasksCount[worker.workerId]?.succeeded ?? 0)}
            </td>

            <td
              data-testid={`failed-tasks-${worker.workerId}`}
              title={`UploadingFailure: ${tasksCount[worker.workerId]?.uploadingFailure}, Failed: ${tasksCount[worker.workerId]?.failed}`}
            >
              {(tasksCount[worker.workerId]?.uploadingFailure ?? 0) 
              + (tasksCount[worker.workerId]?.failed ?? 0)}
            </td>

            <td
              data-testid={`inactive-tasks-${worker.workerId}`}
              title={`Waiting: ${tasksCount[worker.workerId]?.waiting}, Suspended: ${tasksCount[worker.workerId]?.suspended}, Canceled: ${tasksCount[worker.workerId]?.canceled}`}
            >
              {(tasksCount[worker.workerId]?.waiting ?? 0) 
              + (tasksCount[worker.workerId]?.suspended ?? 0) 
              + (tasksCount[worker.workerId]?.canceled ?? 0)}
            </td>

          <!-- Conditional columns based on display mode -->
          {#if workersStatsMap[worker.workerId]}
            {#if displayMode === 'table'}
              <!-- Table mode -->
              <td>{workersStatsMap[worker.workerId]?.cpuUsagePercent?.toFixed(1) ?? 'N/A'}%</td>
              <td>{workersStatsMap[worker.workerId]?.memUsagePercent?.toFixed(1) ?? 'N/A'}%</td>
              <td>{workersStatsMap[worker.workerId]?.load1Min?.toFixed(1) ?? 'N/A'}</td>
              <td>{workersStatsMap[worker.workerId]?.iowaitPercent?.toFixed(1) ?? 'N/A'}%</td>
              <td>
                {#each workersStatsMap[worker.workerId].disks as disk (disk.deviceName)}
                  <div>{disk.deviceName}: {disk.usagePercent.toFixed(1)}%</div>
                {/each}
              </td>
              <td>
                {#if workersStatsMap[worker.workerId].diskIo}
                  <div>{formatBytesPair(workersStatsMap[worker.workerId].diskIo.readBytesRate, workersStatsMap[worker.workerId].diskIo.writeBytesRate)}/s</div>
                  <div>{formatBytesPair(workersStatsMap[worker.workerId].diskIo.readBytesTotal, workersStatsMap[worker.workerId].diskIo.writeBytesTotal)}</div>
                {:else}
                  <div>N/A</div>
                {/if}
              </td>
              <td>
                {#if workersStatsMap[worker.workerId].netIo}
                  <div>{formatBytesPair(workersStatsMap[worker.workerId].netIo.sentBytesRate, workersStatsMap[worker.workerId].netIo.recvBytesRate)}/s</div>
                  <div>{formatBytesPair(workersStatsMap[worker.workerId].netIo.sentBytesTotal, workersStatsMap[worker.workerId].netIo.recvBytesTotal)}</div>
                {:else}
                  <div>N/A</div>
                {/if}
              </td>
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
                          <div class="disk-bars">
                            {#each workersStatsMap[worker.workerId].disks as disk, i (disk.deviceName)}
                              <div class="disk-bar-container">
                                <div class="disk-info">
                                  <span class="disk-name">{disk.deviceName}</span>
                                  <span class="disk-percent">{disk.usagePercent.toFixed(1)}%</span>
                                </div>
                                <div class="disk-bar-background">
                                  <div class="disk-bar-fill {disk.usagePercent > 80 ? 'warning' : ''}" 
                                       style={`width: ${disk.usagePercent}%`}></div>
                                </div>
                              </div>
                            {/each}
                          </div>
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
            <td colspan="7">No statistics available</td>
          {/if}

            <td class="workerCompo-actions">
              <div class="action-row">
                <button class="btn-action" title="Pause"><PauseCircle /></button>
                <button class="btn-action" title="Clean"><Eraser /></button>
              </div>
              <div class="action-row">
                <button class="btn-action" title="Restart"><RefreshCw /></button>
                <button class="btn-action" title="Delete" on:click={() => { deleteWorker(worker.workerId); }} data-testid={`delete-worker-${worker.workerId}`}><Trash /></button>
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
                {/if}
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p class="workerCompo-empty-state">No workers found.</p>
{/if}