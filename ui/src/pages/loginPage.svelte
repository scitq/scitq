<script>
    import { onMount } from 'svelte';
    import LoginForm from '../components/LoginForm.svelte';
    import logo from '../assets/icons/logoGMT.png';
    import '../styles/loginPage.css';
  
    onMount(() => {
      const token = localStorage.getItem('token');
      if (token) {
        goto('/dashboard');
      }
    });
  </script>
  
  <!-- General wrapper -->
  <div class="page-wrapper">
  
    <!-- Headers -->
    <div class="header_login">
      <div class="top-left">SCITQ2</div>
      <div class="top-right">
        <span>GMT SCIENCE</span>
        <img src="{logo}" alt="Logo" class="login-page-logo" />
      </div>
    </div>
  
    <LoginForm />
  </div>
  