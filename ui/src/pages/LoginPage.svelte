<script>
  import { onMount } from 'svelte';
  import LoginForm from '../components/LoginForm.svelte';
  import { branding } from '../lib/branding';
  import '../styles/loginPage.css';
  const { name: brandName, logo: brandLogo } = branding();
</script>

<!-- 
 * Main container for the login page layout
 * @type {HTMLElement}
 -->
<div class="loginpage-page-wrapper" data-testid="login-page">

  <!-- 
   * Header section containing site title and branding elements
   * @type {HTMLElement}
   -->
  <div class="loginpage-header_login">
    <div class="top-left" data-testid="brand-name">{brandName}</div>
    <div class="loginpage-top-right">
      <span></span>
      <img src={brandLogo} alt="Logo" class="loginpage-login-page-logo" data-testid="brand-logo" />
    </div>
  </div>

  <!-- 
   * LoginForm component instance
   * @component
   -->
  <LoginForm />
</div>