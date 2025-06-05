vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import CreateUserForm from '../components/CreateUserForm.svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('CreateUserForm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('fills the form and creates a new user', async () => {
    // Mock newUser to resolve with an ID
    mockApi.newUser.mockResolvedValue(42);

    // Create a mock for the event handler
    const mockUserCreated = vi.fn();

    const { getByLabelText, getByTestId } = render(CreateUserForm, {
      props: {
        onUserCreated: mockUserCreated
      }
    });

    // Get form fields
    const usernameInput = getByLabelText('Username:') as HTMLInputElement;
    const emailInput = getByLabelText('Email:') as HTMLInputElement;
    const passwordInput = getByLabelText('Password:') as HTMLInputElement;
    const adminCheckbox = getByLabelText('Is Admin:') as HTMLInputElement;
    const createBtn = getByTestId('create-user-button');

    // Fill in fields
    await fireEvent.input(usernameInput, { target: { value: 'testuser' } });
    await fireEvent.input(emailInput, { target: { value: 'test@mail.com' } });
    await fireEvent.input(passwordInput, { target: { value: 'secret123' } });
    await fireEvent.click(adminCheckbox);

    // Click the Create button
    await fireEvent.click(createBtn);

    // Verify newUser was called with correct arguments
    expect(mockApi.newUser).toHaveBeenCalledWith('testuser', 'secret123', 'test@mail.com', true);

    // Verify the onUserCreated event was triggered with the expected data
    await waitFor(() => {
      expect(mockUserCreated).toHaveBeenCalled();
      expect(mockUserCreated.mock.calls[0][0].detail.user).toEqual({
        userId: 42,
        username: 'testuser',
        email: 'test@mail.com',
        isAdmin: true
      });
    });

    // Ensure form fields were reset
    expect(usernameInput.value).toBe('');
    expect(emailInput.value).toBe('');
    expect(passwordInput.value).toBe('');
    expect(adminCheckbox.checked).toBe(false);
  });
});
