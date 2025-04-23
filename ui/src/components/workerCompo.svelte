<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import { getWorkers, updateWorkerConfig } from '../lib/api';
  import { Edit, PauseCircle, Trash, RefreshCw, Eraser } from 'lucide-svelte';
  import '../styles/worker.css';

  let workers: Worker[] = [];

  onMount(async () => {
    workers = await getWorkers();
  });

  const updateValue = async (worker, field: 'concurrency' | 'prefetch', delta: number) => {
    console.log(worker);
    const newValue = Math.max(0, worker[field] + delta);
    worker[field] = newValue;

    await updateWorkerConfig(worker.workerId, {
      concurrency: worker.concurrency,
      prefetch: worker.prefetch,
    });
    console.log(worker);
  };
</script>

{#if workers && workers.length > 0}
  <div class="table-wrapper">
    <table>
      <thead>
        <tr>
          <th>Nom</th>
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
            <td class="clickable" on:click={() => goToTaskView(worker.name)}>{worker.name}</td>
            <td>{worker.batch}</td>
            <td>{worker.status}</td>
            
            <!-- Concurrency editable -->
            <td>
              <button class="small-btn" on:click={() => updateValue(worker, 'concurrency', -1)}>-</button>
              {worker.concurrency}
              <button class="small-btn" on:click={() => updateValue(worker, 'concurrency', 1)}>+</button>
            </td>

            <!-- Prefetch editable -->
            <td>
              <button class="small-btn" on:click={() => updateValue(worker, 'prefetch', -1)}>-</button>
              {worker.prefetch}
              <button class="small-btn" on:click={() => updateValue(worker, 'prefetch', 1)}>+</button>
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

            <td class="actions">
              <button><Edit /></button>
              <button><PauseCircle /></button>
              <button><Eraser /></button>
              <button><RefreshCw /></button>
              <button><Trash /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p>Aucun worker.</p>
{/if}
