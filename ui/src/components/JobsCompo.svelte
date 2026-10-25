<script lang="ts">
  import { Trash, RefreshCw } from 'lucide-svelte';
  import { getJobStatusClass, getJobStatusText, delJob, retryJob } from '../lib/api';
  import type { Job } from '../proto/taskqueue_pb';
  import "../styles/worker.css";
  import "../styles/jobsCompo.css";
  import { JobId } from '../../gen/taskqueue';

  // Renders the dashboard's job list. The list is the parent's, bound so
  // that row actions update it; live updates are applied by the parent
  // (Dashboard.svelte), the list's only writer besides these actions.
  interface Props {
    jobs?: Job[];
  }

  let { jobs = $bindable([]) }: Props = $props();

  let deleting = $state(new Set<number>());

  const MAX_DELETE_CONCURRENCY = 3;
  let deleteQueue: number[] = [];
  let inFlightDeletes = 0;

  function deleteJob(jobId: number): void {
    if (deleting.has(jobId)) return;
    deleting = new Set(deleting).add(jobId);
    deleteQueue.push(jobId);
    processDeleteQueue();
  }

  function processDeleteQueue() {
    while (inFlightDeletes < MAX_DELETE_CONCURRENCY && deleteQueue.length > 0) {
      const jobId = deleteQueue.shift()!;
      inFlightDeletes++;
      delJob({ jobId } as JobId)
        .then(() => { jobs = jobs.filter(j => j.jobId !== jobId); })
        .catch((err) => console.error('Failed to delete job', jobId, err))
        .finally(() => {
          inFlightDeletes--;
          const next = new Set(deleting);
          next.delete(jobId);
          deleting = next;
          processDeleteQueue();
        });
    }
  }

  function setStatus(jobId: number, status: string) {
    jobs = jobs.map(j => j.jobId === jobId ? { ...j, status } : j);
  }

  /**
   * Retry a terminal job (F/X). Optimistically flips the row to P; the
   * server's job.updated event confirms it. On error the row goes back and
   * the operator hears about it (e.g. "worker already cleaned up — deploy
   * fresh") instead of the click being a silent no-op.
   */
  async function handleRestart(jobId: number): Promise<void> {
    const prev = jobs.find(j => j.jobId === jobId)?.status;
    setStatus(jobId, 'P');
    try {
      await retryJob(jobId);
    } catch (err) {
      if (prev !== undefined) setStatus(jobId, prev);
      const msg = err instanceof Error ? err.message : String(err);
      alert(`Retry failed: ${msg}`);
    }
  }
</script>

{#if jobs && jobs.length > 0}
  <div class="jobCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>Job</th>
          <th>Target</th>
          <th>Status</th>
          <th>Error</th>
          <th>Latest Update</th>
          <th>Action</th>
        </tr>
      </thead>
      <tbody>
        {#each jobs as job (job.jobId)}
          <tr data-testid={`job-row-${job.jobId}`}>
            <td>
              {#if job.action === 'C'}
                Deploy Worker
                {#if job.progression !== undefined && job.progression != 100}
                  <div class="jobCompo-progress-bar">
                    <div
                      class="jobCompo-progress"
                      style={`width: ${job.progression}%`}
                      data-testid={`progress-bar-${job.jobId}`}
                    ></div>
                  </div>
                  <small class="jobCompo-progress-text" data-testid={`progress-text-${job.jobId}`}>{job.progression}%</small>
                {/if}
              {:else if job.action === 'D'}
                Destroy Worker
              {:else}
                {job.action}
              {/if}
            </td>
            <td title={job.flavorInfo || ''} data-testid={`job-target-${job.jobId}`}>{job.workerName || job.workerId}</td>
            <td>
              <div
                class="jobCompo-status-pill {getJobStatusClass(job.status)}"
                title={getJobStatusText(job.status)}
                data-testid={`status-pill-${job.jobId}`}
              ></div>
            </td>
            <td>
              {#if job.errorClass}
                <span
                  class="job-error-badge job-error-{job.errorClass}"
                  title={job.errorMessage || job.errorClass}
                  data-testid={`error-class-${job.jobId}`}
                >{job.errorClass}</span>
              {/if}
            </td>
            <td>{new Date(job.modifiedAt).toLocaleString()}</td>
            <td class="workerCompo-actions">
              {#if job.status === 'F'}
                <button
                  class="btn-action"
                  data-testid={`refresh-button-${job.jobId}`}
                  onclick={() => handleRestart(job.jobId)}
                  title="Restart"
                >
                  <RefreshCw />
                </button>
              {/if}

              <button
                class="btn-action"
                title="Delete"
                onclick={() => deleteJob(job.jobId)}
                data-testid={`trash-button-${job.jobId}`}
                disabled={deleting.has(job.jobId)}
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
  <p class="workerCompo-empty-state">No jobs currently running.</p>
{/if}