<script lang="ts">
  export let toggleSidebar: () => void; // Function passed from App.svelte
  import WorkerCompo from '../components/WorkerCompo.svelte';
  import CreateForm from '../components/CreateForm.svelte';
  import JobCompo from '../components/JobsCompo.svelte';
  import '../styles/dashboard.css';
</script>

<div class="dashboard-content">
  <!-- Hamburger button for toggling the sidebar, with accessibility label -->
  <button class="hamburger-button" on:click={toggleSidebar} aria-label="Toggle sidebar">
    <span class="hamburger-icon"></span>
    <span class="hamburger-icon"></span>
    <span class="hamburger-icon"></span>
  </button>

  <div class="header">
    <h1>Welcome to SCITQ2</h1>
  </div>

  <!-- Section displaying workers -->
  <div class="worker-section">
    <WorkerCompo />
  </div>

  <div class="bottom-div">
    <!-- Section displaying job status/info -->
    <div class="job-section">
      <JobCompo />
    </div>
    
    <!-- Section for adding a new worker -->
    <div class="add-worker-section">
      <CreateForm />
    </div>
  </div>
</div>
