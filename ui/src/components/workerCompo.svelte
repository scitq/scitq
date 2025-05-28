<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import {delWorker, updateWorkerConfig, getWorkerStatusClass, getWorkerStatusText, getStats, formatBytesPair, getTasks, getStatus} from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import { createEventDispatcher } from 'svelte';
  import '../styles/worker.css';

  export let workers = [];
  let displayWorkers = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};
  let tasksStatus: Record<number, Record<string, number>> = {};
  let interval;

  export let onWorkerUpdated: (event: { detail: { workerId: number; updates: Partial<Worker> } }) => void = () => {};

  onMount(() => {
    updateWorkerData();

    interval = setInterval(() => {
      updateWorkerData();
    }, 5000);

    return () => clearInterval(interval);
  });

  $: console.log('Workers list:', workers.map(w => w.workerId));

  // Reactively enrich workers with status (without mutating the original `workers` prop)
  $: displayWorkers = workers.map(worker => ({
    ...worker,
    status: statusMap.get(worker.workerId) ?? 'unknown'
  }));

  let statusMap = new Map<number, string>();

  async function updateWorkerData() {
    if (workers.length === 0) return;

    const workerIds = workers.map(w => w.workerId);

    // Fetch worker stats
    workersStatsMap = await getStats(workerIds);

    // Fetch worker statuses
    const statuses = await getStatus(workerIds);
    statusMap = new Map(statuses.map(s => [s.workerId, s.status]));

    // Fetch tasks for each worker
    for (const id of workerIds) {
      tasksStatus[id] = {
        pending: await getTasks(id).pending(),
        assigned: await getTasks(id).assigned(),
        accepted: await getTasks(id).accepted(),
        downloading: await getTasks(id).downloading(),
        running: await getTasks(id).running(),
        uploadingSuccess: await getTasks(id).uploadingSuccess(),
        succeeded: await getTasks(id).succeeded(),
        uploadingFailure: await getTasks(id).uploadingFailure(),
        failed: await getTasks(id).failed(),
        suspended: await getTasks(id).suspended(),
        canceled: await getTasks(id).canceled(),
        waiting: await getTasks(id).waiting(),
      };
    }
  }

  async function updateValue(worker, field: 'concurrency' | 'prefetch', delta: number) {
    const newValue = Math.max(0, worker[field] + delta);
    
    // Prepare only the modified fields
    const updates: Partial<Worker> = {};
    if (newValue !== worker[field]) {
      updates[field] = newValue;
      worker[field] = newValue; // Update locally
    }

    // Send update only if there were changes
    if (Object.keys(updates).length > 0) {
      onWorkerUpdated({
        detail: {
          workerId: worker.workerId,
          updates
        }
      });
    }
  }


  async function deleteWorker(workerId: number) {
    workers = workers.filter(worker => worker.workerId !== workerId);
    await delWorker({ workerId });
    delete workersStatsMap[workerId];
    delete tasksStatus[workerId];
  }
</script>


{#if workers && workers.length > 0}
  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>Name</th>
          <th>Batch</th>
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
            <td class="workerCompo-clickable">{worker.name}</td>
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
              title={`Pending: ${tasksStatus[worker.workerId]?.pending}, Assigned: ${tasksStatus[worker.workerId]?.assigned}, Accepted: ${tasksStatus[worker.workerId]?.accepted}`}
            >
              {(tasksStatus[worker.workerId]?.pending ?? 0)
              + (tasksStatus[worker.workerId]?.assigned ?? 0)
              + (tasksStatus[worker.workerId]?.accepted ?? 0)}
            </td>
            <td
              data-testid={`tasks-in-progress-${worker.workerId}`}
              title={`Downloading: ${tasksStatus[worker.workerId]?.downloading}, Waiting: ${tasksStatus[worker.workerId]?.waiting}, Running: ${tasksStatus[worker.workerId]?.running}`}
            >
              {(tasksStatus[worker.workerId]?.downloading ?? 0)
              + (tasksStatus[worker.workerId]?.waiting ?? 0)
              + (tasksStatus[worker.workerId]?.running ?? 0)}
            </td>

            <td
              data-testid={`successful-tasks-${worker.workerId}`}
              title={`UploadingSuccess: ${tasksStatus[worker.workerId]?.uploadingSuccess}, Succeeded: ${tasksStatus[worker.workerId]?.succeeded}`}
            >
              {(tasksStatus[worker.workerId]?.uploadingSuccess ?? 0) + (tasksStatus[worker.workerId]?.succeeded ?? 0)}
            </td>

            <td
              data-testid={`failed-tasks-${worker.workerId}`}
              title={`UploadingFailure: ${tasksStatus[worker.workerId]?.uploadingFailure}, Failed: ${tasksStatus[worker.workerId]?.failed}, Suspended: ${tasksStatus[worker.workerId]?.suspended}, Canceled: ${tasksStatus[worker.workerId]?.canceled}`}
            >
              {(tasksStatus[worker.workerId]?.uploadingFailure ?? 0) 
              + (tasksStatus[worker.workerId]?.failed ?? 0) 
              + (tasksStatus[worker.workerId]?.suspended ?? 0) 
              + (tasksStatus[worker.workerId]?.canceled ?? 0)}
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
