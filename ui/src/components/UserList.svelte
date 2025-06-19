<script lang="ts">
  import { Trash, Pencil, Lock, Eye, EyeOff } from 'lucide-svelte';
  import "../styles/worker.css";
  import "../styles/userList.css";

  export let users: User[] = [];

  // Callbacks passed from parent component
  export let onUserUpdated: (event: { detail: { user: User } }) => void = () => {};
  export let onUserDeleted: (event: { detail: { userId: number } }) => void = () => {};
  export let onForgotPassword: (event: { detail: { user: User; newPswd: string } }) => void = () => {};

  // Edit modal state
  let editingUser: User = null;
  let showEditModal = false;
  let editedUsername = '';
  let editedEmail = '';
  let editedIsAdmin = false;

  // Password modal state
  let showPasswordModal = false;
  let passwordUser: User = null;
  let newPassword = '';
  let showForgotPassword = false;

  /**
   * Opens the edit modal for a given user.
   * Initializes the form fields with the user's current data.
   * @param {User} user - The user to be edited.
   * @returns {void}
   */
  function openEditModal(user: User) {
    editingUser = user;
    editedUsername = user.username;
    editedEmail = user.email;
    editedIsAdmin = user.isAdmin;
    showEditModal = true;
  }

  /**
   * Closes the edit modal and resets the editing user.
   * @returns {void}
   */
  function closeEditModal() {
    showEditModal = false;
    editingUser = null;
  }

  /**
   * Confirms the changes made to the user.
   * Prepares an object containing only the updated fields and triggers the onUserUpdated event.
   * Then closes the edit modal.
   * @returns {void}
   */
  function confirmEdit() {
    const updates: Partial<User> = {};
    
    if (editedUsername !== editingUser.username) updates.username = editedUsername;
    if (editedEmail !== editingUser.email) updates.email = editedEmail;
    if (editedIsAdmin !== editingUser.isAdmin) updates.isAdmin = editedIsAdmin;

    if (Object.keys(updates).length > 0) {
      onUserUpdated({
        detail: {
          userId: editingUser.userId,
          updates
        }
      });
    }
    
    closeEditModal();
  }

  /**
   * Opens the password change modal for a given user.
   * Initializes the new password field.
   * @param {User} user - The user whose password will be changed.
   * @returns {void}
   */
  function openPasswordModal(user: User) {
    passwordUser = user;
    newPassword = '';
    showPasswordModal = true;
  }

  /**
   * Closes the password change modal and resets related fields.
   * @returns {void}
   */
  function closePasswordModal() {
    showPasswordModal = false;
    passwordUser = null;
    newPassword = '';
  }

  /**
   * Confirms the password change.
   * Triggers the onForgotPassword event with the user and new password,
   * then closes the modal.
   * @returns {void}
   */
  function confirmPasswordChange() {
    onForgotPassword({
      detail: {
        user: passwordUser,
        newPswd: newPassword
      }
    });
    closePasswordModal();
  }

  /**
   * Handles user deletion by triggering the onUserDeleted event.
   * @param {number} userId - The ID of the user to delete.
   * @returns {void}
   */
  function handleDeleteUser(userId: number) {
    onUserDeleted({
      detail: { userId }
    });
  }
</script>



{#if users && users.length > 0}
  <div class="workerCompo-table-wrapper">
    <table class="listTable">
      <thead>
        <tr>
          <th>Username</th>
          <th>Email</th>
          <th>Admin</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each users as user (user.userId)}
          <tr>
            <td>{user.username}</td>
            <td>{user.email}</td>
            <td>{user.isAdmin ? 'Yes' : 'No'}</td>
            <td class="workerCompo-actions">
              <button class="btn-action" title="Delete" data-testid={`delete-btn-user-${user.userId}`} on:click={() => handleDeleteUser(user.userId)}>
                <Trash />
              </button>
              <button class="btn-action" title="Edit User" data-testid={`edit-btn-user-${user.userId}`} on:click={() => openEditModal(user)}>
                <Pencil />
              </button>
              <button
                class="btn-action"
                title="Forgot Password"
                data-testid={`forgot-pswd-button-${user.userId}`}
                on:click={() => openPasswordModal(user)}
              >
                <Lock />
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <p class="userList-empty-state">No users found.</p>
{/if}

{#if showEditModal}
  <div class="userList-modal-overlay">
    <div class="userList-modal-content">
      <h3>Edit User</h3>
      
      <div class="form-group">
        <label for="username" class="userList-label-settings">Username:</label>
        <input data-testid="username-edit" id="username" type="text" bind:value={editedUsername} />
      </div>

      <div class="form-group">
        <label for="email" class="userList-label-settings">Email:</label>
        <input data-testid="email-edit" id="email" type="email" bind:value={editedEmail} />
      </div>

      <div class="form-group">
        <label for="isAdmin" class="userList-label-settings">Admin:</label>
        <input data-testid="isAdmin-edit" id="isAdmin" type="checkbox" bind:checked={editedIsAdmin} class="userList-checkbox-input" />
      </div>


      <div class="settings-modal-buttons">
        <button class="settings-confirm-btn" on:click={confirmEdit}>Confirm</button>
        <button class="settings-cancel-btn" on:click={closeEditModal}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

{#if showPasswordModal}
  <div class="userList-modal-overlay">
    <div class="userList-modal-content">
      <h3>Change Password for {passwordUser.username}</h3>

      <div class="form-group">
        <div class="form-group settings-password-group">
          <label for="newPassword" class="userList-label-settings">New Password:</label>
          <div class="settings-password-wrapper">
            <input
              id="newPassword"
              class="settings-password-input"
              type={showForgotPassword ? 'text' : 'password'}
              bind:value={newPassword}
            />
            <button
              type="button"
              class="settings-toggle-password-btn"
              on:click={() => showForgotPassword = !showForgotPassword}
            >
              {#if showForgotPassword}
                <EyeOff size="20" />
              {:else}
                <Eye size="20" />
              {/if}
            </button>
          </div>
        </div>
      </div>

      <div class="userList-modal-buttons">
        <button class="settings-confirm-btn" on:click={confirmPasswordChange}>Confirm</button>
        <button class="settings-cancel-btn" on:click={closePasswordModal}>Cancel</button>
      </div>
    </div>
  </div>
{/if}