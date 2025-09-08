<script lang="ts">
  import { Home, ListChecks, Package, Settings, Power, ChevronDown, SunMoon, Copy } from 'lucide-svelte';
  import '../styles/dashboard.css';
  import { theme } from '../lib/Stores/theme';
  import { logout } from '../lib/auth';
  import { uiVersion } from '../version';

  /**
   * State variables for controlling submenu visibility
   * @type {boolean}
   */
  let tasksOpen = false;
  let awaitingExecutionOpen = false;
  let inProgressOpen = false;
  let successfulTasksOpen = false;
  let failsTasksOpen = false;
  let inactiveTasksOpen = false;

  /**
   * Controls visibility of logout confirmation popup
   * @type {boolean}
   */
  let showLogoutPopup = false;

  /**
   * Toggles between dark and light theme
   * Updates the theme store
   * @returns {void}
   */
  function toggleTheme(): void {
    theme.update(current => current === 'dark' ? 'light' : 'dark');
  }

  /**
   * Shows the logout confirmation popup
   * @returns {void}
   */
  function confirmLogout(): void {
    showLogoutPopup = true;
  }

  /**
   * Cancels the logout process by hiding the confirmation popup
   * @returns {void}
   */
  function cancelLogout(): void {
    showLogoutPopup = false;
  }

  /**
   * Performs user logout and closes the confirmation popup
   * @async
   * @returns {Promise<void>}
   */
  async function performLogout(): Promise<void> {
    await logout();
    showLogoutPopup = false;
  }

  /**
   * Toggles the visibility of a specified submenu
   * @param {string} submenu - The submenu to toggle
   * Valid values: 'awaitingExecution', 'inProgress', 'successfulTasks', 'failsTasks', 'inactiveTasks'
   * @returns {void}
   */
  function toggleSubMenu(submenu: string): void {
    switch(submenu) {
      case 'awaitingExecution':
        awaitingExecutionOpen = !awaitingExecutionOpen;
        break;
      case 'inProgress':
        inProgressOpen = !inProgressOpen;
        break;
      case 'successfulTasks':
        successfulTasksOpen = !successfulTasksOpen;
        break;
      case 'failsTasks':
        failsTasksOpen = !failsTasksOpen;
        break;
      case 'inactiveTasks':
        inactiveTasksOpen = !inactiveTasksOpen;
        break;
    }
  }
</script>

