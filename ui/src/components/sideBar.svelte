<script lang="ts">
  import { Home, ListChecks, Package, Settings, Power, ChevronDown } from 'lucide-svelte';
  import logo from '../assets/icons/logoGMT.png';
  import '../styles/dashboard.css';

  export let isSidebarVisible: boolean;
  export let toggleSidebar: () => void;

  let tasksOpen = false;
</script>

<aside class={`sidebar`}>
  <div class="sidebar-header">
    <img src={logo} alt="Logo" class="logo" />
    <span>SCITQ2</span>
  </div>

  <nav class="sidebar-nav">
    <!-- Dashboard navigation -->
    <a class="nav-link" href="#">
      <Home class="icon" /> Dashboard
    </a>

    <div class="nav-section">
      <!-- Toggle for the task submenu -->
      <button
        class="nav-link dropdown"
        aria-expanded={tasksOpen}
        aria-controls="task-submenu"
        on:click={() => (tasksOpen = !tasksOpen)}
      >
        <div class="left">
          <ListChecks class="icon" />
          Tasks
        </div>
        <ChevronDown class="chevron" />
      </button>

      {#if tasksOpen}
        <div class="submenu" id="task-submenu">
          <a href="#">Pending</a>
          <a href="#">Assigned</a>
          <a href="#">Accepted</a>
          <a href="#">Running</a>
          <a href="#">Failed</a>
          <a href="#">Succeeded</a>
        </div>
      {/if}
    </div>

    <!-- Batch navigation -->
    <a class="nav-link" href="#">
      <Package class="icon" /> Batch
    </a>

    <!-- Settings navigation -->
    <a class="nav-link" href="#">
      <Settings class="icon" /> Settings
    </a>

    <!-- Log out navigation -->
    <a class="nav-link logout" href="#">
      <Power class="icon" /> Log out
    </a>
  </nav>
</aside>
