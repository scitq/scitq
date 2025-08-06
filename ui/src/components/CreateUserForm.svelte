<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import { Eye, EyeOff } from 'lucide-svelte';
  import { newUser } from '../lib/api';
  import "../styles/createForm.css"; // Shared form styles

/**
 * Callback function type for notifying when a new user is created.
 * @callback onUserCreated
 * @param {Object} event - The event object containing user details.
 * @param {Object} event.detail - Details of the created user.
 * @param {User} event.detail.user - The newly created user object.
 */

/**
 * Username input bound variable.
 * @type {string}
 */
let username = '';

/**
 * Password input bound variable.
 * @type {string}
 */
let password = '';

/**
 * Email input bound variable.
 * @type {string}
 */
let email = '';

/**
 * Boolean flag indicating whether the new user is an admin.
 * @type {boolean}
 */
let isAdmin = false;

/**
 * Boolean flag to toggle password visibility in the input field.
 * @type {boolean}
 */
let showPassword = false;

  let successMessage: string = '';
  let alertTimeout;

/**
 * Creates a new user by calling the API with current form values.
 * On success, triggers the `onUserCreated` callback passing the new user details,
 * and resets the form fields.
 * Logs an error if user creation fails.
 * @async
 * @returns {Promise<void>}
 */
async function handleCreateUser() {
  const newUserId = await newUser(username, password, email, isAdmin);
  username = '';
  email = '';
  password = '';
  isAdmin = false;
  successMessage = "User Created";

  clearTimeout(alertTimeout);
  alertTimeout = setTimeout(() => {
    successMessage = '';
  }, 5000);
}

/**
 * Toggles the visibility of the password input field
 * between type 'text' and 'password'.
 * @returns {void}
 */
function toggleShowPassword() {
  showPassword = !showPassword;
}

</script>


<div class="createForm-form-container">  

  <!-- Username Input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="create-username">Username:</label>
    <input id="create-username" data-testid="username-createUser" type="text" bind:value={username} placeholder="Username" class="createForm-input" />
  </div>

  <!-- Email Input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="create-email">Email:</label>
    <input data-testid="email-createUser" id="create-email" type="email" bind:value={email} placeholder="Email" class="createForm-input" />
  </div>

  <!-- Password Input with Visibility Toggle -->
  <div class="createForm-form-group createForm-password-field">
    <label class="createForm-label" for="create-password">Password:</label>
    <input data-testid="password-createUser" id="create-password" type={showPassword ? 'text' : 'password'} bind:value={password} placeholder="Password" class="createForm-input"/>
    <button 
      type="button" 
      class="createForm-eye-button" 
      on:click={toggleShowPassword} 
      aria-label="Toggle password visibility"
    >
      {#if showPassword}
        <EyeOff size="20" />
      {:else}
        <Eye size="20" />
      {/if}
    </button>
  </div>

  <!-- Admin Checkbox -->
  <div class="createForm-form-group createForm-inline-checkbox">
    <label class="createForm-label" for="create-admin">Is Admin:</label>
    <input data-testid="isAdmin-createUser" id="create-admin" type="checkbox" bind:checked={isAdmin} />
  </div>

  <!-- Submit Button -->
  <button
    class="btn-validate createForm-add-button"
    on:click={handleCreateUser}
    aria-label="Create user"
    data-testid="create-user-button"
  >
    Create
  </button>
</div>
