<script lang="ts">
  import Sidebar from './components/Sidebar.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import LoginPage from './pages/loginPage.svelte';
  import './app.css';

  let isSidebarVisible = true;
  let isLoggedIn = true;

  const toggleSidebar = () => {
    isSidebarVisible = !isSidebarVisible;
  };

  $: {
    if (isLoggedIn) {
      document.body.className = 'body-dashboard';
    } else {
      document.body.className = 'body-login';
    }
  }
</script>

{#if isLoggedIn}
  <div class="app-container {isSidebarVisible ? '' : 'sidebar-hidden'}">
    {#if isSidebarVisible}
      <Sidebar {isSidebarVisible} {toggleSidebar} />
    {/if}
    <div class="main-content">
      <Dashboard {toggleSidebar} />
    </div>
  </div>
{:else}
  <LoginPage />
{/if}
