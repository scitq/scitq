<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { wsClient } from '../lib/wsClient';
  import { getListUser, updateUser, delUser, forgotPassword } from '../lib/api';
  import type { User } from '../lib/Stores/user';
  import { Trash, Pencil, Lock, Eye, EyeOff } from 'lucide-svelte';
  import "../styles/worker.css";
  import "../styles/SettingPage.css";
  import "../styles/userList.css";

  /**
   * Array of user objects
   * @type {User[]}
   */
  let users: User[] = [];

  /**
   * User currently being edited
   * @type {User | null}
   */
  let editingUser: User = null;

  /**
   * Flag to show/hide edit modal
   * @type {boolean}
   */
  let showEditModal = false;

  /**
   * Temporary storage for edited username
   * @type {string}
   */
  let editedUsername = '';

  /**
   * Temporary storage for edited email
   * @type {string}
   */
  let editedEmail = '';

  /**
   * Temporary storage for edited admin status
   * @type {boolean}
   */
  let editedIsAdmin = false;

  /**
   * Flag to show/hide password modal
   * @type {boolean}
   */
  let showPasswordModal = false;

  /**
   * User whose password is being changed
   * @type {User | null}
   */
  let passwordUser: User = null;

  /**
   * New password value
   * @type {string}
   */
  let newPassword = '';

  /**
   * Flag to show/hide password text
   * @type {boolean}
   */
  let showForgotPassword = false;

  /**
   * Success message to display
   * @type {string}
   */
  let successMessage: string = '';

  /**
   * Timeout reference for clearing messages
   * @type {number}
   */
  let alertTimeout;

  /**
   * Handles WebSocket messages for user events
   * @param {Object} message - The WebSocket message
   */
  function handleMessage(message) {
    // Handle user deletion
    if (message.type === 'user-deleted') {
      users = users.filter(u => u.userId !== String(message.userId));
    }

    // Handle user creation
    if (message.type === 'user-created') {
      const newUser = {
        userId: String(message.payload.userId),
        username: message.payload.username,
        email: message.payload.email,
        isAdmin: message.payload.isAdmin
      };
      if (!users.some(u => u.userId === newUser.userId)) {
        users = [...users, newUser];
      }
    }

    // Handle user updates
    if (message.type === 'user-updated') {
      users = users.map(u => 
        u.userId === String(message.payload.userId)
          ? {
              ...u,
              username: message.payload.username || u.username,
              email: message.payload.email || u.email,
              isAdmin: message.payload.isAdmin !== undefined 
                       ? message.payload.isAdmin 
                       : u.isAdmin
            }
          : u
      );
    }
  }

  /**
   * Function to unsubscribe from WebSocket
   * @type {() => void}
   */
  let unsubscribeWS: () => void;

  /**
   * Component mount lifecycle hook
   * Loads users and subscribes to WebSocket
   */
  onMount(async () => {
    try {
      users = await getListUser();

      // Subscribe to WebSocket messages
      unsubscribeWS = wsClient.subscribeToMessages(handleMessage);
    } catch (err) {
      console.error("Error loading user data:", err);
    }
  });

  /**
   * Component destroy lifecycle hook
   * Unsubscribes from WebSocket
   */
  onDestroy(() => {
    // Unsubscribe from WebSocket messages
    unsubscribeWS?.();
  });

  /**
   * Opens the edit modal for a user
   * @param {User} user - User to edit
   */
  function openEditModal(user: User) {
    editingUser = user;
    editedUsername = user.username;
    editedEmail = user.email;
    editedIsAdmin = user.isAdmin;
    showEditModal = true;
  }

  /**
   * Closes the edit modal
   */
  function closeEditModal() {
    showEditModal = false;
    editingUser = null;
  }

  /**
   * Confirms and saves user edits
   * @async
   */
  async function confirmEdit() {
    const updates: Partial<User> = {};
    
    if (editedUsername !== editingUser.username) updates.username = editedUsername;
    if (editedEmail !== editingUser.email) updates.email = editedEmail;
    if (editedIsAdmin !== editingUser.isAdmin) updates.isAdmin = editedIsAdmin;

    try {
      await updateUser(editingUser.userId, updates);
      successMessage = "User Updated";
      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => successMessage = '', 5000);
    } catch (error) {
      console.error("Update error:", error);
      alert("Error updating user.");
    }
    
    closeEditModal();
  }

  /**
   * Opens the password change modal
   * @param {User} user - User to change password for
   */
  function openPasswordModal(user: User) {
    passwordUser = user;
    newPassword = '';
    showPasswordModal = true;
  }

  /**
   * Closes the password change modal
   */
  function closePasswordModal() {
    showPasswordModal = false;
    passwordUser = null;
    newPassword = '';
  }

  /**
   * Confirms and saves password change
   * @async
   */
  async function confirmPasswordChange() {
    try {
      await forgotPassword(
        passwordUser.userId,
        passwordUser.username,
        newPassword,
        passwordUser.email,
        passwordUser.isAdmin
      );
      successMessage = "Password Reset";
      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => successMessage = '', 5000);
    } catch (error) {
      alert("Error changing password.");
    }
    closePasswordModal();
  }

  /**
   * Handles user deletion
   * @async
   * @param {number} userId - ID of user to delete
   */
  async function handleDeleteUser(userId: number) {
    try {
      await delUser(userId);
      successMessage = "User Deleted";
      clearTimeout(alertTimeout);
      alertTimeout = setTimeout(() => successMessage = '', 5000);
    } catch (error) {
      console.error("Error deleting user:", error);
    }
  }
</script>


<!-- Success message alert -->
{#if successMessage}
  <div class="alert-success">
    {successMessage}
  </div>
{/if}

{#if users && users.length > 0}
  <div class="user-list-container">
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