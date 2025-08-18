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
    // Mock the API response for new user creation
    mockApi.newUser.mockResolvedValue(42);

    // Create a mock function for the event handler
    const mockUserCreated = vi.fn();

    // Render the component and get DOM elements
    const { getByLabelText, getByTestId } = render(CreateUserForm);

    // Get all form input elements
    const usernameInput = getByLabelText('Username:') as HTMLInputElement;
    const emailInput = getByLabelText('Email:') as HTMLInputElement;
    const passwordInput = getByLabelText('Password:') as HTMLInputElement;
    const adminCheckbox = getByLabelText('Is Admin:') as HTMLInputElement;
    const createBtn = getByTestId('create-user-button');

    // Simulate user input in all fields
    await fireEvent.input(usernameInput, { target: { value: 'testuser' } });
    await fireEvent.input(emailInput, { target: { value: 'test@mail.com' } });
    await fireEvent.input(passwordInput, { target: { value: 'secret123' } });
    await fireEvent.click(adminCheckbox);

    // Simulate form submission
    await fireEvent.click(createBtn);

    // Verify the API was called with correct parameters
    expect(mockApi.newUser).toHaveBeenCalledWith('testuser', 'secret123', 'test@mail.com', true);

    // Verify the form was reset after submission
    expect(usernameInput.value).toBe('');
    expect(emailInput.value).toBe('');
    expect(passwordInput.value).toBe('');
    expect(adminCheckbox.checked).toBe(false);
  });
});