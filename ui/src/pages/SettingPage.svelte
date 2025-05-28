<script lang="ts">
  import { onMount } from 'svelte';
  import CreateUserForm from '../components/CreateUserForm.svelte';
  import UserList from '../components/UserList.svelte';
  import { userInfo } from '../lib/Stores/user';
  import { changepswd, getUser, getListUser, updateUser, delUser, forgotPassword } from '../lib/api';
  import type { User } from '../lib/Stores/user';
  import { Eye, EyeOff } from 'lucide-svelte';
  import '../styles/SettingPage.css';

  // Current user info
  let user: User = undefined;
  // List of all users
  let users: User[] = [];

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

  // Success message and timeout handler
  let successMessage: string = '';
  let alertTimeout;

  // Fetch user data and user list on component mount
  onMount(async () => {
    try {
      const token = $userInfo?.token;
      if (token) {
        user = await getUser(token);
      }
      users = await getListUser();
    } catch (err) {
      console.error("Error during onMount:", err);
    }
  });

  /**
   * Handle new user creation event from CreateUserForm
   * Adds new user to local users list and shows success message
   */
  function handleUserCreated(event) {
    if (event?.detail?.user) {
      users = [...users, event.detail.user];
    }
    successMessage = "User Created";

    // Clear success message after 5 seconds
    clearTimeout(alertTimeout);
    alertTimeout = setTimeout(() => {
      successMessage = '';
    }, 5000);
  }

  /**
   * Handle user deletion event from UserList
   * Deletes user via API and updates local list, then shows success message
   */
  async function handleDeleteUser(event) {
    const userIdToDelete = event.detail.userId;
    try {
      await delUser(userIdToDelete);
      users = users.filter(u => u.userId !== userIdToDelete);

      successMessage = "User Deleted";

      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);

    } catch (error) {
      console.error("Error deleting user:", error);
    }
  }

  /**
   * Handle user update event from UserList
   * Updates user info via API and updates local list, then shows success message
   */
async function handleUpdateUser(event) {
  const { userId, updates } = event.detail;
  
  try {
    await updateUser(userId, updates);

    // Optimized local state update
    users = users.map(u => u.userId === userId ? { ...u, ...updates } : u);

    successMessage = "User Updated";
    clearTimeout(alertTimeout);
    alertTimeout = setTimeout(() => {
      successMessage = '';
    }, 5000);
  } catch (error) {
    console.error("Update error:", error);
    alert("Error updating user.");
  }
}


  /**
   * Handle password reset event from UserList
   * Calls API to reset password and shows success message
   */
  async function handleForgotPassword(event) {
    const { userId, username, email, isAdmin } = event.detail.user;
    const newPassword = event.detail.newPswd;
    try {
      await forgotPassword(userId, username, newPassword, email, isAdmin);
      successMessage = "Password Reset";

      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => {
        successMessage = '';
      }, 5000);
    } catch (error) {
      alert("Error changing password.");
    }
  }

  /**
   * Toggle password visibility for old, new, or confirm fields
   */
  function toggleShow(type: 'old' | 'new' | 'confirm') {
    if (type === 'old') showOld = !showOld;
    if (type === 'new') showNew = !showNew;
    if (type === 'confirm') showConfirm = !showConfirm;
  }

  /**
   * Open the password change modal and reset fields
   */
  function openModal() {
    showModal = true;
    oldPassword = '';
    newPassword = '';
    confirmNewPassword = '';
    errorMessage = '';
  }

  /**
   * Close the password change modal
   */
  function closeModal() {
    showModal = false;
  }

  /**
   * Confirm password change after validation
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
<div class="settings-container">
  <h2 class="settings-myProfile">My Profile :</h2>
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
        <span>{user.isAdmin ? 'Admin' : 'No Admin'}</span>
      </div>
    </div>
    <button class="link settings-change-password" data-testid="change-pswd-button" on:click={openModal}>Change your password here</button>
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

<!-- Admin section: user creation and user list -->
{#if user && user.isAdmin}
  <div class="settings-admin-user-grid">
    <div class="settings-list-user-box">
      <h2 class="settings-myProfile">List of Users :</h2>
      <UserList
        {users}
        onUserDeleted={handleDeleteUser}
        onUserUpdated={handleUpdateUser}
        onForgotPassword={handleForgotPassword}
      />
    </div>
    <div class="settings-create-user-box">
      <h2 class="settings-myProfile">Create User :</h2>
      <CreateUserForm onUserCreated={handleUserCreated} />
    </div>
  </div>
{/if}

<!-- Success message alert -->
{#if successMessage}
  <div class="alert-success">
    {successMessage}
  </div>
{/if}
