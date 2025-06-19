<script lang="ts">
  import { onMount } from 'svelte';
  import * as grpcWeb from 'grpc-web';
  import { getSteps } from '../lib/api';
  import StepList from './StepList.svelte';
  import { RefreshCw, PauseCircle, CircleX, Eraser } from 'lucide-svelte';

  /**
   * Workflow ID passed as a prop to fetch corresponding steps.
   * @type {number}
   */
  export let workflowId: number;

  /**
   * List of steps for the given workflow.
   * @type {Array}
   */
  let steps = [];

  /**
   * Fetches the steps for the given workflow on component mount.
   * Updates the `steps` variable with the fetched data.
   */
  onMount(async () => {
    steps = await getSteps(workflowId);
  });
</script>



{#if steps && steps.length > 0}
  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>#</th>
          <th>Name</th>
          <th>Workers</th>
          <th>Progress</th>
          <th>Starting</th>
          <th>Progress</th>
          <th>Successes</th>
          <th>Fails</th>
          <th>Total</th>
          <th>Average duration [min-max]</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each steps as step (step.stepId)}
          <tr data-testid={`step-${step.stepId}`}>
            <td>{step.stepId}</td>
            <td> <a href="#/tasks?workflowId={workflowId}&stepId={step.stepId}" class="workerCompo-clickable">{step.name}</a> </td>
            <td>Worker</td>
            <td> 
              <div class="wf-progress-bar"></div>
            </td>
            <td>Starting</td>
            <td>Progress</td>
            <td>Successes</td>
            <td>Fails</td>
            <td>Total</td>
            <td>Average duration [min-max]</td>

            <td class="workerCompo-actions">
              <button class="btn-action" title="Pause"><PauseCircle /></button>
              <button class="btn-action" title="Reset"><RefreshCw /></button>
              <button class="btn-action" title="Break"><CircleX /></button>
              <button class="btn-action" title="Clear"><Eraser /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p>No steps found for workflow #{workflowId}</p>
{/if}
