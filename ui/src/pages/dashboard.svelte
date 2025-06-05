<script lang="ts">
  import { onMount } from 'svelte';
  import { getWorkers, updateWorkerConfig, delWorker } from '../lib/api';
  import WorkerCompo from '../components/WorkerCompo.svelte';
  import CreateForm from '../components/CreateForm.svelte';
  import JobCompo from '../components/JobsCompo.svelte';
  import '../styles/dashboard.css';

  let workers = [];

  // Success message and timeout handler
  let successMessage: string = '';
  let alertTimeout;

  /**
   * Fetches the list of workers when the component is mounted.
   * Updates the local `workers` array with the fetched data.
   */
  onMount(async () => {
    workers = await getWorkers();
  });

  /**
   * Event handler called when a new worker is added.
   * Adds the new worker to the current workers list.
   * Displays a success message for 5 seconds.
   * 
   * @param event - Custom event containing the new worker in `event.detail.worker`.
   */
  function onWorkerAdded(event) {
    const newWorker = event.detail.worker;
    workers = [...workers, newWorker];

    successMessage = "Worker Added";

    clearTimeout(alertTimeout);
    alertTimeout = setTimeout(() => {
      successMessage = '';
    }, 5000);
  }

  /**
   * Event handler called when a worker is updated.
   * Calls the API to update worker configuration.
   * Updates the local workers array to reflect the changes.
   * Logs an error if the update fails.
   * 
   * @param event - Custom event containing `workerId` and update data in `event.detail`.
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
   * Event handler called when a worker is deleted.
   * Removes the worker from the local list and calls API to delete it.
   * Shows a success or failure message for 5 seconds.
   * Logs an error if deletion fails.
   * 
   * @param event - Custom event containing the `workerId` in `event.detail`.
   */
  async function handleWorkerDeleted(event) {
    const workerId = event.detail.workerId;

    try {
      workers = workers.filter(worker => worker.workerId !== workerId);
      await delWorker({ workerId });

      successMessage = "Worker Deleted";
      clearTimeout(alertTimeout); 
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
    } catch (error) {
      console.error("Worker delete failed:", error);
      successMessage = "Failed to delete worker";
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
      <JobCompo />
    </div>
    <div class="dashboard-add-worker-section">
      <CreateForm onWorkerCreated={onWorkerAdded} />
    </div>
  </div>
</div>
