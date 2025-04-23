<script lang="ts">
    export let toggleSidebar: () => void;  // Fonction venant de App.svelte
    import WorkerCompo from '../components/workerCompo.svelte'
    import '../styles/dashboard.css'
  </script>
  
  <div class="dashboard-content">
  <!-- Bouton Hamburger avec aria-label pour l'accessibilité -->
    <button class="hamburger-button" on:click={toggleSidebar} aria-label="Toggle sidebar">
        <span class="hamburger-icon"></span>
        <span class="hamburger-icon"></span>
        <span class="hamburger-icon"></span>
    </button>
  
    <div class="header">
      <h1>Welcome to SCITQ2</h1>
    </div>
  
    <div class="worker-section">
      <WorkerCompo />
    </div>
  </div>
  