<script lang="ts">
  import '../styles/loginForm.css';              // Styles specific to the login form
  import { getLogin } from '../lib/auth';        // API call for authentication
  import { isLoggedIn } from '../lib/Stores/user'; // Svelte store for global auth state
  import { push } from 'svelte-spa-router';      // For navigation after login

  import { Eye, EyeOff } from 'lucide-svelte';   // Icons for showing/hiding password

  // Local state variables
  let username = '';
  let password = '';
  let message = '';          // Feedback message (error/info)
  let isLoading = false;     // Controls loading state of login button
  let showModal = false;     // Controls visibility of "Forgot password" modal
  let showPassword = false;  // Toggle password field visibility

  /**
   * Handles the user login process:
   * - Validates that username and password are provided
   * - Calls the backend API to authenticate the user
   * - Updates the UI with success or error messages
   * - Updates the global login state on success or failure
   * - Navigates to the homepage upon successful login
   * @returns {Promise<void>} Resolves when login process completes
   */
  async function handleLogin() {
    if (!username || !password) {
      message = "Please enter a username and password.";
      return;
    }

    isLoading = true;

    try {
      await getLogin(username, password); // Call API to authenticate
      message = '';
      isLoggedIn.set(true);              // Update global store
      push('/');                         // Navigate to homepage
    } catch (error) {
      console.error("Login error: ", error);
      message = "Login failed. Check your credentials.";
      isLoggedIn.set(false);
    } finally {
      isLoading = false;
    }
  }

  /**
   * Toggles the visibility of the password input field
   * @returns {void}
   */
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
