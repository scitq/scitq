<script lang="ts">
  import {
    Home,
    ListChecks,
    Package,
    Settings,
    Power,
    ChevronDown
  } from 'lucide-svelte';
  import logo from '../assets/icons/logoGMT.png';
  import '../styles/dashboard.css';

  import { isLoggedIn } from '../lib/Stores/user';
  import { logout } from '../lib/auth';
  import Router from 'svelte-spa-router';
  import SettingPage from '../pages/SettingPage.svelte';

  let tasksOpen = false;
  let awaitingExecutionOpen = false;
  let inProgressOpen = false;
  let successfulTasksOpen = false;
  let failsTasksOpen = false;

  let showLogoutPopup = false;

  const routes = {
    '/settings': SettingPage
  };

  function confirmLogout() {
    showLogoutPopup = true;
  }

  function cancelLogout() {
    showLogoutPopup = false;
  }

  function performLogout() {
    logout();
    showLogoutPopup = false;
  }

  // Toggle the visibility of a submenu
  function toggleSubMenu(submenu: string) {
    if (submenu === 'awaitingExecution') awaitingExecutionOpen = !awaitingExecutionOpen;
    if (submenu === 'inProgress') inProgressOpen = !inProgressOpen;
    if (submenu === 'successfulTasks') successfulTasksOpen = !successfulTasksOpen;
    if (submenu === 'failsTasks') failsTasksOpen = !failsTasksOpen;
  }
</script>

<aside class="dashboard-sidebar">
  <div class="dashboard-sidebar-header">
    <img src={logo} alt="Logo" class="dashboard-logo" />
    <span>SCITQ2</span>
  </div>

  <nav class="dashboard-sidebar-nav">
    <a class="dashboard-nav-link" href="#">
      <Home class="dashboard-icon" /> Dashboard
    </a>

    <div class="dashboard-nav-section">
      <button
        class="dashboard-nav-link dropdown"
        aria-expanded={tasksOpen}
        aria-controls="task-submenu"
        on:click={() => (tasksOpen = !tasksOpen)}
      >
        <div class="dashboard-left">
          <ListChecks class="dashboard-icon" />
          Tasks
        </div>
        <ChevronDown class="dashboard-chevron {tasksOpen ? 'rotate' : ''}" />
      </button>

      {#if tasksOpen}
        <div class="dashboard-submenu" id="task-submenu">
          <!-- Awaiting Execution -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('awaitingExecution')}>
              Starting
              <ChevronDown class="dashboard-chevron {awaitingExecutionOpen ? 'rotate' : ''}" />
            </button>
            {#if awaitingExecutionOpen}
              <div class="dashboard-submenu-items">
                <a href="#">Pending</a>
                <a href="#">Assigned</a>
                <a href="#">Accepted</a>
              </div>
            {/if}
          </div>

          <!-- In Progress -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('inProgress')}>
              Progress
              <ChevronDown class="dashboard-chevron {inProgressOpen ? 'rotate' : ''}" />
            </button>
            {#if inProgressOpen}
              <div class="dashboard-submenu-items">
                <a href="#">Downloading</a>
                <a href="#">Waiting</a>
                <a href="#">Running</a>
              </div>
            {/if}
          </div>

          <!-- Successful -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('successfulTasks')}>
              Successful
              <ChevronDown class="dashboard-chevron {successfulTasksOpen ? 'rotate' : ''}" />
            </button>
            {#if successfulTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#">Uploading (after success)</a>
                <a href="#">Succeeded</a>
              </div>
            {/if}
          </div>

          <!-- Fails -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('failsTasks')}>
              Fails
              <ChevronDown class="dashboard-chevron {failsTasksOpen ? 'rotate' : ''}" />
            </button>
            {#if failsTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#">Uploading (after failure)</a>
                <a href="#">Suspended</a>
                <a href="#">Canceled</a>
              </div>
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <a class="dashboard-nav-link" href="#">
      <Package class="dashboard-icon" /> Batch
    </a>

    <a class="dashboard-nav-link" href="#/settings">
      <Settings class="dashboard-icon" /> Settings
    </a>

    <button class="dashboard-nav-link dashboard-logout" data-testid="logout-button" on:click={confirmLogout}>
      <Power class="dashboard-icon" /> Log Out
    </button>
  </nav>
</aside>

{#if showLogoutPopup}
  <div class="dashboard-popup-overlay">
    <div class="dashboard-popup">
      <p>Are you sure you want to log out?</p>
      <div class="dashboard-popup-actions">
        <button class="dashboard-confirm" on:click={performLogout}>Log out</button>
        <button class="dashboard-cancel" on:click={cancelLogout}>Cancel</button>
      </div>
    </div>
  </div>
{/if}
