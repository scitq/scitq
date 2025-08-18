vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { wsClient } from '../lib/wsClient';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import SettingPage from '../pages/SettingPage.svelte';
import * as auth from '../lib/auth';

let messageHandler: (msg: any) => void;


vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth');
  return {
    ...actual,
    getToken: vi.fn(),
  };
});


describe('SettingPage', () => {
  const mockUser = { userId: 1, username: 'admin', email: 'admin@example.com', isAdmin: true };
  const mockUsers = [
    { userId: 2, username: 'user1', email: 'user1@example.com', isAdmin: false },
    { userId: 3, username: 'user2', email: 'user2@example.com', isAdmin: false },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(auth.getToken).mockResolvedValue('test-token');

    mockApi.getUser.mockResolvedValue(mockUser);
    mockApi.getListUser.mockResolvedValue(mockUsers);

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
      messageHandler = handler; // Store the handler for later use
      return () => true; // Unsubscribe function
    });
  });

  it('displays current user information after fetching', async () => {
    const { getByText, getByDisplayValue } = render(SettingPage);
    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(getByDisplayValue('admin')).toBeInTheDocument();
      expect(getByDisplayValue('admin@example.com')).toBeInTheDocument();
      expect(getByText('Admin')).toBeInTheDocument();
    });
  });

  it('should allow user to change password via modal', async () => {
    const { getByText, getByPlaceholderText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(getByText('Change your password here')).toBeInTheDocument();
    });

    const changePasswordBtn = getByText('Change your password here');
    await fireEvent.click(changePasswordBtn);

    const oldPasswordInput = getByPlaceholderText('Current Password');
    const newPasswordInput = getByPlaceholderText('New Password');
    const confirmPasswordInput = getByPlaceholderText('Confirm New Password');

    await fireEvent.input(oldPasswordInput, { target: { value: 'oldpass' } });
    await fireEvent.input(newPasswordInput, { target: { value: 'newpass123' } });
    await fireEvent.input(confirmPasswordInput, { target: { value: 'newpass123' } });

    const confirmButton = getByText('Confirm');
    await fireEvent.click(confirmButton);

    await waitFor(() => {
      expect(mockApi.changepswd).toHaveBeenCalledWith('admin', 'oldpass', 'newpass123');
    });
  });

  it('shows error when new passwords do not match', async () => {
    const { getByText, getByPlaceholderText, findByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(getByText('Change your password here')).toBeInTheDocument();
    });

    await fireEvent.click(getByText('Change your password here'));

    await fireEvent.input(getByPlaceholderText('Current Password'), { target: { value: 'old' } });
    await fireEvent.input(getByPlaceholderText('New Password'), { target: { value: 'abc' } });
    await fireEvent.input(getByPlaceholderText('Confirm New Password'), { target: { value: 'xyz' } });

    await fireEvent.click(getByText('Confirm'));

    expect(await findByText('New passwords do not match.')).toBeInTheDocument();
  });

  it('shows user list and create user form only for admin', async () => {
    mockApi.getUser.mockResolvedValue({
      userId: 1,
      username: 'admin',
      email: 'admin@example.com',
      isAdmin: true
    });

    const { getByText, queryByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(queryByText('List of Users :')).toBeInTheDocument();
      expect(queryByText('Create User :')).toBeInTheDocument();
    });
  });

  it('does not show user list and create user form for non-admin user', async () => {
    mockApi.getUser.mockResolvedValue({
      userId: 2,
      username: 'user1',
      email: 'user1@example.com',
      isAdmin: false
    });

    const { getByText, queryByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(queryByText('List of Users :')).not.toBeInTheDocument();
      expect(queryByText('Create User :')).not.toBeInTheDocument();
    });
  });

it('should add a user and update the list', async () => {
    mockApi.newUser.mockResolvedValue('4');
    mockApi.getListUser.mockResolvedValueOnce(mockUsers);

    const { getByTestId, findByText, getByText } = render(SettingPage);

    await waitFor(() => {
        expect(getByTestId('create-user-button')).toBeInTheDocument();
    });

    // Fill the form
    await fireEvent.input(getByTestId('username-createUser'), { target: { value: 'newuser' } });
    await fireEvent.input(getByTestId('email-createUser'), { target: { value: 'new@test.com' } });
    await fireEvent.input(getByTestId('password-createUser'), { target: { value: 'password123' } });
    await fireEvent.click(getByTestId('isAdmin-createUser'));

    // Submit the form
    await fireEvent.click(getByTestId('create-user-button'));

    // Simulate WebSocket message
    if (messageHandler) {
        messageHandler({
            type: 'user-created',
            payload: {
                userId: 4,
                username: 'newuser',
                email: 'new@test.com',
                isAdmin: true
            }
        });
    }

    expect(await findByText('newuser')).toBeInTheDocument();
});

it('should delete a user and update the list', async () => {
    const { getByTestId, findByText, queryByText } = render(SettingPage);

    await waitFor(() => {
        expect(getByTestId('delete-btn-user-3')).toBeInTheDocument();
    });

    await fireEvent.click(getByTestId('delete-btn-user-3'));

    // Simulate WebSocket message
    if (messageHandler) {
        messageHandler({
            type: 'user-deleted',
            userId: 3
        });
    }

    // Wait for success message
    expect(await findByText('User Deleted')).toBeInTheDocument();
    
    // Verify user is removed
    await waitFor(() => {
        expect(queryByText('user2')).not.toBeInTheDocument();
    });
});

it('should update a user and show success message', async () => {
    const { getByTestId, getByText, findByText, queryByText } = render(SettingPage);

    await waitFor(() => {
        expect(getByTestId('edit-btn-user-2')).toBeInTheDocument();
    });

    mockApi.updateUser.mockResolvedValue({ success: true });

    await fireEvent.click(getByTestId('edit-btn-user-2'));
    await fireEvent.input(getByTestId('username-edit'), { target: { value: 'updatedUser' } });
    await fireEvent.input(getByTestId('email-edit'), { target: { value: 'updated@test.com' } });
    await fireEvent.click(getByTestId('isAdmin-edit'));
    await fireEvent.click(getByText('Confirm'));

    // Simulate WebSocket message
    if (messageHandler) {
        messageHandler({
            type: 'user-updated',
            payload: {
                userId: 2,
                username: 'updatedUser',
                email: 'updated@test.com',
                isAdmin: true
            }
        });
    }

    // Wait for success message
    expect(await findByText('User Updated')).toBeInTheDocument();
    
    // Verify changes
    await waitFor(() => {
        expect(queryByText('user1')).not.toBeInTheDocument();
        expect(getByText('updatedUser')).toBeInTheDocument();
    });
});

  it('should handle forgot password functionality', async () => {
    const { getByTestId, getByLabelText, getByText, findByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByTestId('forgot-pswd-button-2')).toBeInTheDocument();
    });

    await fireEvent.click(getByTestId('forgot-pswd-button-2'));
    await fireEvent.input(getByLabelText('New Password:'), { target: { value: 'newPassword123' } });
    await fireEvent.click(getByText('Confirm'));

    expect(await findByText('Password Reset')).toBeInTheDocument();
    expect(mockApi.forgotPassword).toHaveBeenCalledWith(
      2,
      'user1',
      'newPassword123',
      'user1@example.com',
      false
    );
  });
});