<script lang="ts">
  import { onMount } from 'svelte';
  import CreateUserForm from '../components/CreateUserForm.svelte';
  import UserList from '../components/UserList.svelte';
  import { changepswd, getUser, getListUser, updateUser, delUser, forgotPassword } from '../lib/api';
  import { getToken } from '../lib/auth';
  import type { User } from '../lib/Stores/user';
  import { Eye, EyeOff } from 'lucide-svelte';
  import '../styles/SettingPage.css';

  // Current user info
  let user: User = undefined;

  // Modal visibility state for password change
  let showModal = false;

  // Password input fields
  let oldPassword = '';
  let newPassword = '';
  let confirmNewPassword = '';

  // Error message for password change
  let errorMessage = '';

  // Toggles for password visibility
  let showOld = false;
  let showNew = false;
  let showConfirm = false;

  /**
   * Initializes component by fetching current user and user list
   * @returns {Promise<void>} Resolves when data is loaded
   */
  onMount(async () => {
    try {
      const token = await getToken();
      if (token) {
        user = await getUser(token);
      }
    } catch (err) {
      console.error("Error loading user data:", err);
    }
  });

  /**
   * Toggles the visibility of password input fields.
   * 
   * @param type - Which password field to toggle ('old', 'new', or 'confirm').
   */
  function toggleShow(type: 'old' | 'new' | 'confirm') {
    if (type === 'old') showOld = !showOld;
    if (type === 'new') showNew = !showNew;
    if (type === 'confirm') showConfirm = !showConfirm;
  }

  /**
   * Opens the password change modal and resets input fields and error messages.
   */
  function openModal() {
    showModal = true;
    oldPassword = '';
    newPassword = '';
    confirmNewPassword = '';
    errorMessage = '';
  }

  /**
   * Closes the password change modal.
   */
  function closeModal() {
    showModal = false;
  }

  /**
   * Confirms the password change after validating input fields.
   * Checks password match and difference, then calls API to change the password.
   * Displays error messages if validation or API call fails.
   */
  async function confirmPasswordChange() {
    if (newPassword !== confirmNewPassword) {
      errorMessage = "New passwords do not match.";
      return;
    }
    if (oldPassword === newPassword) {
      errorMessage = "New password must be different from the current one.";
      return;
    }
    try {
      await changepswd(user.username, oldPassword, newPassword);
      closeModal();
    } catch (err) {
      errorMessage = "Error. Please check that your password is valid.";
    }
  }
</script>

<!-- Personal profile display -->
<div class="settings-container" data-testid="settings-page">
  <div class="settings-myProfile">
    <h2 class="settings-myProfile-header" style="margin-left: 2rem;">My Profile :</h2>
    {#if user}
      <div class="settings-info-item">
        <div class="settings-form-block">
          <label for="username" class="settings-label-settings">Username:</label>
          <input id="username" class="settings-input-text" type="text" value={user.username} readonly />
        </div>
        <div class="settings-form-block">
          <label for="email" class="settings-label-settings">Email:</label>
          <input id="email" class="settings-input-email" type="email" value={user.email} readonly />
        </div>
        <div class="settings-form-block">
          <div class="settings-label-settings" aria-label="Status">Status:</div>
          <span style="font-size: 0.7rem; margin-left: 0.5rem;">{user.isAdmin ? 'Admin' : 'No Admin'}</span>
        </div>
      </div>
      <button class="link settings-change-password" data-testid="change-pswd-button" on:click={openModal}>Change your password here</button>
    {/if}
  </div>

    <!-- Admin section: user creation and user list -->
  {#if user && user.isAdmin}
    <div class="settings-admin-user-grid">
      <div class="settings-list-user-box">
        <h2 class="settings-myProfile-header">List of Users :</h2>
        <UserList />
      </div>
      <div class="settings-create-user-box">
        <h2 class="settings-myProfile-header">Create User :</h2>
        <CreateUserForm />
      </div>
    </div>
  {/if}
</div>

<!-- Password change modal -->
{#if showModal}
  <div class="settings-modal">
    <div class="settings-modal-content">
      <h3>Change your password</h3>
      <div class="settings-password-field">
        <input type={showOld ? 'text' : 'password'} bind:value={oldPassword} placeholder="Current Password" class="settings-input-password" />
        <button class="settings-eye-button" on:click={() => toggleShow('old')}>
          {#if showOld}<EyeOff size="20" />{:else}<Eye size="20" />{/if}
        </button>
      </div>
      <div class="settings-password-field">
        <input type={showNew ? 'text' : 'password'} bind:value={newPassword} placeholder="New Password" class="settings-input-password" />
        <button class="settings-eye-button" on:click={() => toggleShow('new')}>
          {#if showNew}<EyeOff size="20" />{:else}<Eye size="20" />{/if}
        </button>
      </div>
      <div class="settings-password-field">
        <input type={showConfirm ? 'text' : 'password'} bind:value={confirmNewPassword} placeholder="Confirm New Password" class="settings-input-password" />
        <button class="settings-eye-button" on:click={() => toggleShow('confirm')}>
          {#if showConfirm}<EyeOff size="20" />{:else}<Eye size="20" />{/if}
        </button>
      </div>
      <div class="settings-modal-buttons">
        <button class="settings-confirm-btn" on:click={confirmPasswordChange}>Confirm</button>
        <button class="settings-cancel-btn" on:click={closeModal}>Cancel</button>
      </div>
      {#if errorMessage}<p style="color: red;">{errorMessage}</p>{/if}
    </div>
  </div>
{/if}