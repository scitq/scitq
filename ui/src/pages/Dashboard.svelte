<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getWorkers, getJobs, updateWorkerConfig, delWorker, delJob } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import WorkerCompo from '../components/WorkerCompo.svelte';
  import CreateForm from '../components/CreateForm.svelte';
  import JobCompo from '../components/JobsCompo.svelte';
  import '../styles/dashboard.css';

  let workers = [];
  let jobs = [];
  let pendingJobs = [];
  let hasMoreJobs = true;
  let isLoading = false;
  let jobsContainer: HTMLDivElement;
  let isScrolledToTop = true;
  let newJobsCount = 0;
  let showNewJobsNotification = false;

  const JOBS_CHUNK_SIZE = 25;
  let unsubscribeWS: () => void;

  let successMessage: string = '';
  let alertTimeout: NodeJS.Timeout;

  onMount(async () => {
    workers = await getWorkers();
    jobs = await getJobs(JOBS_CHUNK_SIZE, 0);
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  onDestroy(() => {
    unsubscribeWS?.();
  });

  function handleMessage(message) {

    if (message.type === 'job-deleted') {
      const payload = message.payload;
      if (!payload || typeof payload.jobId !== 'number') return;

      const idToRemove = payload.jobId;
      jobs = jobs.filter(j => j.jobId !== idToRemove);
      pendingJobs = pendingJobs.filter(j => j.jobId !== idToRemove);
    }


    if (message.type === 'worker-created') {
      const newWorker = message.payloadWorker;
      const alreadyExists = workers.some(w => w.workerId === newWorker.workerId);
      if (!alreadyExists) {
        workers = [...workers, newWorker];
      }

      const newJob = message.payloadJob;
      const existsInDisplayed = jobs.some(j => j.jobId === newJob.jobId);
      const existsInPending = pendingJobs.some(j => j.jobId === newJob.jobId);

      if (!existsInDisplayed && !existsInPending) {
        if (isScrolledToTop) {
          jobs = [newJob, ...jobs];
        } else {
          pendingJobs = [newJob, ...pendingJobs];
          newJobsCount = pendingJobs.length;
          showNewJobsNotification = true;
        }
      }
    }

    if (message.type === 'worker-deleted') {
      const workerId = message.payloadWorker.workerId;
      workers = workers.filter(w => w.workerId !== workerId);

      const newJob = message.payloadJob;
      const existsInDisplayed = jobs.some(j => j.jobId === newJob.jobId);
      const existsInPending = pendingJobs.some(j => j.jobId === newJob.jobId);

      if (!existsInDisplayed && !existsInPending) {
        if (isScrolledToTop) {
          jobs = [newJob, ...jobs];
        } else {
          pendingJobs = [newJob, ...pendingJobs];
          newJobsCount = pendingJobs.length;
          showNewJobsNotification = true;
        }
      }
    }
  }

  async function handleScroll() {
    if (!jobsContainer || isLoading) return;

    const { scrollTop, scrollHeight, clientHeight } = jobsContainer;
    isScrolledToTop = scrollTop <= 10;

    if (isScrolledToTop && showNewJobsNotification) {
      loadNewJobs();
    }

    const threshold = 150;
    const distanceFromBottom = scrollHeight - (scrollTop + clientHeight);

    if (distanceFromBottom <= threshold && hasMoreJobs && !isLoading) {
      await loadMoreJobs();
    }
  }

  function loadNewJobs() {
    jobs = [...pendingJobs, ...jobs];
    pendingJobs = [];
    newJobsCount = 0;
    showNewJobsNotification = false;

    if (jobsContainer) {
      jobsContainer.scrollTo({ top: 0, behavior: 'smooth' });
    }
  }

  async function loadMoreJobs() {
    isLoading = true;
    try {
      const newJobs = await getJobs(JOBS_CHUNK_SIZE, jobs.length);
      if (newJobs.length === 0) {
        hasMoreJobs = false;
      } else {
        jobs = [...jobs, ...newJobs];
      }
    } catch (err) {
      console.error('Failed to load more jobs:', err);
    } finally {
      isLoading = false;
    }
  }

  async function handleWorkerUpdated(event) {
    const { workerId, updates } = event.detail;
    try {
      await updateWorkerConfig(workerId, updates);
      workers = workers.map(w => w.workerId === workerId ? { ...w, ...updates } : w);
    } catch (err) {
      console.error('Worker update failed:', err);
    }
  }

</script>

<div class="dashboard-content" data-testid="dashboard-page">
  <div class="dashboard-worker-section">
    <WorkerCompo {workers} onWorkerUpdated={handleWorkerUpdated}/>
  </div>

  {#if successMessage}
    <div class="alert-success">{successMessage}</div>
  {/if}

  <div class="dashboard-bottom-div">
    <div class="dashboard-job-section" bind:this={jobsContainer} on:scroll={handleScroll}>
      {#if showNewJobsNotification}
        <button
          class="new-jobs-notification"
          on:click={loadNewJobs}
          aria-label={`Show ${newJobsCount} new job${newJobsCount > 1 ? 's' : ''}`}
          data-testid="new-jobs-notification"
        >
          {newJobsCount} new job{newJobsCount > 1 ? 's' : ''} available
          <span class="show-new-btn">Show</span>
        </button>
      {/if}

      <JobCompo {jobs} />

      {#if isLoading}
        <div class="loading-indicator">Loading...</div>
      {:else if !hasMoreJobs}
        <p class="workerCompo-empty-state">No more jobs.</p>
      {/if}
    </div>
    <div class="dashboard-add-worker-section">
      <CreateForm/>
    </div>
  </div>
</div>
