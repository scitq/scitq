<script lang="ts">
  import { onMount } from 'svelte';
  import { getWorkers, getJobs, updateWorkerConfig, delWorker, delJob } from '../lib/api';
  import WorkerCompo from '../components/WorkerCompo.svelte';
  import CreateForm from '../components/CreateForm.svelte';
  import JobCompo from '../components/JobsCompo.svelte';
  import '../styles/dashboard.css';

  let workers = [];
  let jobs = [];

  // Success message and timeout handler
  let successMessage: string = '';
  let alertTimeout;

  /**
   * Fetches and initializes workers and jobs data when component is mounted.
   * @async
   * @returns {Promise<void>} Resolves when both workers and jobs data are loaded
   * @throws {Error} If either workers or jobs data fetching fails
   */
  onMount(async () => {
    workers = await getWorkers();
    jobs = await getJobs();
  });

  /**
   * Handles worker and job creation event.
   * Adds new worker to workers list and new job to jobs list.
   * Displays success message for 5 seconds.
   * @param {Object} event - The event object
   * @param {WorkerDetails} event.detail.worker - The newly created worker details
   * @param {Partial<Job>} event.detail.job - The associated job creation record
   * @returns {void}
   */
  function onWorkerAdded(event) {
    const newWorker = event.detail.worker;
    workers = [...workers, newWorker];
    const newJob = event.detail.job;
    jobs = [...jobs, newJob];

    successMessage = "Worker/Job Added";

    clearTimeout(alertTimeout);
    alertTimeout = setTimeout(() => {
      successMessage = '';
    }, 5000);
  }

  /**
   * Handles worker configuration updates.
   * Calls API to persist changes and updates local state optimistically.
   * @async
   * @param {Object} event - The event object
   * @param {string} event.detail.workerId - ID of worker to update
   * @param {Partial<WorkerDetails>} event.detail.updates - Configuration changes
   * @returns {Promise<void>}
   * @throws {Error} If worker update fails
   */
  async function handleWorkerUpdated(event) {
    const { workerId, updates } = event.detail;
    
    try {
      await updateWorkerConfig(workerId, updates);
      workers = workers.map(w => 
        w.workerId === workerId ? { ...w, ...updates } : w
      );
    } catch (error) {
      console.error("Worker update failed:", error);
    }
  }

  /**
   * Handles worker deletion workflow.
   * Removes worker from local state, calls deletion API, and creates deletion job record.
   * Manages success/error messaging with 5 second timeout.
   * @async
   * @param {Object} event - The event object
   * @param {string} event.detail.workerId - ID of worker to delete
   * @returns {Promise<void>}
   * @throws {Error} If deletion process fails
   */
  async function handleWorkerDeleted(event) {
    const workerId = event.detail.workerId;

    try {
      workers = workers.filter(worker => worker.workerId !== workerId);
      const jobId = await delWorker({ workerId });
      const newJob: Job = {
        jobId: jobId,
        action: 'D',
        status: 'P',
        workerId: null,
        modifiedAt: new Date().toISOString(),
      }
      jobs = [...jobs, newJob];

      successMessage = "Worker deleted / Job Created";
      clearTimeout(alertTimeout); 
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
      
    } catch (error) {
      console.error("Worker deletion failed:", error);
      successMessage = "Failed to delete worker";
      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
    }
  }

  /**
   * Handles job deletion process.
   * Removes job from local state and calls deletion API.
   * Manages success/error messaging with 5 second timeout.
   * @async
   * @param {Object} event - The event object
   * @param {string} event.detail.jobId - ID of job to delete
   * @returns {Promise<void>}
   * @throws {Error} If job deletion fails
   */
  async function handleJobDeleted(event) {
    const jobId = event.detail.jobId;

    try {
      jobs = jobs.filter(job => job.jobId !== jobId);
      await delJob({ jobId });

      successMessage = "Job Deleted";
      clearTimeout(alertTimeout); 
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
    } catch (error) {
      console.error("Job delete failed:", error);
      successMessage = "Failed to delete job";
      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
    }
  }
</script>

<div class="dashboard-content" data-testid="dashboard-page">

  <div class="dashboard-worker-section">
    <WorkerCompo {workers} onWorkerUpdated={handleWorkerUpdated} onWorkerDeleted={handleWorkerDeleted} />
  </div>

  <!-- Success message alert -->
  {#if successMessage}
    <div class="alert-success">
      {successMessage}
    </div>
  {/if}

  <div class="dashboard-bottom-div">
    <div class="dashboard-job-section">
      <JobCompo {jobs} onJobDeleted={handleJobDeleted} />
    </div>
    <div class="dashboard-add-worker-section">
      <CreateForm onWorkerCreated={onWorkerAdded} />
    </div>
  </div>
</div>
