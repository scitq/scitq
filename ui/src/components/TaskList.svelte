<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount, afterUpdate } from 'svelte';
  import { getJobStatusClass, getJobStatusText, getAllTasks, getSteps } from '../lib/api';
  import { RefreshCcw, Download, Trash, Eye, Workflow } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';
  import { TaskId, TaskLog } from '../../gen/taskqueue';

  /** Mapping of task IDs to their saved logs */
  export let taskLogsSaved: LogChunk[] = [];

  /** List of all available workers */
  export let workers: Worker[] = [];

  /** List of all available workflows */
  export let workflows: Workflow[] = [];

  /** List of all steps from all workflows */
  export let allSteps: Step[] = [];

  /** Callback function triggered when "Full Log" button is clicked */
  export let onOpenModal: (taskId: number) => void;

  /** Currently displayed tasks in the table */
  export let displayedTasks: Task[] = [];

  /** Mapping of task IDs to their log containers for auto-scrolling */
  let logContainers: Record<number, HTMLDivElement> = {};

  /**
   * Resolves and returns a display name based on provided identifiers
   * @param workerId - Optional worker ID
   * @param workflowId - Optional workflow ID
   * @param stepId - Optional step ID
   * @returns Display name for the given identifier(s)
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

  /** Auto-scrolls log containers to bottom after DOM updates */
  afterUpdate(() => {
    Object.values(logContainers).forEach(container => {
      if (container) container.scrollTop = container.scrollHeight;
    });
  });

  /**
   * Retrieves stdout logs for a specific task
   * @param taskId - ID of the task
   * @returns Array of formatted stdout log entries
   */
  function getLogsOut(taskId: number) {
    const logs = taskLogsSaved.find(log => log.taskId === taskId)?.stdout || [];
    return logs.map((line, i) => ({ id: `${taskId}-out-${i}`, text: line }));
  }

  /**
   * Retrieves stderr logs for a specific task
   * @param taskId - ID of the task
   * @returns Array of formatted stderr log entries
   */
  function getLogsErr(taskId: number) {
    const logs = taskLogsSaved.find(log => log.taskId === taskId)?.stderr || [];
    return logs.map((line, i) => ({ id: `${taskId}-err-${i}`, text: line }));
  }
</script>

{#if displayedTasks && displayedTasks.length > 0}
  <div class="tasks-table-wrapper">
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
        {#each displayedTasks as task (task.taskId)}
          <tr data-testid={`task-${task.taskId}`}>
            <td>{task.taskId}</td>
            <td>{task.name}</td>
            <td>
              <div class="tasks-truncate-command" title={task.command}>{task.command}</div>
            </td>
            <td><a href="#/" class="link" data-testid={`worker-cell-${task.taskId}`}>{getName(task.workerId)}</a></td>
            <td><a href={`#/workflows?open=${1}`} class="link">{getName(undefined, task.workflowId, undefined)}</a></td>
            <td><a href={`#/workflows?open=${1}`} class="link">{getName(undefined, 1, task.stepId)}</a></td>
            <td>
              <div class="tasks-status-pill {getJobStatusClass(task.status)}" title={getJobStatusText(task.status)}></div>
            </td>
            <td></td>
            <td>{task.runningTimeout}</td>
            <td>
              <div style="white-space: pre-wrap;">
                {#each getLogsOut(task.taskId) as log (log.id)}
                  {log.text}<br/>
                {/each}
              </div>
            </td>
            <td>
              <div style="white-space: pre-wrap;">
                {#each getLogsErr(task.taskId) as log (log.id)}
                  {log.text}<br/>
                {/each}
              </div>
            </td>
            <td class="workerCompo-actions">
              <button class="btn-action" title="Full Log" data-testid={`full-log-${task.taskId}`} on:click={() => onOpenModal(task.taskId)}><Eye /></button>
              <button class="btn-action" title="Restart"><RefreshCcw /></button>
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