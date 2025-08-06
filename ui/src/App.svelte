<script lang="ts">
  import Sidebar from './components/SideBar.svelte';
  import LoginPage from './pages/LoginPage.svelte';
  import './app.css';

  import { theme } from './lib/Stores/theme';
  import { getToken } from './lib/auth';
  import { isLoggedIn } from './lib/Stores/user';
  import { onMount, onDestroy } from 'svelte';
  import { wsClient } from './lib/wsClient';
  import Router from 'svelte-spa-router';

  // Pages
  import Dashboard from './pages/Dashboard.svelte';
  import SettingPage from './pages/SettingPage.svelte';
  import TaskPage from './pages/TaskPage.svelte';
  import WorkflowPage from './pages/WorkflowPage.svelte';
  import WfTemplatePage from './pages/WfTemplatePage.svelte';

  let isSidebarVisible = true;
  let logged = false;

  $: $isLoggedIn;
  $: logged = $isLoggedIn;

  const toggleSidebar = () => {
    isSidebarVisible = !isSidebarVisible;
  };

  onMount(async () => {
    wsClient.connect();
    document.documentElement.setAttribute('data-theme', $theme);
    const tokenCookie = await getToken(); 
    if (tokenCookie) {
      isLoggedIn.set(true);
    }
  });

  $: {
    document.body.className = logged ? 'body-dashboard' : 'body-login';
  }

  const routes = {
    '/': Dashboard,
    '/settings': SettingPage,
    '/tasks': TaskPage,
    '/workflows': WorkflowPage,
    '/workflowsTemplate': WfTemplatePage
  };
</script>

{#if logged}
  <div class="app-container {isSidebarVisible ? '' : 'sidebar-hidden'}">
    {#if isSidebarVisible}
      <Sidebar {isSidebarVisible} {toggleSidebar} />
    {/if}
    <div class="main-content">
      <!-- The button is now to the right of the sidebar -->
      <button class="hamburger-button" on:click={toggleSidebar} aria-label="Toggle sidebar">
        <span class="hamburger-icon"></span>
        <span class="hamburger-icon"></span>
        <span class="hamburger-icon"></span>
      </button>
      <Router {routes} />
    </div>
  </div>
{:else}
  <LoginPage />
{/if}
