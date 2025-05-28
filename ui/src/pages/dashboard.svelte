<script lang="ts">
  import { onMount } from 'svelte';
  import { getWorkers, updateWorkerConfig } from '../lib/api';
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
   * Event handler for when a new worker is added.
   * Adds the new worker to the local workers array to update the UI.
   * 
   * @param event - The custom event containing the new worker data in `event.detail`.
   */
  function onWorkerAdded(event) {
    const newWorker = event.detail;
    // Add the new worker to the local list
    workers = [...workers, newWorker];

    successMessage = "Worker Added";

    clearTimeout(alertTimeout);
    alertTimeout = setTimeout(() => {
      successMessage = '';
    }, 5000);
  }

  /**
   * Event handler for when a worker is updated.
   * Finds the worker in the local array and updates their data.
   * Triggers Svelte's reactivity by creating a new array reference.
   * 
   * @param event - The custom event containing updated worker data in `event.detail`.
   */
  async function handleWorkerUpdated(event) {
    const { workerId, updates } = event.detail;
    
    try {
      await updateWorkerConfig(workerId, updates);
      
      // Local state update
      workers = workers.map(w => 
        w.workerId === workerId ? { ...w, ...updates } : w
      );
      
    } catch (error) {
      console.error("Worker update failed:", error);
    }
  }

</script>



<div class="dashboard-content">

  <div class="dashboard-header">
    <h1>Welcome to SCITQ2</h1>
  </div>

  <div class="dashboard-worker-section">
    <WorkerCompo {workers} onWorkerUpdated={handleWorkerUpdated} />
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