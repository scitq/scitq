<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { delWorker, getWorkers, updateWorkerConfig, getStatusClass, getStatusText, getStats, formatBytesPair} from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';

  let workers: Worker[] = [];
  let workersStatsMap: Record<number, taskqueue.WorkerStats> = {};

  onMount(async () => {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    // Retrieve worker statistics
    workersStatsMap = await getStats(workerIds);  // Call to the new getStats function
  });


  export async function refresh() {
    workers = await getWorkers();
    const workerIds = workers.map(worker => worker.workerId);
    workersStatsMap = await getStats(workerIds);
  }

  // Function to update concurrency or prefetch values
  const updateValue = async (worker, field: 'concurrency' | 'prefetch', delta: number) => {
    console.log(worker);
    const newValue = Math.max(0, worker[field] + delta);
    worker[field] = newValue;

    await updateWorkerConfig(worker.workerId, worker.concurrency, worker.prefetch);
    console.log(worker);
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

            <td>{worker.accepted}</td>
            <td>{worker.running}</td>
            <td>{worker.successes}</td>
            <td>{worker.fail}</td>

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
