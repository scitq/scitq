<script lang="ts">
  import Sidebar from './components/SideBar.svelte'; // Import Sidebar component
  import Dashboard from './pages/Dashboard.svelte'; // Import Dashboard page
  import LoginPage from './pages/LoginPage.svelte'; // Import Login page
  import './app.css'; // Import global styles

  let isSidebarVisible = true; // State: controls the visibility of the sidebar
  let isLoggedIn = true; // State: controls user authentication status

  // Function to toggle sidebar visibility
  const toggleSidebar = () => {
    isSidebarVisible = !isSidebarVisible;
  };

  // Reactive statement: update body class based on login state
  $: {
    if (isLoggedIn) {
      document.body.className = 'body-dashboard'; // Apply dashboard styles
    } else {
      document.body.className = 'body-login'; // Apply login page styles
    }
  }
</script>

{#if isLoggedIn}
  <!-- Render dashboard layout when logged in -->
  <div class="app-container {isSidebarVisible ? '' : 'sidebar-hidden'}">
    {#if isSidebarVisible}
      <Sidebar {isSidebarVisible} {toggleSidebar} /> <!-- Sidebar component -->
    {/if}
    <div class="main-content">
      <Dashboard {toggleSidebar} /> <!-- Main dashboard content -->
    </div>
  </div>
{:else}
  <!-- Render login page when not logged in -->
  <LoginPage />
{/if}
