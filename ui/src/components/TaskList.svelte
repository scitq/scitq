<script lang="ts">
  import { onMount, afterUpdate } from 'svelte';
  import { getJobStatusClass, getJobStatusText, getAllTasks, getSteps, retryTask } from '../lib/api';
  import { RefreshCcw, Download, Trash, Eye, Workflow } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';
  import { wsClient } from '../lib/wsClient';


  /**
   * Mapping of task IDs to their saved logs
   * @type {LogChunk[]}
   */
  export let taskLogsSaved: LogChunk[] = [];

  /**
   * List of all available workers
   * @type {Worker[]}
   */
  export let workers: Worker[] = [];

  /**
   * List of all available workflows
   * @type {Workflow[]}
   */
  export let workflows: Workflow[] = [];

  /**
   * List of all steps from all workflows
   * @type {Step[]}
   */
  export let allSteps: Step[] = [];

  /**
   * Callback function triggered when "Full Log" button is clicked
   * @type {(taskId: number) => void}
   */
  export let onOpenModal: (taskId: number) => void;

  /**
   * Currently displayed tasks in the table
   * @type {Task[]}
   */
  export let displayedTasks: Task[] = [];

  /**
   * Mapping of task IDs to their log containers for auto-scrolling
   * @type {Record<number, HTMLDivElement>}
   */
  let logContainers: Record<number, HTMLDivElement> = {};

  let errorMessage: string | null = null;

  /**
   * Resolves and returns a display name based on provided identifiers
   * @param {number} [workerId] - Optional worker ID
   * @param {number} [workflowId] - Optional workflow ID
   * @param {number} [stepId] - Optional step ID
   * @returns {string} Display name for the given identifier(s)
   */
  function getName(workerId?: number, workflowId?: number, stepId?: number): string {
    if (workerId != undefined) {
      const worker = workers.find(w => w.workerId === workerId);
      return worker ? worker.name : `Worker ${workerId}`;
    }

    if (workflowId != undefined && stepId == undefined) {
      const workflow = workflows.find(w => w.workflowId === workflowId);
      return workflow ? workflow.name : `Workflow ${workflowId}`;
    }

    if (workflowId != undefined && stepId != undefined) {
      const step = allSteps.find(s => s.workflowId === workflowId && s.stepId === stepId);
      return step ? step.name : `Step ${stepId}`;
    }

    return '';
  }

  /**
   * Auto-scrolls log containers to bottom after DOM updates
   * @returns {void}
   */
  afterUpdate(() => {
    Object.values(logContainers).forEach(container => {
      if (container) container.scrollTop = container.scrollHeight;
    });
  });

  /**
   * Retrieves stdout logs for a specific task
   * @param {number} taskId - ID of the task
   * @returns {Array<{id: string, text: string}>} Array of formatted stdout log entries
   */
  function getLogsOut(taskId: number) {
    const logs = taskLogsSaved.find(log => log.taskId === taskId)?.stdout || [];
    return logs.map((line, i) => ({ id: `${taskId}-out-${i}`, text: line }));
  }

  /**
   * Retrieves stderr logs for a specific task
   * @param {number} taskId - ID of the task
   * @returns {Array<{id: string, text: string}>} Array of formatted stderr log entries
   */
  function getLogsErr(taskId: number) {
    const logs = taskLogsSaved.find(log => log.taskId === taskId)?.stderr || [];
    return logs.map((line, i) => ({ id: `${taskId}-err-${i}`, text: line }));
  }

async function retryTaskClick(taskId: number) {
  errorMessage = null;
  try {
    const newId = await retryTask(taskId);
    console.log(`🔁 Retried task ${taskId} → new task ${newId}`);
  } catch (err) {
    console.error("Error retrying task:", err);
    errorMessage = err.message || `Failed to retry task ${taskId}`;
  }
}
</script>

{#if displayedTasks && displayedTasks.length > 0}
  <div class="tasks-table-wrapper" >
    {#if errorMessage}
      <div class="error-banner">{errorMessage}</div>
    {/if}
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
          <th>stdout/stderr</th>
          <th>Output</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each displayedTasks as task (task.taskId)}
          <tr data-testid={`task-${task.taskId}`}>
            <td>{task.taskId}</td>
            <td>{task.taskName}</td>
            <td>
              <div class="tasks-truncate-command" title={task.command}>{task.command}</div>
            </td>
            <td><a href="#/" class="link" data-testid={`worker-cell-${task.taskId}`}>{getName(task.workerId)}</a></td>
            <td><a href={`#/workflows?open=${task.workflowId}`} class="link">{getName(undefined, task.workflowId, undefined)}</a></td>
            <td><a href={`#/workflows?open=${task.workflowId}`} class="link">{getName(undefined, task.workflowId, task.stepId)}</a></td>
            <td>
              <div class="tasks-status-pill {getJobStatusClass(task.status)}" title={getJobStatusText(task.status)}></div>
            </td>
            <td>
              <div style="white-space: pre-wrap;">
                {#each getLogsOut(task.taskId) as log (log.id)}
                  {log.text}<br/>
                {/each}
                {#each getLogsErr(task.taskId) as log (log.id)}
                  {log.text}<br/>
                {/each}
              </div>
            </td>
            <td>{task.output}</td>
            
            <!-- Action Buttons -->
            <td class="workerCompo-actions">
              <button class="btn-action" title="Full Log" data-testid={`full-log-${task.taskId}`} on:click={() => onOpenModal(task.taskId)}><Eye /></button>
              <button class="btn-action" title="Restart" on:click={() => retryTaskClick(task.taskId)}><RefreshCcw /></button>
              <br />
              <button class="btn-action" title="Download"><Download /></button>
              <button class="btn-action" title="Delete"><Trash /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .error-banner {
    color: red;
    background-color: #ffe6e6;
    padding: 8px;
    border: 1px solid red;
    margin: 8px 0;
    border-radius: 4px;
  }
</style>