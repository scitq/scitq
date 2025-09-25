<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { Trash, RefreshCw } from 'lucide-svelte';
  import { getJobs, getJobStatusClass, getJobStatusText, delJob, getJobStatus } from '../lib/api';
  import type { Job } from '../proto/taskqueue_pb';
  import "../styles/worker.css";
  import "../styles/jobsCompo.css";
  import { JobId } from '../../gen/taskqueue';
  import { wsClient } from '../lib/wsClient';
  import type { WSMessage } from '../lib/wsTypes';

  /**
   * List of all jobs passed as a prop to the component
   * @type {Job[]}
   */
  export let jobs: Job[] = [];

  /**
   * Jobs enriched with status and progression for display
   * @type {Array<Job & { status?: string, progression?: number }>}
   */
  let displayJobs: (Job & { status?: string, progression?: number })[] = [];


  /**
   * Flag to track initial data load status
   * @type {boolean}
   */
  let hasLoaded = false;

  /**
   * Map storing job statuses and progressions by job ID
   * @type {Map<number, { status: string, progression?: number }>}
   */
  let jobStatusMap = new Map<number, { status: string, progression?: number }>();

  let deleting = new Set<number>();

  const MAX_DELETE_CONCURRENCY = 3;
  let deleteQueue: number[] = [];
  let pendingDelete = new Set<number>(); // prevents duplicate enqueues
  let processingDeletes = false;
  let inFlightDeletes = 0;

  const dispatch = createEventDispatcher();
  let sentinel: HTMLDivElement | null = null;
  let observer: IntersectionObserver | null = null;
  let lastLoadRequest = 0;

  // --- WS unsubscribe reference for cleanup ---
  let unsubscribeWS: (() => void) | null = null;

  function maybeRequestMoreJobs() {
    const now = Date.now();
    if (now - lastLoadRequest < 500) return; // debounce
    lastLoadRequest = now;
    dispatch('load-more-jobs');
  }

  function enqueueDelete(jobId: number) {
    if (pendingDelete.has(jobId)) return; // already queued or in-flight
    pendingDelete.add(jobId);
    deleteQueue.push(jobId);
    processDeleteQueue();
  }

  async function processDeleteQueue() {
    if (processingDeletes) return;
    processingDeletes = true;
    try {
      while (deleteQueue.length > 0 || inFlightDeletes > 0) {
        while (inFlightDeletes < MAX_DELETE_CONCURRENCY && deleteQueue.length > 0) {
          const nextId = deleteQueue.shift();
          if (nextId === undefined) break;
          inFlightDeletes++;
          // mark row as deleting (disable button + optional style)
          deleting.add(nextId);
          (async () => {
            try {
              const req: JobId = { jobId: nextId };
              await delJob(req);
              // Only remove from UI after a confirmed success
              jobs = jobs.filter(j => j.jobId !== nextId);
              jobStatusMap.delete(nextId);
            } catch (err) {
              console.error('Failed to delete job', nextId, err);
              // optional: non-blocking user feedback (keep row visible)
              // alert(`Failed to delete job ${nextId}`);
            } finally {
              deleting.delete(nextId);
              pendingDelete.delete(nextId);
              inFlightDeletes--;
            }
          })();
        }
        // Small tick to yield back to event loop while in-flight operations settle
        await new Promise(r => setTimeout(r, 50));
      }
      // After batch deletions, the list may have shrunk enough to expose the sentinel; proactively request more jobs.
      // The IntersectionObserver will also fire if visibility changed, but this is a safe extra nudge.
      maybeRequestMoreJobs();
    } finally {
      processingDeletes = false;
    }
  }

  function deleteJob(jobId: number): void {
    enqueueDelete(jobId);
  }

  // Removed sorting reactive statement to honor server-side job_id DESC ordering

  // Live updates are driven by WS `job` events (created/updated/deleted).
  // We still perform one initial `updateJobData()` to hydrate statuses for preloaded jobs.
  onMount(() => {
    updateJobData(); // one-time seed

    // Setup intersection observer to request more jobs when the list end is visible
    observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          maybeRequestMoreJobs();
        }
      }
    }, { root: null, rootMargin: '0px', threshold: 0.01 });

    if (sentinel) observer.observe(sentinel);

    function handleMessage(message: WSMessage) {
      const { type, action, id, payload } = message;
      console.log('[WS][job]', { type, action, id, payload });
      if (type !== 'job') return;

      switch (action) {
        case 'created': {
          // A new job was created; ask parent to fetch the newest page so it appears immediately
          maybeRequestMoreJobs();

          // Seed status map so the new job (once loaded) gets a sensible status immediately
          if (typeof id === 'number' && (payload as any) && typeof (payload as any).status === 'string') {
            const status = (payload as any).status as string;
            jobStatusMap.set(id, { status, progression: 0 });
            jobStatusMap = new Map(jobStatusMap);
            console.log('[WS][job][created] seed status/prog', id, jobStatusMap.get(id));
          }

          // If the new job isn't in the current list yet, prepend a minimal placeholder on top
          if (typeof id === 'number') {
            const p: any = payload;
            if (!jobs.some(j => j.jobId === id)) {
              const placeholder = {
                jobId: id,
                workerId: typeof p.workerId === 'number' ? p.workerId : 0,
                action: typeof p.action === 'string' ? p.action : '',
                status: typeof p.status === 'string' ? p.status : 'P',
                modifiedAt: p.modifiedAt || new Date().toISOString(),
              } as Job;
              jobs = [placeholder, ...jobs];
            }
          }
          break;
        }
        case 'updated': {
          const jid = id;
          const p: any = payload;
          const prev = jobStatusMap.get(jid) || { status: 'unknown', progression: undefined };
          const nextStatus = typeof p.status === 'string' ? p.status : prev.status;
          const nextProg = typeof p.progression === 'number' ? p.progression : prev.progression;
          console.log('[WS][job][updated] before', jid, prev, '->', { status: nextStatus, progression: nextProg });
          jobStatusMap.set(jid, { status: nextStatus, progression: nextProg });
          jobStatusMap = new Map(jobStatusMap);
          console.log('[WS][job][updated] after', jid, jobStatusMap.get(jid));

          // Update the job's modifiedAt so display is accurate
          if (p && p.modifiedAt) {
            const idx = jobs.findIndex(j => j.jobId === jid);
            if (idx !== -1) {
              const clone = [...jobs];
              clone[idx] = { ...clone[idx], modifiedAt: p.modifiedAt } as Job;
              jobs = clone;
            }
          }
          break;
        }
        case 'deleted': {
          const jid = id;
          // Remove from local lists so it disappears without reload
          jobs = jobs.filter(j => j.jobId !== jid);
          jobStatusMap.delete(jid);
          jobStatusMap = new Map(jobStatusMap);
          // Optionally request more to replenish the page if it got short
          maybeRequestMoreJobs();
          break;
        }
        default:
          break;
      }
    }
    unsubscribeWS = wsClient.subscribeWithTopics({ job: [] }, handleMessage);

    return () => {
      if (observer && sentinel) observer.unobserve(sentinel);
      observer = null;
      if (unsubscribeWS) unsubscribeWS();
      unsubscribeWS = null;
    };
  });

  /**
   * Reactive statement that updates job data when jobs are initialized
   * One-shot status hydration after first jobs arrive
   */
  $: if (jobs.length > 0 && !hasLoaded) {
    // One-shot status hydration after first jobs arrive
    updateJobData();
    hasLoaded = true;
  }

  /**
   * Reactive statement that creates display jobs with enriched status data
   */
  $: displayJobs = jobs.map(job => {
    const statusInfo = jobStatusMap.get(job.jobId) || {};
    return {
      ...job,
      status: statusInfo.status || job.status || 'unknown',
      progression: statusInfo.progression ?? job.progression
    } as Job & { status?: string; progression?: number };
  });

  /**
   * Fetches latest job statuses and progressions
   * Updates the internal job status map
   * @async
   * @returns {Promise<void>}
   */
  async function updateJobData(): Promise<void> {
    if (jobs.length === 0) return; 

    try {
      const jobsStatus = await getJobStatus(jobs.map(j => j.jobId));
      jobStatusMap = new Map(jobsStatus.map(s => [s.jobId, { 
        status: s.jobStatus, 
        progression: s.progression 
      }]));
      console.log('[POLL][job] seeded statuses', jobsStatus);
    } catch (err) {
      console.error('Error loading job data:', err);
    }
  }

  /**
   * Handles job restart functionality
   * Currently logs to console (implementation pending)
   * @param {number} jobId - The ID of the job to restart
   */
  function handleRestart(jobId: number): void {
    console.log('Restarting job:', jobId);
    // TODO: Implement actual restart logic
  }
</script>

{#if displayJobs && displayJobs.length > 0}
  <div class="jobCompo-table-wrapper">
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
                  <small class="jobCompo-progress-text" data-testid={`progress-text-${job.jobId}`}>{job.progression}%</small>
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
                disabled={deleting.has(job.jobId) || pendingDelete.has(job.jobId)}
              >
                <Trash />
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <div bind:this={sentinel} style="height: 1px; width: 100%;"></div>
  </div>
{:else}
  <p class="workerCompo-empty-state">No jobs currently running.</p>
{/if}