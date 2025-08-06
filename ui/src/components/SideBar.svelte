<script lang="ts">
  import { Home, ListChecks, Package, Settings, Power, ChevronDown, SunMoon, Copy} from 'lucide-svelte';
  import logo from '../assets/icons/logoGMT.png';
  import '../styles/dashboard.css';
  import { theme } from '../lib/Stores/theme';
  import { isLoggedIn } from '../lib/Stores/user';
  import { logout } from '../lib/auth';
  import Router from 'svelte-spa-router';
  import Dashboard from '../pages/Dashboard.svelte';
  import SettingPage from '../pages/SettingPage.svelte';
  import TaskPage from '../pages/TaskPage.svelte';
  import WorkflowPage from '../pages/WorkflowPage.svelte';
  import WfTemplatePage from '../pages/WfTemplatePage.svelte';

  let tasksOpen = false;
  let awaitingExecutionOpen = false;
  let inProgressOpen = false;
  let successfulTasksOpen = false;
  let failsTasksOpen = false;
  let inactiveTasksOpen = false;

  let showLogoutPopup = false;

  const routes = {
    '/dashboard' : Dashboard,
    '/settings': SettingPage,
    '/tasks' : TaskPage,
    '/worflows' : WorkflowPage,
    '/workflowsTemplate' : WfTemplatePage
  };

  function toggleTheme() {
    theme.update(current => current === 'dark' ? 'light' : 'dark');
  }

  /**
   * Shows the logout confirmation popup.
   * @returns {void}
   */
  function confirmLogout() {
    showLogoutPopup = true;
  }

  /**
   * Cancels the logout process by closing the popup.
   * @returns {void}
   */
  function cancelLogout() {
    showLogoutPopup = false;
  }

  /**
   * Logs out the user and closes the confirmation popup.
   * @returns {void}
   */
  function performLogout() {
    logout();
    showLogoutPopup = false;
  }

  /**
   * Toggles the visibility of a specified submenu.
   * @param {string} submenu - The identifier of the submenu to toggle.
   * Can be one of 'awaitingExecution', 'inProgress', 'successfulTasks', 'failsTasks' or 'inactiveTasks'.
   * @returns {void}
   */
  function toggleSubMenu(submenu: string) {
    if (submenu === 'awaitingExecution') awaitingExecutionOpen = !awaitingExecutionOpen;
    if (submenu === 'inProgress') inProgressOpen = !inProgressOpen;
    if (submenu === 'successfulTasks') successfulTasksOpen = !successfulTasksOpen;
    if (submenu === 'failsTasks') failsTasksOpen = !failsTasksOpen;
    if (submenu === 'inactiveTasks') inactiveTasksOpen = !inactiveTasksOpen;
  }
</script>

<aside class="dashboard-sidebar">
  <div class="dashboard-sidebar-header">
    <span>SCITQ2</span>
    <span class="dashboard-sidebar-version">V2.0.0</span>
  </div>

  <nav class="dashboard-sidebar-nav">
    <a class="dashboard-nav-link" href="#/">
      <Home class="dashboard-icon" /> Dashboard
    </a>

    <div class="dashboard-nav-section">
      <div class="dashboard-nav-link-container">
        <a href="#/tasks" class="dashboard-nav-link" data-testid="tasks-button">
          <div class="dashboard-left">
            <ListChecks class="dashboard-icon" />
            Tasks
          </div>
        </a>
        <button data-testid="tasks-chevron" class="dashboard-chevron-button {tasksOpen ? 'rotate' : ''}" 
                on:click={() => (tasksOpen = !tasksOpen)}>
          <ChevronDown class="dashboard-chevron" />
        </button>
      </div>

      {#if tasksOpen}
        <div class="dashboard-submenu" id="task-submenu">
          <!-- Awaiting Execution -->
          <div>
            <button class="dashboard-submenu-header" data-testid="starting-button" on:click={() => toggleSubMenu('awaitingExecution')}>
              Starting
              <ChevronDown class="dashboard-chevron {awaitingExecutionOpen ? 'rotate' : ''}" />
            </button>
            {#if awaitingExecutionOpen}
              <div class="dashboard-submenu-items">
                <a data-testid="pending-link" href="#/tasks?status=P">Pending</a>
                <a href="#/tasks?status=A">Assigned</a>
                <a href="#/tasks?status=C">Accepted</a>
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
                <a href="#/tasks?status=D">Downloading</a>
                <a href="#/tasks?status=R">Running</a>
              </div>
            {/if}
          </div>

          <!-- Successful -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('successfulTasks')}>
              Success
              <ChevronDown class="dashboard-chevron {successfulTasksOpen ? 'rotate' : ''}" />
            </button>
            {#if successfulTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=U">Uploading (after success)</a>
                <a href="#/tasks?status=S">Succeeded</a>
              </div>
            {/if}
          </div>

          <!-- Fails -->
          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('failsTasks')}>
              Fail
              <ChevronDown class="dashboard-chevron {failsTasksOpen ? 'rotate' : ''}" />
            </button>
            {#if failsTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=V">Uploading (after failure)</a>
                <a href="#/tasks?status=F">Failed</a>
              </div>
            {/if}
          </div>

          <div>
            <button class="dashboard-submenu-header" on:click={() => toggleSubMenu('inactiveTasks')}>
              Inactive
              <ChevronDown class="dashboard-chevron {inactiveTasksOpen ? 'rotate' : ''}" />
            </button>
            {#if inactiveTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=W">Waiting</a>
                <a href="#/tasks?status=Z">Suspended</a>
                <a href="#/tasks?status=X">Canceled</a>
              </div>
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <a class="dashboard-nav-link" href="#/workflows">
      <Package class="dashboard-icon" /> Workflows
    </a>

    <a class="dashboard-nav-link" href="#/workflowsTemplate">
      <Copy class="dashboard-icon" /> Templates
    </a>

    <a class="dashboard-nav-link" href="#/settings">
      <Settings class="dashboard-icon" /> Settings
    </a>

    <button class="dashboard-nav-link dashboard-logout" data-testid="logout-button" on:click={confirmLogout}>
      <Power class="dashboard-icon" /> Log Out
    </button>
  </nav>

  <div class="theme-toggle-container">
    <button on:click={toggleTheme} class="theme-toggle" title="Toggle theme" data-testid="theme-button">
      {#if $theme === 'dark'}
        <SunMoon class="theme-icon" color="#fbbf24" />
      {:else}
        <SunMoon class="theme-icon" color="#9ca3af" />
      {/if}
    </button>
  </div>

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
