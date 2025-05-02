<script lang="ts">
    import { onMount } from 'svelte';
    import { Trash, RefreshCw } from 'lucide-svelte';
    import { getJobs, getStatusClass, getStatusText } from '../lib/api';
    import type { Job } from '../proto/taskqueue_pb';
    import "../styles/jobsCompo.css";
  
    let jobs: Job[] = [];
  
    // Fetch jobs on component mount
    onMount(async () => {
      jobs = await getJobs();
    });

  </script>
  
  {#if jobs && jobs.length > 0}
    <div class="table-wrapper">
      <table>
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
            <tr>
              <td>
                {#if job.action === 'C'}
                  Deploy Worker
                  {#if job.progression !== undefined && job.progression != 100}
                    <div class="progress-bar">
                      <div class="progress" style={`width: ${job.progression}%`} data-testid={`progress-bar-${job.jobId}`} 
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
                <!-- Status indicator -->
                <div class="status-pill {getStatusClass(job.status)}" title={getStatusText(job.status)} data-testid={`status-pill-${job.jobId}`}></div>
              </td>
              <td>{new Date(job.modifiedAt).toLocaleString()}</td>
              <td class="actions">
                {#if job.status === 'F'}
                  <button data-testid={`refresh-button-${job.jobId}`} on:click={() => handleRestart(job.jobId)} title="Restart"><RefreshCw /></button>
                {/if}
                <button data-testid={`trash-button-${job.jobId}`} title="Delete"><Trash /></button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="p1-worker">No jobs currently running.</p>
  {/if}
  