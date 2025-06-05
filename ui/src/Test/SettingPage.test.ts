vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor, getByTestId } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import SettingPage from '../pages/SettingPage.svelte';
import { userInfo } from '../lib/Stores/user';

describe('SettingPage', () => {
  const mockUser = { userId: '1', username: 'admin', email: 'admin@example.com', isAdmin: true };
  const mockUsers = [
    { userId: '2', username: 'user1', email: 'user1@example.com', isAdmin: false },
    { userId: '3', username: 'user2', email: 'user2@example.com', isAdmin: false },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    userInfo.set({ token: 'test-token' });

    mockApi.getUser.mockResolvedValue(mockUser);
    mockApi.getListUser.mockResolvedValue(mockUsers);
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
      userId: '1',
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
      userId: '2',
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
    // 1. Setup mocks
    mockApi.newUser.mockResolvedValue('4');
    mockApi.getListUser.mockResolvedValueOnce(mockUsers); // First call - initial list

    const { getByTestId, findByText, queryByText } = render(SettingPage);

    // 2. Wait for form to be ready
    await waitFor(() => {
      expect(getByTestId('create-user-button')).toBeInTheDocument();
    });

    // 3. Fill the form with a more robust approach
    const usernameInput = getByTestId('username-createUser') as HTMLInputElement;
    const emailInput = getByTestId('email-createUser') as HTMLInputElement;
    const passwordInput = getByTestId('password-createUser') as HTMLInputElement;

    // Method to ensure values update
    await fireEvent.input(usernameInput, { target: { value: 'newuser' } });
    await fireEvent.input(emailInput, { target: { value: 'new@test.com' } });
    await fireEvent.input(passwordInput, { target: { value: 'password123' } });

    // Intermediate value check
    expect(usernameInput.value).toBe('newuser');
    expect(emailInput.value).toBe('new@test.com');
    expect(passwordInput.value).toBe('password123');

    // Check the checkbox
    const adminCheckbox = getByTestId('isAdmin-createUser') as HTMLInputElement;
    await fireEvent.click(adminCheckbox);

    // Submit the form
    await fireEvent.click(getByTestId('create-user-button'));

    // 4. Validations
    expect(await findByText('User Created')).toBeInTheDocument();
    expect(mockApi.newUser).toHaveBeenCalledWith(
      'newuser',
      'password123',
      'new@test.com',
      true
    );

    // Additional check that user appears in list
    await waitFor(() => {
      expect(queryByText('newuser')).toBeInTheDocument();
    });
  });

  it('should delete a user and update the list', async () => {
    const { findByText, getByTestId, queryByText, getByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(queryByText('List of Users :')).toBeInTheDocument();
      expect(queryByText('Create User :')).toBeInTheDocument();
      expect(queryByText('user2')).toBeInTheDocument;
    });

    const deleteButton = getByTestId('delete-btn-user-3');
    await fireEvent.click(deleteButton);

    expect(await findByText('User Deleted')).toBeInTheDocument();
    expect(mockApi.delUser).toHaveBeenCalledWith('3');

    await waitFor(() => {
      expect(queryByText('user2')).not.toBeInTheDocument();
    });
  });

  it('should update a user and show success message', async () => {
    const { getByText, findByText, getByTestId, queryByText } = render(SettingPage);

    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(queryByText('List of Users :')).toBeInTheDocument();
      expect(queryByText('Create User :')).toBeInTheDocument();
      expect(queryByText('user1')).toBeInTheDocument();
      expect(getByTestId(`edit-btn-user-2`)).toBeInTheDocument();
    });

    await fireEvent.click(getByTestId('edit-btn-user-2'));
    await fireEvent.input(getByTestId('username-edit'), { target: { value: 'updatedUser' } });
    await fireEvent.input(getByTestId('email-edit'), { target: { value: 'updated@test.com' } });
    await fireEvent.click(getByTestId('isAdmin-edit'));
    await fireEvent.click(getByText('Confirm'));

    expect(await findByText('User Updated')).toBeInTheDocument();
    expect(mockApi.updateUser).toHaveBeenCalledWith('2', {
      username: 'updatedUser',
      email: 'updated@test.com',
      isAdmin: true
    });
    await waitFor(() => {
      expect(queryByText('updatedUser')).toBeInTheDocument();
      expect(queryByText('user1')).not.toBeInTheDocument();
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
      '2',
      'user1',
      'newPassword123',
      'user1@example.com',
      false
    );
  });
});
