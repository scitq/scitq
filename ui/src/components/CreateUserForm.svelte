<script lang="ts"> 
  import { createEventDispatcher } from 'svelte';
  import { Eye, EyeOff } from 'lucide-svelte';
  import { newUser } from '../lib/api';
  import "../styles/createForm.css"; // Shared form styles

  // Callback function to notify parent when a user is created
  export let onUserCreated: (event: { detail: { user: User } }) => void = () => {};

  // Form field values
  let username = '';
  let password = '';
  let email = '';
  let isAdmin = false;
  let showPassword = false; // Flag to toggle password visibility

  // Creates a new user via API call
  async function handleCreateUser() {
    const newUserId = await newUser(username, password, email, isAdmin);

    if (newUserId !== undefined) {
      // Notify parent component with the new user details
      onUserCreated({
        detail: {
          user: {
            userId: newUserId,
            username,
            email,
            isAdmin
          }
        }
      });

      // Reset form fields after submission
      username = '';
      email = '';
      password = '';
      isAdmin = false;

    } else {
      console.error("User creation failed, not dispatching event.");
    }
  }

  // Toggle visibility of the password field
  function toggleShowPassword() {
    showPassword = !showPassword;
  }
</script>


<div class="createForm-form-container">  

  <!-- Username Input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="create-username">Username:</label>
    <input id="create-username" type="text" bind:value={username} placeholder="Username" class="createForm-input" />
  </div>

  <!-- Email Input -->
  <div class="createForm-form-group">
    <label class="createForm-label" for="create-email">Email:</label>
    <input id="create-email" type="email" bind:value={email} placeholder="Email" class="createForm-input" />
  </div>

  <!-- Password Input with Visibility Toggle -->
  <div class="createForm-form-group createForm-password-field">
    <label class="createForm-label" for="create-password">Password:</label>
    <input 
      id="create-password" 
      type={showPassword ? 'text' : 'password'} 
      bind:value={password} 
      placeholder="Password" 
      class="createForm-input" 
    />
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
    <input id="create-admin" type="checkbox" bind:checked={isAdmin} />
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
