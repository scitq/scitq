<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { getWorkers, getJobs, updateWorkerConfig, delWorker, delJob } from '../lib/api';
  import { wsClient } from '../lib/wsClient';
  import WorkerCompo from '../components/WorkerCompo.svelte';
  import CreateForm from '../components/CreateForm.svelte';
  import JobCompo from '../components/JobsCompo.svelte';
  import '../styles/dashboard.css';

  /**
   * Array of worker objects
   * @type {Array<Object>}
   */
  let workers = [];

  /**
   * Array of currently displayed jobs
   * @type {Array<Object>}
   */
  let jobs = [];

  /**
   * Array of pending jobs waiting to be loaded
   * @type {Array<Object>}
   */
  let pendingJobs = [];

  /**
   * Flag indicating if more jobs are available to load
   * @type {boolean}
   */
  let hasMoreJobs = true;

  /**
   * Loading state flag
   * @type {boolean}
   */
  let isLoading = false;

  /**
   * Reference to the jobs container DOM element
   * @type {HTMLDivElement}
   */
  let jobsContainer: HTMLDivElement;

  /**
   * Flag indicating if the jobs container is scrolled to top
   * @type {boolean}
   */
  let isScrolledToTop = true;

  /**
   * Count of new jobs available
   * @type {number}
   */
  let newJobsCount = 0;

  /**
   * Flag to show new jobs notification
   * @type {boolean}
   */
  let showNewJobsNotification = false;

  /**
   * Number of jobs to load at once
   * @type {number}
   * @constant
   */
  const JOBS_CHUNK_SIZE = 25;

  /**
   * Function to unsubscribe from WebSocket messages
   * @type {() => void}
   */
  let unsubscribeWS: () => void;

  /**
   * Success message to display
   * @type {string}
   */
  let successMessage: string = '';

  /**
   * Timeout reference for clearing messages
   * @type {NodeJS.Timeout}
   */
  let alertTimeout: NodeJS.Timeout;

  /**
   * Component lifecycle hook that runs on mount
   * Fetches initial data and subscribes to WebSocket messages
   * @async
   */
  onMount(async () => {
    workers = await getWorkers();
    jobs = await getJobs(JOBS_CHUNK_SIZE, 0);
    unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
  });

  /**
   * Component lifecycle hook that runs on destroy
   * Unsubscribes from WebSocket messages
   */
  onDestroy(() => {
    unsubscribeWS?.();
  });

  /**
   * Handles incoming WebSocket messages
   * @param {Object} message - The WebSocket message
   */
  function handleMessage(message) {
    // Handle job deletion
    if (message.type === 'job-deleted') {
      const payload = message.payload;
      if (!payload || typeof payload.jobId !== 'number') return;

      const idToRemove = payload.jobId;
      jobs = jobs.filter(j => j.jobId !== idToRemove);
      pendingJobs = pendingJobs.filter(j => j.jobId !== idToRemove);
    }

    // Handle worker creation
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

    // Handle worker deletion
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

  /**
   * Handles scroll events on jobs container
   * @async
   */
  async function handleScroll() {
    if (!jobsContainer || isLoading) return;

    const { scrollTop, scrollHeight, clientHeight } = jobsContainer;
    isScrolledToTop = scrollTop <= 20;

    if (isScrolledToTop && showNewJobsNotification) {
      loadNewJobs();
    }

    const threshold = 150;
    const distanceFromBottom = scrollHeight - (scrollTop + clientHeight);

    if (distanceFromBottom <= threshold && hasMoreJobs && !isLoading) {
      await loadMoreJobs();
    }
  }

  /**
   * Loads pending jobs into the main jobs list
   */
  function loadNewJobs() {
    jobs = [...pendingJobs, ...jobs];
    pendingJobs = [];
    newJobsCount = 0;
    showNewJobsNotification = false;

    if (jobsContainer) {
      jobsContainer.scrollTo({ top: 0, behavior: 'smooth' });
    }
  }

  /**
   * Loads additional jobs when scrolling near bottom
   * @async
   */
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

  /**
   * Handles worker configuration updates
   * @param {Object} event - The update event
   * @param {number} event.detail.workerId - ID of the worker to update
   * @param {Object} event.detail.updates - New configuration values
   * @async
   */
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

<!-- Main dashboard container -->
<div class="dashboard-content" data-testid="dashboard-page">
  <!-- Workers section -->
  <div class="dashboard-worker-section">
    <WorkerCompo {workers} onWorkerUpdated={handleWorkerUpdated}/>
  </div>

  <!-- Success message display -->
  {#if successMessage}
    <div class="alert-success">{successMessage}</div>
  {/if}

  <!-- Bottom section with jobs and create form -->
  <div class="dashboard-bottom-div">
    <!-- Jobs container with scroll handling -->
    <div class="dashboard-job-section" bind:this={jobsContainer} on:scroll={handleScroll} data-testid="jobs-container">
      <!-- New jobs notification -->
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

      <!-- Jobs component -->
      <JobCompo {jobs} />

      <!-- Loading indicators -->
      {#if isLoading}
        <div class="loading-indicator">Loading...</div>
      {:else if !hasMoreJobs}
        <p class="workerCompo-empty-state">No more jobs.</p>
      {/if}
    </div>

    <!-- Worker creation form section -->
    <div class="dashboard-add-worker-section">
      <CreateForm/>
    </div>
  </div>
</div>