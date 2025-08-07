vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import UserList from '../components/UserList.svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { wsClient } from '../lib/wsClient';

describe('UserList', () => {
  const mockUsers = [
    { userId: 1, username: 'user1', email: 'user1@test.com', isAdmin: false },
    { userId: 2, username: 'user2', email: 'user2@test.com', isAdmin: true }
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    // Mock getListUser to return our test users
    mockApi.getListUser = vi.fn().mockResolvedValue(mockUsers);
  });

  it('should call delUser with correct userId when delete button is clicked', async () => {
    const { getAllByTestId } = render(UserList);
    
    await waitFor(() => {
      const deleteButtons = getAllByTestId(/delete-btn-user-/);
      expect(deleteButtons.length).toBe(2);
    });

    const deleteButtons = getAllByTestId(/delete-btn-user-/);
    await fireEvent.click(deleteButtons[0]);

    expect(mockApi.delUser).toHaveBeenCalledTimes(1);
    expect(mockApi.delUser).toHaveBeenCalledWith(1); // First user's ID
  });

  it('should call updateUser with correct data when editing a user', async () => {
    const { getByTestId, getByLabelText, getByText } = render(UserList);
    
    await waitFor(() => {
      expect(getByTestId('edit-btn-user-1')).toBeDefined();
    });

    // Open edit modal
    await fireEvent.click(getByTestId('edit-btn-user-1'));

    // Modify fields
    const usernameInput = getByLabelText('Username:');
    const emailInput = getByLabelText('Email:');
    const adminCheckbox = getByLabelText('Admin:');

    await fireEvent.input(usernameInput, { target: { value: 'newUsername' } });
    await fireEvent.input(emailInput, { target: { value: 'new@email.com' } });
    await fireEvent.click(adminCheckbox);

    // Submit changes
    await fireEvent.click(getByText('Confirm'));

    // Verify API call
    expect(mockApi.updateUser).toHaveBeenCalledTimes(1);
    expect(mockApi.updateUser).toHaveBeenCalledWith(1, {
      username: 'newUsername',
      email: 'new@email.com',
      isAdmin: true
    });
  });

  it('should call forgotPassword with correct data', async () => {
    const { getByTestId, getByLabelText, getByText } = render(UserList);
    
    await waitFor(() => {
      expect(getByTestId('forgot-pswd-button-1')).toBeDefined();
    });

    // Open password modal
    await fireEvent.click(getByTestId('forgot-pswd-button-1'));

    // Set new password
    await fireEvent.input(getByLabelText('New Password:'), { 
      target: { value: 'newPass123' } 
    });

    // Submit
    await fireEvent.click(getByText('Confirm'));

    // Verify API call
    expect(mockApi.forgotPassword).toHaveBeenCalledTimes(1);
    expect(mockApi.forgotPassword).toHaveBeenCalledWith(
      1,      // userId
      'user1', // username
      'newPass123', // newPassword
      'user1@test.com', // email
      false    // isAdmin
    );
  });
});