<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount } from 'svelte';
  import {getJobStatusClass, getJobStatusText, getAllTasks} from '../lib/api';
  import { RefreshCcw, Download, Trash } from 'lucide-svelte';
  import '../styles/worker.css';
   import '../styles/jobsCompo.css';

  export let tasks : TaskList = [];
  
</script>


{#if tasks && tasks.length > 0}
  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>#</th>
          <th>Name</th>
          <th>Command</th>
          <th>Worker</th>
          <th>Wf</th>
          <th>Step</th>
          <th>Status</th>
          <th>Start</th>
          <th>Runtime</th>
          <th>Output</th>
          <th>Error</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each tasks as task (task.taskId)}
          <tr data-testid={`task-${task.taskId}`}>
            <td>{task.taskId}</td>
            <td>{task.name}</td>
            <td>{task.command}</td>
            <td>{task.workerId}</td>
            <td>Wf</td>
            <td>{task.stepId}</td>
            <td>
              <div class="tasks-status-pill {getJobStatusClass(task.status)}" title={getJobStatusText(task.status)}></div>
            </td>
            <td>Start</td>
            <td>{task.runningTimeout}</td>
            <td>{task.output}</td>
            <td>{task.retry}</td>

            <td class="workerCompo-actions">
              <button class="btn-action" title="Restart"><RefreshCcw /></button>
              <button class="btn-action" title="Download"><Download /></button>
              <button class="btn-action" title="Delete"><Trash /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p class="workerCompo-empty-state">No task found.</p>
{/if}
