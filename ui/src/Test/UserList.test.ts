vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import UserList from '../components/UserList.svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('UserList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('should call onUserDeleted with correct userId when delete button is clicked', async () => {
    const mockUsers = [
        { userId: 1, username: 'user1', email: 'user1@test.com', isAdmin: false },
        { userId: 2, username: 'user2', email: 'user2@test.com', isAdmin: true }
    ];
    const mockOnUserDeleted = vi.fn();
    
    const { getAllByTitle } = render(UserList, {
      props: {
        users: mockUsers,
        onUserDeleted: mockOnUserDeleted
      }
    });

    // Find all Delete buttons (one per user)
    const deleteButtons = getAllByTitle('Delete');
    expect(deleteButtons.length).toBe(2);

    // Click the Delete button for the first user
    await fireEvent.click(deleteButtons[0]);

    // Check that the event was triggered with the correct userId
    expect(mockOnUserDeleted).toHaveBeenCalledTimes(1);
    expect(mockOnUserDeleted).toHaveBeenCalledWith({
      detail: {
        userId: 1 // First user's ID
      }
    });
  });

  it('should call onUserUpdated with correct data when editing a user', async () => {
    const mockUsers = [
        { userId: 1, username: 'user1', email: 'user1@test.com', isAdmin: false }
    ];
    const mockOnUserUpdated = vi.fn();
    
    const { getByTitle, getByLabelText, getByText } = render(UserList, {
      props: {
        users: mockUsers,
        onUserUpdated: mockOnUserUpdated
      }
    });

    // 1. Open the edit modal
    const editButton = getByTitle('Edit User');
    await fireEvent.click(editButton);

    // 2. Modify the fields
    const usernameInput = getByLabelText('Username:');
    const emailInput = getByLabelText('Email:');
    const adminCheckbox = getByLabelText('Admin:');

    await fireEvent.input(usernameInput, { target: { value: 'newUsername' } });
    await fireEvent.input(emailInput, { target: { value: 'new@email.com' } });
    await fireEvent.click(adminCheckbox); // Toggle to true

    // 3. Submit changes
    const confirmButton = getByText('Confirm');
    await fireEvent.click(confirmButton);

    // 4. Verify the emitted event
    await waitFor(() => {
      expect(mockOnUserUpdated).toHaveBeenCalledTimes(1);
      expect(mockOnUserUpdated).toHaveBeenCalledWith({
        detail: {
          userId: 1, // Unchanged ID
          updates: {
            username: 'newUsername',
            email: 'new@email.com',
            isAdmin: true // Changed
          }
        }
      });
    });
  });

  it('should pass both user and new password', async () => {
    const mockUsers = [
        { userId: 1, username: 'user1', email: 'user1@test.com', isAdmin: false },
        { userId: 2, username: 'user2', email: 'user2@test.com', isAdmin: true }
    ];
    const mockOnForgotPassword = vi.fn();

    const { getByLabelText, getByText, getByTestId } = render(UserList, {
      props: {
        users: mockUsers,
        onForgotPassword: mockOnForgotPassword
      }
    });

    // Trigger forgot password action for userId 1
    await fireEvent.click(getByTestId('forgot-pswd-button-1'));
    await fireEvent.input(getByLabelText('New Password:'), { target: { value: 'newPass123' } });
    await fireEvent.click(getByText('Confirm'));

    // Verify structure of emitted data
    await waitFor(() => {
      expect(mockOnForgotPassword).toHaveBeenCalledWith({
        detail: {
          user: {
            userId: 1,
            username: 'user1',
            email: 'user1@test.com',
            isAdmin: false
          },
          newPswd: 'newPass123' // Correctly passed inside detail
        }
      });
    });
  });

});
