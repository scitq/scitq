<script lang="ts">
  import { Home, ListChecks, Package, Settings, Power, ChevronDown } from 'lucide-svelte';
  import logo from '../assets/icons/logoGMT.png';
  import '../styles/dashboard.css';

  let tasksOpen = false;
  let awaitingExecutionOpen = false;
  let inProgressOpen = false;
  let successfulTasksOpen = false;
  let failsTasksOpen = false;

  // Toggle pour ouvrir/fermer les sous-menus
  const toggleSubMenu = (submenu: string) => {
    if (submenu === 'awaitingExecution') awaitingExecutionOpen = !awaitingExecutionOpen;
    if (submenu === 'inProgress') inProgressOpen = !inProgressOpen;
    if (submenu === 'successfulTasks') successfulTasksOpen = !successfulTasksOpen;
    if (submenu === 'failsTasks') failsTasksOpen = !failsTasksOpen;
  };
</script>

<aside class="sidebar">
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
      <!-- Toggle pour le menu déroulant des tâches -->
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
          <!-- Groupe "Tasks awaiting execution" -->
          <div>
            <button class="submenu-header" on:click={() => toggleSubMenu('awaitingExecution')}>
               Starting <ChevronDown class="chevron" />
            </button>
            {#if awaitingExecutionOpen}
              <div class="submenu-items">
                <a href="#">Pending</a>
                <a href="#">Assigned</a>
                <a href="#">Accepted</a>
              </div>
            {/if}
          </div>

          <!-- Groupe "Tasks in progress" -->
          <div>
            <button class="submenu-header" on:click={() => toggleSubMenu('inProgress')}>
              Progress <ChevronDown class="chevron" />
            </button>
            {#if inProgressOpen}
              <div class="submenu-items">
                <a href="#">Downloading</a>
                <a href="#">Waiting</a>
                <a href="#">Running</a>
              </div>
            {/if}
          </div>

          <!-- Groupe "Successful tasks" -->
          <div>
            <button class="submenu-header" on:click={() => toggleSubMenu('successfulTasks')}>
              Successful <ChevronDown class="chevron" />
            </button>
            {#if successfulTasksOpen}
              <div class="submenu-items">
                <a href="#">Uploading (after success)</a>
                <a href="#">Succeeded</a>
              </div>
            {/if}
          </div>
          <!-- Groupe "Failed tasks" -->
          <div>
            <button class="submenu-header" on:click={() => toggleSubMenu('failsTasks')}>
              Fails <ChevronDown class="chevron" />
            </button>
            {#if failsTasksOpen}
              <div class="submenu-items">
                <a href="#">Uploading (after failure)</a>
                <a href="#">Suspended</a>
                <a href="#">Canceled</a>
              </div>
            {/if}
          </div>
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