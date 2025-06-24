<script lang="ts">
  import { onMount } from 'svelte';
  import { Trash, RefreshCw } from 'lucide-svelte';
  import { getJobs, getJobStatusClass, getJobStatusText, delJob, getJobStatus } from '../lib/api';
  import type { Job } from '../proto/taskqueue_pb';
  import "../styles/worker.css";
  import "../styles/jobsCompo.css";
  import { JobId } from '../../gen/taskqueue';

/** List of all jobs */
export let jobs: Job[] = [];

/** Jobs enriched with status and progression for display */
let displayJobs: (Job & { status?: string, progression?: number })[] = [];

/** Interval reference for auto-refresh */
let interval;

/** Flag to track initial data load */
let hasLoaded = false;

/** Map of job statuses and progressions by job ID */
let jobStatusMap = new Map<number, { status: string, progression?: number }>();

/**
 * Callback when job is deleted
 * @event
 * @param jobId ID of deleted job
 */
export let onJobDeleted: (event: { detail: { jobId: number } }) => void = () => {};

/**
 * Initialize component - starts auto-refresh
 */
onMount(() => {
  updateJobData();
  interval = setInterval(updateJobData, 5000);
  return () => clearInterval(interval);
});

/**
 * Auto-update when jobs are initialized
 */
$: if (jobs.length > 0 && !hasLoaded) {
  updateJobData();
  hasLoaded = true;
}

/**
 * Reactive jobs list with enriched status data
 */
$: displayJobs = jobs.map(job => {
  const statusInfo = jobStatusMap.get(job.jobId) || {};
  return {
    ...job,
    status: statusInfo.status || job.status || 'unknown',
    progression: statusInfo.progression ?? job.progression
  };
});

/**
 * Fetch latest job statuses and progressions
 * Updates internal status map
 */
async function updateJobData() {
  if (jobs.length === 0) return;

  try {
    const jobsStatus = await getJobStatus(jobs.map(j => j.jobId));
    jobStatusMap = new Map(jobsStatus.map(s => [s.jobId, { 
      status: s.jobStatus, 
      progression: s.progression 
    }]));
  } catch (err) {
    console.error('Error loading job data:', err);
  }
}

/**
 * Delete a job and clean up local state
 * @param jobId ID of job to delete
 */
async function deleteJob(jobId: number) {
  onJobDeleted({ detail: { jobId } });
  jobStatusMap.delete(jobId);
}

/**
 * Restart a job (stub implementation)
 * @param jobId ID of job to restart
 */
function handleRestart(jobId: number) {
  console.log('Restarting job:', jobId);
  // TODO: Implement actual restart logic
}
</script>

{#if displayJobs && displayJobs.length > 0}
  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>Job</th>
          <th>Target</th>
          <th>Status</th>
          <th>Latest Update</th>
          <th>Action</th>
        </tr>
      </thead>
      <tbody>
        {#each displayJobs as job (job.jobId)}
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
                {/if}
              {:else if job.action === 'D'}
                Destroy Worker
              {:else}
                {job.action}
              {/if}
            </td>
            <td>{job.workerId}</td>
            <td>
              <div
                class="jobCompo-status-pill {getJobStatusClass(job.status)}"
                title={getJobStatusText(job.status)}
                data-testid={`status-pill-${job.jobId}`}
              ></div>
            </td>
            <td>{new Date(job.modifiedAt).toLocaleString()}</td>
            <td class="workerCompo-actions">
              {#if job.status === 'F'}
                <button
                  class="btn-action"
                  data-testid={`refresh-button-${job.jobId}`}
                  on:click={() => handleRestart(job.jobId)}
                  title="Restart"
                >
                  <RefreshCw />
                </button>
              {/if}

              <button
                class="btn-action"
                title="Delete"
                on:click={() => deleteJob(job.jobId)}
                data-testid={`trash-button-${job.jobId}`}
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