<!-- Dashboard sidebar container -->
<aside class="dashboard-sidebar" aria-label="Main navigation">
  <!-- Sidebar header with version info -->
  <div class="dashboard-sidebar-header">
    <span>SCITQ2</span>
    <span class="dashboard-sidebar-version">{uiVersion}</span>
  </div>

  <!-- Main navigation menu -->
  <nav class="dashboard-sidebar-nav">
    <!-- Dashboard link -->
    <a class="dashboard-nav-link" href="#/" data-testid="dashboard-link">
      <Home class="dashboard-icon" aria-hidden="true" /> 
      Dashboard
    </a>

    <!-- Tasks section with expandable submenu -->
    <div class="dashboard-nav-section">
      <div class="dashboard-nav-link-container">
        <a href="#/tasks" class="dashboard-nav-link" data-testid="tasks-button">
          <div class="dashboard-left">
            <ListChecks class="dashboard-icon" aria-hidden="true" />
            Tasks
          </div>
        </a>
        <button 
          data-testid="tasks-chevron" 
          class="dashboard-chevron-button {tasksOpen ? 'rotate' : ''}" 
          on:click={() => (tasksOpen = !tasksOpen)}
          aria-expanded={tasksOpen}
          aria-controls="task-submenu"
        >
          <ChevronDown class="dashboard-chevron" aria-hidden="true" />
        </button>
      </div>

      <!-- Tasks submenu -->
      {#if tasksOpen}
        <div class="dashboard-submenu" id="task-submenu">
          <!-- Awaiting Execution section -->
          <div>
            <button 
              class="dashboard-submenu-header" 
              data-testid="starting-button" 
              on:click={() => toggleSubMenu('awaitingExecution')}
              aria-expanded={awaitingExecutionOpen}
            >
              Starting
              <ChevronDown class="dashboard-chevron {awaitingExecutionOpen ? 'rotate' : ''}" aria-hidden="true" />
            </button>
            {#if awaitingExecutionOpen}
              <div class="dashboard-submenu-items">
                <a data-testid="pending-link" href="#/tasks?status=P">Pending</a>
                <a href="#/tasks?status=A">Assigned</a>
                <a href="#/tasks?status=C">Accepted</a>
              </div>
            {/if}
          </div>

          <!-- In Progress section -->
          <div>
            <button 
              class="dashboard-submenu-header" 
              on:click={() => toggleSubMenu('inProgress')}
              aria-expanded={inProgressOpen}
            >
              Progress
              <ChevronDown class="dashboard-chevron {inProgressOpen ? 'rotate' : ''}" aria-hidden="true" />
            </button>
            {#if inProgressOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=D">Downloading</a>
                <a href="#/tasks?status=R">Running</a>
              </div>
            {/if}
          </div>

          <!-- Successful Tasks section -->
          <div>
            <button 
              class="dashboard-submenu-header" 
              on:click={() => toggleSubMenu('successfulTasks')}
              aria-expanded={successfulTasksOpen}
            >
              Success
              <ChevronDown class="dashboard-chevron {successfulTasksOpen ? 'rotate' : ''}" aria-hidden="true" />
            </button>
            {#if successfulTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=U">Uploading (after success)</a>
                <a href="#/tasks?status=S">Succeeded</a>
              </div>
            {/if}
          </div>

          <!-- Failed Tasks section -->
          <div>
            <button 
              class="dashboard-submenu-header" 
              on:click={() => toggleSubMenu('failsTasks')}
              aria-expanded={failsTasksOpen}
            >
              Fail
              <ChevronDown class="dashboard-chevron {failsTasksOpen ? 'rotate' : ''}" aria-hidden="true" />
            </button>
            {#if failsTasksOpen}
              <div class="dashboard-submenu-items">
                <a href="#/tasks?status=V">Uploading (after failure)</a>
                <a href="#/tasks?status=F">Failed</a>
              </div>
            {/if}
          </div>

          <!-- Inactive Tasks section -->
          <div>
            <button 
              class="dashboard-submenu-header" 
              on:click={() => toggleSubMenu('inactiveTasks')}
              aria-expanded={inactiveTasksOpen}
            >
              Inactive
              <ChevronDown class="dashboard-chevron {inactiveTasksOpen ? 'rotate' : ''}" aria-hidden="true" />
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

    <!-- Workflows link -->
    <a class="dashboard-nav-link" href="#/workflows" data-testid="workflows-link">
      <Package class="dashboard-icon" aria-hidden="true" /> 
      Workflows
    </a>

    <!-- Templates link -->
    <a class="dashboard-nav-link" href="#/workflowsTemplate" data-testid="templates-link">
      <Copy class="dashboard-icon" aria-hidden="true" /> 
      Templates
    </a>

    <!-- Settings link -->
    <a class="dashboard-nav-link" href="#/settings" data-testid="settings-link">
      <Settings class="dashboard-icon" aria-hidden="true" /> 
      Settings
    </a>

    <!-- Logout button -->
    <button 
      class="dashboard-nav-link dashboard-logout" 
      data-testid="logout-button" 
      on:click={confirmLogout}
    >
      <Power class="dashboard-icon" aria-hidden="true" /> 
      Log Out
    </button>
  </nav>

  <!-- Theme toggle button -->
  <div class="theme-toggle-container">
    <button 
      on:click={toggleTheme} 
      class="theme-toggle" 
      title="Toggle theme" 
      data-testid="theme-button"
      aria-label="Toggle color theme"
    >
      {#if $theme === 'dark'}
        <SunMoon class="theme-icon" color="#fbbf24" aria-hidden="true" />
      {:else}
        <SunMoon class="theme-icon" color="#9ca3af" aria-hidden="true" />
      {/if}
    </button>
  </div>
</aside>

<!-- Logout confirmation popup -->
{#if showLogoutPopup}
  <div class="dashboard-popup-overlay" role="dialog" aria-modal="true">
    <div class="dashboard-popup">
      <p>Are you sure you want to log out?</p>
      <div class="dashboard-popup-actions">
        <button 
          class="dashboard-confirm" 
          on:click={performLogout}
          data-testid="confirm-logout-button"
        >
          Log out
        </button>
        <button 
          class="dashboard-cancel" 
          on:click={cancelLogout}
          data-testid="cancel-logout-button"
        >
          Cancel
        </button>
      </div>
    </div>
  </div>
{/if}