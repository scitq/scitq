<script lang="ts">
  import { onMount } from 'svelte';
  import CreateUserForm from '../components/CreateUserForm.svelte';
  import UserList from '../components/UserList.svelte';
  import { changepswd, getUser, getListUser, updateUser, delUser, forgotPassword } from '../lib/api';
  import { getToken } from '../lib/auth';
  import type { User } from '../lib/Stores/user';
  import { Eye, EyeOff } from 'lucide-svelte';
  import '../styles/SettingPage.css';

  // Current authenticated user data
  let user: User = undefined;

  // Controls visibility of password change modal
  let showModal = false;

  // Password fields for change password operation
  let oldPassword = '';
  let newPassword = '';
  let confirmNewPassword = '';

  // Stores error messages for password change operations
  let errorMessage = '';

  // Toggle states for password visibility
  let showOld = false;
  let showNew = false;
  let showConfirm = false;

  /**
   * Component initialization lifecycle hook
   * Fetches current user data when component mounts
   * @async
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
   * Toggles password field visibility
   * @param {'old'|'new'|'confirm'} type - Specifies which password field to toggle
   */
  function toggleShow(type: 'old' | 'new' | 'confirm') {
    if (type === 'old') showOld = !showOld;
    if (type === 'new') showNew = !showNew;
    if (type === 'confirm') showConfirm = !showConfirm;
  }

  /**
   * Opens the password change modal and resets form state
   */
  function openModal() {
    showModal = true;
    oldPassword = '';
    newPassword = '';
    confirmNewPassword = '';
    errorMessage = '';
  }

  /**
   * Closes the password change modal
   */
  function closeModal() {
    showModal = false;
  }

  /**
   * Handles password change confirmation
   * Validates inputs and calls password change API
   * @async
   * @throws {Error} When password validation fails or API call fails
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

<!-- Main user profile section -->
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

  <!-- Admin-specific functionality section -->
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

<!-- Password change modal dialog -->
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