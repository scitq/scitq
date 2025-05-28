<script lang="ts">
  import '../styles/loginForm.css';
  import { getLogin } from '../lib/auth';
  import { isLoggedIn } from '../lib/Stores/user';
  import { push } from 'svelte-spa-router';

  import { Eye, EyeOff } from 'lucide-svelte';

  let username = '';
  let password = '';
  let message = '';
  let isLoading = false;
  let showModal = false;
  let showPassword = false;

  // Handle user login
  async function handleLogin() {
    if (!username || !password) {
      message = "Please enter a username and password.";
      return;
    }

    isLoading = true;
    try {
      await getLogin(username, password);
      message = '';
      isLoggedIn.set(true);
      push('/');
    } catch (error) {
      console.error("Login error: ", error);
      message = "Login failed. Check your credentials.";
      isLoggedIn.set(false);
    } finally {
      isLoading = false;
    }
  }

  // Toggle password field visibility
  function togglePasswordVisibility() {
    showPassword = !showPassword;
  }
</script>

<div class="loginform-container">
  <h1 class="loginform-title">Login</h1>

  <div class="loginform-group">
    <label for="email" class="loginform-label">Username</label>
    <input id="email" type="email" class="loginform-input" bind:value={username} />
  </div>

  <div class="loginform-group">
    <label for="password" class="loginform-label">Password</label>
    <div class="password-input-wrapper">
      <input
        id="password"
        type={showPassword ? 'text' : 'password'}
        class="loginform-input password-input"
        bind:value={password}
      />
      <button
        type="button"
        aria-label={showPassword ? "Hide password" : "Show password"}
        on:click={togglePasswordVisibility}
        class="password-toggle-button"
      >
        {#if showPassword}
          <EyeOff size={20} />
        {:else}
          <Eye size={20} />
        {/if}
      </button>
    </div>
  </div>

  <button class="loginform-button btn-validate" on:click={handleLogin} disabled={isLoading}>
    {isLoading ? "Loading..." : "Log In"}
  </button>

  <div class="loginform-footer">
    <button type="button" class="loginform-forgot" on:click={() => showModal = true}>
      Forgot Password?
    </button>
  </div>

  {#if message}
    <p class="loginform-message">{message}</p>
  {/if}
</div>

{#if showModal}
  <div class="loginform-modal-overlay">
    <div class="loginform-modal-content">
      <h4>Please contact an administrator.</h4>
      <button
        type="button"
        class="loginform-modal-close-button btn-validate"
        on:click={() => showModal = false}
      >
        Close
      </button>
    </div>
  </div>
{/if}
