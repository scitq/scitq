<script lang="ts">
  import { onMount } from 'svelte';
  import { Trash, RefreshCw } from 'lucide-svelte';
  import { getJobs, getJobStatusClass, getJobStatusText, delJob } from '../lib/api';
  import type { Job } from '../proto/taskqueue_pb';

  import "../styles/worker.css";    // Shared styles for tables and buttons
  import "../styles/jobsCompo.css"; // Job-specific styles

  let jobs: Job[] = [];

  // Fetch jobs from the API when component mounts
  onMount(async () => {
    jobs = await getJobs();
  });

  // Remove job from UI list and backend
  async function deleteJob(jobId: number) {
    jobs = jobs.filter(job => job.jobId !== jobId);
    await delJob({ jobId });
  }

  // Placeholder for restarting a job (not yet implemented)
  function handleRestart(jobId: number) {
    // TODO: Implement job restart logic
  }
</script>

{#if jobs && jobs.length > 0}
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
                <!-- Restart button (only for finished jobs) -->
                <button
                  class="btn-action"
                  data-testid={`refresh-button-${job.jobId}`}
                  on:click={() => handleRestart(job.jobId)}
                  title="Restart"
                >
                  <RefreshCw />
                </button>
              {/if}

              <!-- Delete button -->
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
