<script lang="ts">
  import '../styles/loginForm.css';              // Styles specific to the login form
  import { getLogin } from '../lib/auth';        // API call for authentication
  import { isLoggedIn } from '../lib/Stores/user'; // Svelte store for global auth state
  import { push } from 'svelte-spa-router';      // For navigation after login
  import { Eye, EyeOff } from 'lucide-svelte';   // Icons for showing/hiding password

  /**
   * User's login username/email input
   * @type {string}
   */
  let username = '';

  /**
   * User's login password input
   * @type {string}
   */
  let password = '';

  /**
   * Feedback message displayed to user (error/success)
   * @type {string}
   */
  let message = '';

  /**
   * Loading state flag for login button
   * @type {boolean}
   */
  let isLoading = false;

  /**
   * Controls visibility of the password reset modal
   * @type {boolean}
   */
  let showModal = false;

  /**
   * Toggles password visibility in the input field
   * @type {boolean}
   */
  let showPassword = false;

  /**
   * Handles the user login process:
   * - Validates input fields
   * - Calls authentication API
   * - Updates UI state and global auth store
   * - Handles success/error cases
   * @async
   * @returns {Promise<void>}
   * @throws {Error} When authentication fails
   */
  async function handleLogin(): Promise<void> {
    if (!username || !password) {
      message = "Please enter a username and password.";
      return;
    }

    isLoading = true;
    message = ''; // Clear previous messages

    try {
      await getLogin(username, password);
      isLoggedIn.set(true); // Update global auth state
      push('/'); // Navigate to home on success
    } catch (error) {
      console.error("Login error: ", error);
      message = "Login failed. Check your credentials.";
      isLoggedIn.set(false);
    } finally {
      isLoading = false;
    }
  }

  /**
   * Toggles password visibility in the input field
   * Updates the showPassword state and related UI
   * @returns {void}
   */
  function togglePasswordVisibility(): void {
    showPassword = !showPassword;
  }
</script>

<!-- Main login form container -->
<div class="loginform-container">
  <h1 class="loginform-title">Login</h1>

  <!-- Username input field -->
  <div class="loginform-group">
    <label for="email" class="loginform-label">Username</label>
    <input 
      id="email" 
      type="email" 
      class="loginform-input" 
      bind:value={username}
      aria-label="Enter your username"
      data-testid="username-input"
    />
  </div>

  <!-- Password input field with visibility toggle -->
  <div class="loginform-group">
    <label for="password" class="loginform-label">Password</label>
    <div class="password-input-wrapper">
      <input
        id="password"
        type={showPassword ? 'text' : 'password'}
        class="loginform-input password-input"
        bind:value={password}
        aria-label="Enter your password"
        data-testid="password-input"
      />
      <button
        type="button"
        aria-label={showPassword ? "Hide password" : "Show password"}
        on:click={togglePasswordVisibility}
        class="password-toggle-button"
        data-testid="toggle-password-visibility"
      >
        {#if showPassword}
          <EyeOff size={20} />
        {:else}
          <Eye size={20} />
        {/if}
      </button>
    </div>
  </div>

  <!-- Login submit button -->
  <button 
    class="loginform-button btn-validate" 
    on:click={handleLogin} 
    disabled={isLoading}
    aria-busy={isLoading}
    data-testid="login-button"
  >
    {isLoading ? "Loading..." : "Log In"}
  </button>

  <!-- Footer with password reset option -->
  <div class="loginform-footer">
    <button 
      type="button" 
      class="loginform-forgot" 
      on:click={() => showModal = true}
      data-testid="forgot-password-button"
    >
      Forgot Password?
    </button>
  </div>

  <!-- Status message display -->
  {#if message}
    <p class="loginform-message" data-testid="login-message">{message}</p>
  {/if}
</div>

<!-- Password reset modal -->
{#if showModal}
  <div class="loginform-modal-overlay" role="dialog" aria-modal="true">
    <div class="loginform-modal-content">
      <h4>Please contact an administrator.</h4>
      <button
        type="button"
        class="loginform-modal-close-button btn-validate"
        on:click={() => showModal = false}
        aria-label="Close password reset modal"
        data-testid="close-modal-button"
      >
        Close
      </button>
    </div>
  </div>
{/if}