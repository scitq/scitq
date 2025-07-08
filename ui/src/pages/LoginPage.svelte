<script>
  import { onMount } from 'svelte';
  import LoginForm from '../components/LoginForm.svelte';
  import logo from '../assets/icons/logoGMT.png';
  import '../styles/loginPage.css';
</script>

<!--
  General wrapper for the login page layout
-->
<div class="loginpage-page-wrapper" data-testid="login-page">

  <!--
    Header section with site title and branding
  -->
  <div class="loginpage-header_login">
    <div class="top-left">SCITQ2</div>
    <div class="loginpage-top-right">
      <span>GMT SCIENCE</span>
      <img src="{logo}" alt="Logo" class="loginpage-login-page-logo" />
    </div>
  </div>

  <!-- Login form component -->
  <LoginForm />
</div>
