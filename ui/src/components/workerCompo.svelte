<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { getWorkerStatusClass, getWorkerStatusText, getStats, formatBytesPair, getTasksCount, getStatus } from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';

  export let workers = [];
  let displayWorkers = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};
  let tasksCount: Record<number, Record<string, number>> = {};
  let interval;
  let tasksAllCount;
  let hasLoaded = false;


  export let onWorkerUpdated: (event: { detail: { workerId: number; updates: Partial<Worker> } }) => void = () => {};
  export let onWorkerDeleted: (event: { detail: { workerId: number } }) => void = () => {};

  onMount(() => {
    
    updateWorkerData();
    interval = setInterval(() => {
      updateWorkerData();
    }, 5000);

    return () => clearInterval(interval);
  });

  /**
   * Reactive statement to trigger initial data update when
   * workers list becomes non-empty and data hasn't been loaded yet.
   * Ensures updateWorkerData() is called only once after workers load.
   */
  $: if (workers.length > 0 && !hasLoaded) {
    updateWorkerData();
    hasLoaded = true;
  }

  // Reactively enrich workers with status (without mutating the original `workers` prop)
  $: displayWorkers = workers.map(worker => ({
    ...worker,
    status: statusMap.get(worker.workerId) ?? 'unknown'
  }));

  let statusMap = new Map<number, string>();

  /**
   * Fetches and updates worker statistics, statuses, and tasks counts.
   * Updates internal state maps with the latest data.
   * Does nothing if the workers array is empty.
   * @async
   * @returns {Promise<void>}
   */
  async function updateWorkerData() {
    if (workers.length === 0) return;

    const workerIds = workers.map(w => w.workerId);
    try {
      
      workersStatsMap = await getStats(workerIds);

      const statuses = await getStatus(workerIds);
      statusMap = new Map(statuses.map(s => [s.workerId, s.status]));

      for (const id of workerIds) {
        tasksCount[id] = await getTasksCount(id);
      }
      tasksAllCount = await getTasksCount();
    } catch (err) {
      console.error('Error chargement data:', err);
    }
  }


  /**
   * Updates a numeric property of a worker (either 'concurrency' or 'prefetch') by a delta.
   * Ensures the new value is not less than zero.
   * Updates the worker locally and triggers the onWorkerUpdated callback if changes occur.
   * @param {Worker} worker - The worker object to update.
   * @param {'concurrency' | 'prefetch'} field - The property to update.
   * @param {number} delta - The amount to increment or decrement the property by.
   * @async
   * @returns {Promise<void>}
   */
  async function updateValue(worker, field: 'concurrency' | 'prefetch', delta: number) {
    const newValue = Math.max(0, worker[field] + delta);

    const updates: Partial<Worker> = {};
    if (newValue !== worker[field]) {
      updates[field] = newValue;
      worker[field] = newValue; // Update locally
    }

    if (Object.keys(updates).length > 0) {
      onWorkerUpdated({
        detail: {
          workerId: worker.workerId,
          updates
        }
      });
    }
  }

  /**
   * Deletes a worker by triggering the onWorkerDeleted callback.
   * Also removes the worker’s stats and tasks count from local state maps.
   * @param {number} workerId - The ID of the worker to delete.
   * @returns {void}
   */
  async function deleteWorker(workerId: number) {
    onWorkerDeleted({
      detail: { workerId }
    });
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
    <a href="#/tasks?status=W">Waiting: {tasksAllCount?.waiting ?? 0}</a>
    <a href="#/tasks?status=R">Running: {tasksAllCount?.running ?? 0}</a>
    <a href="#/tasks?status=U">Uploading (AS): {tasksAllCount?.uploadingSuccess ?? 0}</a>
    <a href="#/tasks?status=S">Succeeded: {tasksAllCount?.succeeded ?? 0}</a>
    <a href="#/tasks?status=V">Uploading (AF): {tasksAllCount?.uploadingFailure ?? 0}</a>
    <a href="#/tasks?status=F">Failed: {tasksAllCount?.failed ?? 0}</a>
    <a href="#/tasks?status=Z">Suspended: {tasksAllCount?.suspended ?? 0}</a>
    <a href="#/tasks?status=X">Canceled: {tasksAllCount?.canceled ?? 0}</a>
  </div>

  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>Name</th>
          <th>Wf.Step</th>
          <th>Status</th>
          <th>Concurrency</th>
          <th>Prefetch</th>
          <th>Starting</th>
          <th>Progress</th>
          <th>Successes</th>
          <th>Fails</th>
          <th>CPU%</th>
          <th>Mem%</th>
          <th>Load</th>
          <th>IOWait</th>
          <th>Disk Usage%</th>
          <th>Disk R/W</th>
          <th>Network Sent/Recv</th>
          <th>Actions</th>
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
              title={`Downloading: ${tasksCount[worker.workerId]?.downloading}, Waiting: ${tasksCount[worker.workerId]?.waiting}, Running: ${tasksCount[worker.workerId]?.running}`}
            >
              {(tasksCount[worker.workerId]?.downloading ?? 0)
              + (tasksCount[worker.workerId]?.waiting ?? 0)
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
              title={`UploadingFailure: ${tasksCount[worker.workerId]?.uploadingFailure}, Failed: ${tasksCount[worker.workerId]?.failed}, Suspended: ${tasksCount[worker.workerId]?.suspended}, Canceled: ${tasksCount[worker.workerId]?.canceled}`}
            >
              {(tasksCount[worker.workerId]?.uploadingFailure ?? 0) 
              + (tasksCount[worker.workerId]?.failed ?? 0) 
              + (tasksCount[worker.workerId]?.suspended ?? 0) 
              + (tasksCount[worker.workerId]?.canceled ?? 0)}
            </td>

            {#if workersStatsMap[worker.workerId]}
              <td>{workersStatsMap[worker.workerId]?.cpuUsagePercent != null ? workersStatsMap[worker.workerId].cpuUsagePercent.toFixed(1) + '%' : 'N/A'}</td>
              <td>{workersStatsMap[worker.workerId]?.memUsagePercent != null ? workersStatsMap[worker.workerId].memUsagePercent.toFixed(1) + '%' : 'N/A'}</td>
              <td>{workersStatsMap[worker.workerId]?.load1Min != null ? workersStatsMap[worker.workerId].load1Min.toFixed(1) : 'N/A'}</td>
              <td>{workersStatsMap[worker.workerId]?.iowaitPercent != null ? workersStatsMap[worker.workerId].iowaitPercent.toFixed(1) + '%' : 'N/A'}</td>
              <td>
                {#each workersStatsMap[worker.workerId].disks as disk (disk.deviceName)}
                  <div>{disk.deviceName}: {disk.usagePercent.toFixed(1)}%</div>
                {/each}
              </td>
              <td>
                {#if workersStatsMap[worker.workerId].diskIo}
                  <div>
                    {formatBytesPair(
                      workersStatsMap[worker.workerId].diskIo.readBytesRate,
                      workersStatsMap[worker.workerId].diskIo.writeBytesRate
                    )}/s
                  </div>
                  <div>
                    {formatBytesPair(
                      workersStatsMap[worker.workerId].diskIo.readBytesTotal,
                      workersStatsMap[worker.workerId].diskIo.writeBytesTotal
                    )}
                  </div>
                {:else}
                  <div>N/A</div>
                {/if}
              </td>
              <td>
                {#if workersStatsMap[worker.workerId].netIo}
                  <div>
                    {formatBytesPair(
                      workersStatsMap[worker.workerId].netIo.sentBytesRate,
                      workersStatsMap[worker.workerId].netIo.recvBytesRate
                    )}/s
                  </div>
                  <div>
                    {formatBytesPair(
                      workersStatsMap[worker.workerId].netIo.sentBytesTotal,
                      workersStatsMap[worker.workerId].netIo.recvBytesTotal
                    )}
                  </div>
                {:else}
                  <div>N/A</div>
                {/if}
              </td>
            {:else}
              <td colspan="7">No statistics available for this worker.</td>
            {/if}

            <td class="workerCompo-actions">
              <button class="btn-action" title="Pause"><PauseCircle /></button>
              <button class="btn-action" title="Clean"><Eraser /></button>
              <br />
              <button class="btn-action" title="Restart"><RefreshCw /></button>
              <button class="btn-action" title="Delete" on:click={() => { deleteWorker(worker.workerId); }} data-testid={`delete-worker-${worker.workerId}`}><Trash /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p class="workerCompo-empty-state">No workers found.</p>
{/if}
