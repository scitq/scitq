<script lang="ts">
  import * as grpcWeb from 'grpc-web';
  import { onMount, afterUpdate } from 'svelte';
  import { getJobStatusClass, getJobStatusText, getAllTasks, getSteps } from '../lib/api';
  import { RefreshCcw, Download, Trash, Eye, Workflow } from 'lucide-svelte';
  import '../styles/worker.css';
  import '../styles/jobsCompo.css';
  import { TaskId, TaskLog } from '../../gen/taskqueue';

  /** 
   * List of tasks to be displayed in the table.
   * @type {TaskList} 
   */
  export let tasks: TaskList = [];

  /**
   * Mapping of task IDs to their saved logs.
   * @type {Record<number, TaskLog[]>}
   */
  export let taskLogsSaved: LogChunk[] = [];

  /**
   * List of all workers available.
   * @type {Worker[]}
   */
  export let workers: Worker[] = [];

  /**
   * List of all workflows available.
   * @type {Workflow[]}
   */
  export let workflows: Workflow[] = [];

  /**
   * List of all steps from all workflows.
   * @type {Step[]}
   */
  export let allSteps: Step[] = [];

  /**
   * Callback function called when the "Full Log" button is clicked.
   * @type {(taskId: number) => void}
   */
  export let onOpenModal: (taskId: number) => void;

  /**
   * Mapping of task IDs to their respective log HTML containers.
   * Used for auto-scrolling log content.
   * @type {Record<number, HTMLDivElement>}
   */
  let logContainers: Record<number, HTMLDivElement> = {};

  /**
   * Resolves and returns a display name based on provided identifiers.
   * Priority: workerId > workflowId > stepId (with workflowId)
   * @param {number} [workerId]
   * @param {number} [workflowId]
   * @param {number} [stepId]
   * @returns {string} Display name
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
   * Lifecycle hook called after DOM updates.
   * Used to scroll log containers to the bottom automatically.
   */
  afterUpdate(() => {
    Object.values(logContainers).forEach(container => {
      if (container) container.scrollTop = container.scrollHeight;
    });
  });

  function getLogsOut(taskId: number) {
    return taskLogsSaved.find(log => log.taskId === taskId)?.stdout || [];
  }
  function getLogsErr(taskId: number) {
    return taskLogsSaved.find(log => log.taskId === taskId)?.stderr || [];
  }
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
            <td>
              <div class="tasks-truncate-command" title={task.command}>{task.command}</div>
            </td>
            <td> <a href="#/" class="link">{getName(task.workerId)}</a></td>
            <td> <a href={`#/workflows?open=${1}`} class="link">{getName(undefined, task.workflowId, undefined)}</a></td>
            <td> <a href={`#/workflows?open=${1}`} class="link">{getName(undefined, 1, task.stepId)}</a></td>
            <td>
              <div class="tasks-status-pill {getJobStatusClass(task.status)}" title={getJobStatusText(task.status)}></div>
            </td>
            <td> </td>
            <td>{task.runningTimeout}</td>
            <td>
              <div style="white-space: pre-wrap;">
                {#each getLogsOut(task.taskId) as line (line)}
                  {line}
                {/each}
              </div>
            </td>

            <td>
              <div style="white-space: pre-wrap;">
                {#each getLogsErr(task.taskId) as line (line)}
                  {line}
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
{:else}
  <p class="workerCompo-empty-state">No task found.</p>
{/if}