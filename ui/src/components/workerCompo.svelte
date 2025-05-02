<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { delWorker, getWorkers, updateWorkerConfig, getStatusClass, getStatusText, getStats, formatBytesPair, getTasks} from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';

  let workers: Worker[] = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};

  let tasksStatus: Record<number, Record<string, number>> = {};

  onMount(async () => {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    // Retrieve worker statistics
    workersStatsMap = await getStats(workerIds);  // Call to the new getStats function

    // Charger le nombre de tâches par worker
    for (const worker of workers) {
      tasksStatus[worker.workerId] = {
        pending: await getTasks(worker.workerId).pending(),
        assigned: await getTasks(worker.workerId).assigned(),
        accepted: await getTasks(worker.workerId).accepted(),
        downloading: await getTasks(worker.workerId).downloading(),
        running: await getTasks(worker.workerId).running(),
        uploadingSuccess: await getTasks(worker.workerId).uploadingSuccess(),
        succeeded: await getTasks(worker.workerId).succeeded(),
        uploadingFailure: await getTasks(worker.workerId).uploadingFailure(),
        failed: await getTasks(worker.workerId).failed(),
        suspended: await getTasks(worker.workerId).suspended(),
        canceled: await getTasks(worker.workerId).canceled(),
        waiting: await getTasks(worker.workerId).waiting(),
      };
    }
  });


  export async function refresh() {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    workersStatsMap = await getStats(workerIds);

    // Charger le nombre de tâches par worker
    for (const worker of workers) {
      tasksStatus[worker.workerId] = {
        pending: await getTasks(worker.workerId).pending(),
        assigned: await getTasks(worker.workerId).assigned(),
        accepted: await getTasks(worker.workerId).accepted(),
        downloading: await getTasks(worker.workerId).downloading(),
        running: await getTasks(worker.workerId).running(),
        uploadingSuccess: await getTasks(worker.workerId).uploadingSuccess(),
        succeeded: await getTasks(worker.workerId).succeeded(),
        uploadingFailure: await getTasks(worker.workerId).uploadingFailure(),
        failed: await getTasks(worker.workerId).failed(),
        suspended: await getTasks(worker.workerId).suspended(),
        canceled: await getTasks(worker.workerId).canceled(),
        waiting: await getTasks(worker.workerId).waiting(),
      };
    }
  }

  // Function to update concurrency or prefetch values
  const updateValue = async (worker, field: 'concurrency' | 'prefetch', delta: number) => {
    const newValue = Math.max(0, worker[field] + delta);
    worker[field] = newValue;
    await updateWorkerConfig(worker.workerId, worker.concurrency, worker.prefetch);
    // Refresh data after the update
    await refresh();
  };
</script>

