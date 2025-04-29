<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { delWorker, getWorkers, updateWorkerConfig, getStatusClass, getStatusText } from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';

  let workers: Worker[] = [];

  onMount(async () => {
    workers = await getWorkers();
  });

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
          <th>Disk Usage%</th>
          <th>Disk R/W</th>
          <th>Network Sent/Recv</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each workers as worker (worker.workerId)}
          <tr>
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
            <td>{worker.cpuUsage}%</td>
            <td>{worker.memUsage}%</td>
            <td>{worker.loadAvg}</td>
            <td>{worker.diskUsage}</td>
            <td>{worker.diskRW}</td>
            <td>{worker.netIO}</td>

            <!-- Action buttons -->
            <td class="actions">
              <button><Edit /></button>
              <button><PauseCircle /></button>
              <button><Eraser /></button>
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
