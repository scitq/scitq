import { beforeAll, beforeEach } from 'vitest';
import { render, fireEvent, waitFor, screen, getByTestId } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import LoginPage from '../pages/LoginPage.svelte';
import Sidebar from '../components/SideBar.svelte';
import { get } from 'svelte/store';
import { isLoggedIn } from '../lib/Stores/user';
import * as auth from '../lib/auth';
import App from '../App.svelte';

const mockFetch = vi.fn((url, options) => {
  if (url.includes('/login')) {
    return Promise.resolve({
      ok: true,
      headers: {
        get: (h: string) => h === 'Content-Type' ? 'application/json' : null
      },
      json: async () => ({ success: true })
    } as Response);
  }

  if (url.includes('/fetchCookie')) {
    return Promise.resolve({
      ok: true,
      json: async () => ({ token: 'mocked-token-123' })
    } as Response);
  }

  if (url.includes('/logout')) {
    return Promise.resolve({
      ok: true,
      status: 200,
      json: async () => ({}),
    } as Response);
  }

  return Promise.reject(new Error('Unknown URL: ' + url));
});

global.fetch = mockFetch;

// Mock auth module at the beginning of the file
vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth');
  return {
    ...actual,
    logout: vi.fn(actual.logout),
    getToken: vi.fn(),
  };
});

beforeAll(() => {
  // Simulate a delay to ensure the page is ready
  return new Promise((resolve) => setTimeout(resolve, 1000)); // 1 second delay
});

describe('LoginForm', () => {
  it('should render username and password inputs', () => {
    const { getByLabelText } = render(LoginPage);

    expect(getByLabelText('Username')).toBeInTheDocument();
    expect(getByLabelText('Password')).toBeInTheDocument();
  });

  it('should render the login button', () => {
    const { getByText } = render(LoginPage);

    expect(getByText('Log In')).toBeInTheDocument();
  });

  it('should allow typing into inputs', async () => {
    const { getByLabelText } = render(LoginPage);

    const usernameInput = getByLabelText('Username') as HTMLInputElement;
    const passwordInput = getByLabelText('Password') as HTMLInputElement;

    await fireEvent.input(usernameInput, { target: { value: 'myusername' } });
    await fireEvent.input(passwordInput, { target: { value: 'mypassword' } });

    expect(usernameInput.value).toBe('myusername');
    expect(passwordInput.value).toBe('mypassword');
  });
});

describe('Login functionality', () => {
  beforeEach (() => {
    isLoggedIn.set(false);
  })

  it('should trigger login, update store state and display dashboard', async () => {
    const { getByText, getByLabelText, getByTestId } = render(App);
    const usernameInput = getByLabelText('Username') as HTMLInputElement;
    const passwordInput = getByLabelText('Password') as HTMLInputElement;

    await fireEvent.input(usernameInput, { target: { value: 'myusername' } });
    await fireEvent.input(passwordInput, { target: { value: 'mypassword' } });

    expect(getByText('Log In')).toBeInTheDocument();

    // Click on "Log in" button
    await fireEvent.click(getByText('Log In'));

    vi.mocked(auth.getToken).mockResolvedValue('mocked-token-123');
  
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        expect.stringContaining('/login'),
        expect.objectContaining({
          method: 'POST',
          credentials: 'include',
        }),
      );
    });
    await waitFor(()=>{
      // Verify store updates and UI changes
      expect(get(isLoggedIn)).toBe(true);
      expect(getByTestId('dashboard-page')).toBeInTheDocument();
    });
  });
})

describe('Logout functionality', () => {
  beforeEach(() => {
    isLoggedIn.set(true);
  });

  it('should trigger logout, update store state and display login page', async () => {
    const { getByTestId, getByText, queryByText } = render(App);

    // Open the confirmation dialog
    await fireEvent.click(getByTestId('logout-button'));

    expect(getByText('Are you sure you want to log out?')).toBeInTheDocument();

    // Confirm logout by clicking "Log out"
    await fireEvent.click(getByText('Log out'));

    await waitFor(() => {
      // Verify dialog is closed
      expect(queryByText('Are you sure you want to log out?')).toBeNull();
    });

    // Verify logout was called
    expect(auth.logout).toHaveBeenCalled();

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        expect.stringContaining('/logout'),
        expect.objectContaining({
          method: 'POST',
          credentials: 'include',
        }),
      );
    });

    // Verify store updates
    expect(get(isLoggedIn)).toBe(false);
    await waitFor(()=>{
      expect(getByTestId('login-page')).toBeInTheDocument();
    });
    
  });

  it('should cancel logout when clicking cancel', async () => {
    const { getByTestId, getByText } = render(Sidebar);

    await fireEvent.click(getByTestId('logout-button'));
    expect(getByText('Are you sure you want to log out?')).toBeInTheDocument();

    await fireEvent.click(getByText('Cancel'));

    await waitFor(() => {
      expect(getByText('Log Out')).toBeInTheDocument(); // Button still visible
    });
  });
});