{#if workers && workers.length > 0}
  <div class="table-wrapper">
    <table>
      <thead>
        <tr>
          <th>Name</th>
          <th>Batch</th>
          <th>Status</th>
          <th>Concurrency</th>
          <th>Prefetch</th>
          <th>Starting </th>
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
            <!-- Worker name clickable to open task view -->
            <td class="clickable" on:click={() => goToTaskView(worker.name)}>{worker.name}</td>
            <td class="actions">              
              <!-- Bouton d'édition -->
              <button title="Edit"><Edit /></button>
            </td>
            <td>
              <div class="status-pill {getStatusClass(worker.status)}" title={getStatusText(worker.status)}></div>
            </td>

            <!-- Editable concurrency -->
            <td>
              <button class="small-btn" data-testid={`decrease-concurrency-${worker.workerId}`} on:click={() => updateValue(worker, 'concurrency', -1)}>-</button>
              {worker.concurrency}
              <button class="small-btn" data-testid={`increase-concurrency-${worker.workerId}`} on:click={() => updateValue(worker, 'concurrency', 1)}>+</button>
            </td>

            <!-- Editable prefetch -->
            <td>
              <button class="small-btn" data-testid={`decrease-prefetch-${worker.workerId}`} on:click={() => updateValue(worker, 'prefetch', -1)}>-</button>
              {worker.prefetch}
              <button class="small-btn" data-testid={`increase-prefetch-${worker.workerId}`} on:click={() => updateValue(worker, 'prefetch', 1)}>+</button>
            </td>

            <!-- Tasks awaiting execution: Pending + Assigned + Accepted -->
            <td
              data-testid={`tasks-awaiting-execution-${worker.workerId}`}
              title={`Pending: ${tasksStatus[worker.workerId]?.pending}, Assigned: ${tasksStatus[worker.workerId]?.assigned}, Accepted: ${tasksStatus[worker.workerId]?.accepted}`}
            >
              {(tasksStatus[worker.workerId]?.pending ?? 0)
              + (tasksStatus[worker.workerId]?.assigned ?? 0)
              + (tasksStatus[worker.workerId]?.accepted ?? 0)}
            </td>
            <!-- Tasks in progress: Downloading + Waiting + Running -->
            <td
              data-testid={`tasks-in-progress-${worker.workerId}`}
              title={`Downloading: ${tasksStatus[worker.workerId]?.downloading}, Waiting: ${tasksStatus[worker.workerId]?.waiting}, Running: ${tasksStatus[worker.workerId]?.running}`}
            >
              {(tasksStatus[worker.workerId]?.downloading ?? 0)
              + (tasksStatus[worker.workerId]?.waiting ?? 0)
              + (tasksStatus[worker.workerId]?.running ?? 0)}
            </td>
          
            <!-- Successful tasks: Uploading after success + Completed -->
            <td
              data-testid={`successful-tasks-${worker.workerId}`}
              title={`UploadingSuccess: ${tasksStatus[worker.workerId]?.uploadingSuccess}, Succeeded: ${tasksStatus[worker.workerId]?.succeeded}`}
            >
              {(tasksStatus[worker.workerId]?.uploadingSuccess ?? 0) + (tasksStatus[worker.workerId]?.succeeded ?? 0)}
            </td>
          

            <!-- Failed tasks: Uploading after failure + Failed + Suspended + Canceled -->
            <td
              data-testid={`failed-tasks-${worker.workerId}`}
              title={`UploadingFailure: ${tasksStatus[worker.workerId]?.uploadingFailure}, Failed: ${tasksStatus[worker.workerId]?.failed}, Suspended: ${tasksStatus[worker.workerId]?.suspended}, Canceled: ${tasksStatus[worker.workerId]?.canceled}`}
            >
              {(tasksStatus[worker.workerId]?.uploadingFailure ?? 0) 
              + (tasksStatus[worker.workerId]?.failed ?? 0) 
              + (tasksStatus[worker.workerId]?.suspended ?? 0) 
              + (tasksStatus[worker.workerId]?.canceled ?? 0)}
            </td>

            <!-- Statistics -->
            {#if workersStatsMap[worker.workerId]}
              <td>{workersStatsMap[worker.workerId].cpuUsagePercent.toFixed(1)}%</td>
              <td>{workersStatsMap[worker.workerId].memUsagePercent.toFixed(1)}%</td>
              <td>{workersStatsMap[worker.workerId].load1Min.toFixed(1)}</td>
              <td>{workersStatsMap[worker.workerId].iowaitPercent.toFixed(1)}%</td>
              <td>
                {#each workersStatsMap[worker.workerId].disks as disk (disk.deviceName)}
                  <div>{disk.deviceName}: {disk.usagePercent.toFixed(1)}%</div>
                {/each}
              </td>
            <!-- Disk IO -->
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
                <div>No Disk IO data available</div>
              {/if}
            </td>

            <!-- Network IO -->
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
                <div>No Network IO data available</div>
              {/if}
            </td>
            {:else}
              <td colspan="6">No statistics available for this worker.</td>
            {/if}
            
            <!-- Action buttons -->
            <td class="actions">

              <!-- Bouton de pause -->
              <button title="Pause"><PauseCircle /></button>

              <!-- Bouton d'effacement -->
              <button title="Clean"><Eraser /></button>
              
              <br />
              
              <!-- Bouton de rafraîchissement -->
              <button title="Restart"><RefreshCw /></button>

              <!-- Bouton de suppression -->
              <button 
                title="Delete"
                on:click={() => {
                  console.log('Worker ID:', worker.workerId);
                  if (worker.workerId) {
                    delWorker({ workerId: worker.workerId });
                  } else {
                    console.error('Invalid Worker ID');
                  }
                }} 
                data-testid={`delete-worker-${worker.workerId}`}
              >
                <Trash />
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p class="p1-worker">No workers found.</p>
{/if}
