import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, test } from 'vitest';
import SettingPage from '../pages/SettingPage.svelte';
import { getUser, getListUser, delUser, updateUser, changepswd, newUser } from '../lib/api';
import { userInfo } from '../lib/Stores/user';
import * as api from '../lib/api';

// Mocks
vi.mock('../lib/api', () => ({
  getUser: vi.fn(),
  getListUser: vi.fn(),
  newUser: vi.fn(),
  delUser: vi.fn(),
  updateUser: vi.fn(),
  changepswd: vi.fn()
}));

vi.mock('../components/CreateUserForm.svelte', () => ({
  default: () => {
    return {
      $$render: () => '<div data-testid="create-user-form"></div>'
    }
  }
}));

vi.mock('../components/UserList.svelte', () => ({
  default: () => {
    return {
      $$render: () => '<div data-testid="user-list"></div>'
    }
  }
}));

describe('SettingPage', () => {
  const mockUser = { userId: '1', username: 'admin', email: 'admin@example.com', isAdmin: true };
  const mockUsers = [
    { userId: '2', username: 'user1', email: 'user1@example.com', isAdmin: false },
    { userId: '3', username: 'user2', email: 'user2@example.com', isAdmin: false },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    userInfo.set({ token: 'test-token' });

    (getUser as any).mockResolvedValue(mockUser);
    (getListUser as any).mockResolvedValue(mockUsers);
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

    // Wait for user info to be displayed (i.e., user is defined)
    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      expect(getByText('Change your password here')).toBeInTheDocument();
    });

    // Click on "Change your password here" button
    const changePasswordBtn = getByText('Change your password here');
    await fireEvent.click(changePasswordBtn);

    // Check that the modal appears with input fields
    const oldPasswordInput = getByPlaceholderText('Current Password') as HTMLInputElement;
    const newPasswordInput = getByPlaceholderText('New Password') as HTMLInputElement;
    const confirmPasswordInput = getByPlaceholderText('Confirm New Password') as HTMLInputElement;

    // Fill the fields
    await fireEvent.input(oldPasswordInput, { target: { value: 'oldpass' } });
    await fireEvent.input(newPasswordInput, { target: { value: 'newpass123' } });
    await fireEvent.input(confirmPasswordInput, { target: { value: 'newpass123' } });

    // Click on "Confirm"
    const confirmButton = getByText('Confirm');
    await fireEvent.click(confirmButton);

    // Check that `changepswd` API was called with correct parameters
    await waitFor(() => {
      expect(changepswd).toHaveBeenCalledWith('admin', 'oldpass', 'newpass123');
    });
  });

  it('shows error when new passwords do not match', async () => {
    const { getByText, getByPlaceholderText, findByText } = render(SettingPage);

    // Wait for user info to be displayed (i.e., user is defined)
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
    // Admin case (user.isAdmin = true)
    (getUser as any).mockResolvedValue({
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
    // Non-admin case
    (getUser as any).mockResolvedValue({
      userId: '2',
      username: 'user1',
      email: 'user1@example.com',
      isAdmin: false
    });

    const { getByText, queryByText } = render(SettingPage);

    // Wait for profile to be displayed
    await waitFor(() => {
      expect(getByText('My Profile :')).toBeInTheDocument();
      // Ensure the list and form are NOT in the DOM
      expect(queryByText('List of Users :')).not.toBeInTheDocument();
      expect(queryByText('Create User :')).not.toBeInTheDocument();
    });
  });

//   it('Add a user and update the list', async () => {
//   const { getByLabelText, getByTestId, findByText } = render(SettingPage)

//   await fireEvent.input(getByLabelText('Username:'), { target: { value: 'newuser' } })
//   await fireEvent.input(getByLabelText('Email:'), { target: { value: 'new@test.com' } })
//   await fireEvent.input(getByLabelText('Password:'), { target: { value: 'password123' } })
//   await fireEvent.click(getByLabelText('Is Admin:'))

//   await fireEvent.click(getByTestId('create-user-button'))

//   expect(await findByText('User Created')).toBeInTheDocument()
// })
});
