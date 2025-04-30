<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { delWorker, getWorkers, updateWorkerConfig, getStatusClass, getStatusText, getStats, formatBytesPair, getTasks} from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';

  let workers: Worker[] = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};

  let tasksAccepted: Record<number, number> = {};
  let tasksRunning: Record<number, number> = {};
  let tasksSuccesses: Record<number, number> = {};
  let tasksFailed: Record<number, number> = {};

  onMount(async () => {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    // Retrieve worker statistics
    workersStatsMap = await getStats(workerIds);  // Call to the new getStats function

    // Charger le nombre de tâches par worker
    for (const worker of workers) {
      const counter = getTasks(worker.workerId);
      tasksAccepted[worker.workerId] = await counter.accepted();
      tasksRunning[worker.workerId] = await counter.running();
      tasksSuccesses[worker.workerId] = await counter.successes();
      tasksFailed[worker.workerId] = await counter.failed();
    }
  });


  export async function refresh() {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    workersStatsMap = await getStats(workerIds);

    for (const worker of workers) {
      const counter = getTasks(worker.workerId);
      tasksAccepted[worker.workerId] = (await getTasks(worker.workerId)).accepted;
      tasksRunning[worker.workerId] = (await getTasks(worker.workerId)).running;
      tasksSuccesses[worker.workerId] = (await getTasks(worker.workerId)).successes;
      tasksFailed[worker.workerId] = (await getTasks(worker.workerId)).failed;
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
          <th>Accepted</th>
          <th>Running</th>
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
            <td>{worker.batch}</td>
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

            <td>{tasksAccepted[worker.workerId]}</td>
            <td>{tasksRunning[worker.workerId]}</td>
            <td>{tasksSuccesses[worker.workerId]}</td>
            <td>{tasksFailed[worker.workerId]}</td>

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
              <button><Edit /></button>
              <button><PauseCircle /></button>
              <button><Eraser /></button>
              <br />
              <button><RefreshCw /></button>
              <button on:click={() => {
                console.log('Worker ID:', worker.workerId);
                if (worker.workerId) {
                  delWorker({ workerId: worker.workerId });
                } else {
                  console.error('Invalid Worker ID');
                }
              }} data-testid={`delete-worker-${worker.workerId}`}>
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
