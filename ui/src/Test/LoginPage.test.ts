import { beforeAll, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import LoginPage from '../pages/LoginPage.svelte';
import Sidebar from '../components/SideBar.svelte';
import { get } from 'svelte/store';
import { isLoggedIn, userInfo } from '../lib/Stores/user';
import * as auth from '../lib/auth';

const mockFetch = vi.fn((url, options) => {
  if (url === 'http://localhost:8081/login') {
    return Promise.resolve({
      ok: true,
      headers: {
        get: (h: string) => h === 'Content-Type' ? 'application/json' : null
      },
      json: async () => ({ success: true })
    } as Response);
  }

  if (url === 'http://localhost:8081/fetchCookie') {
    return Promise.resolve({
      ok: true,
      json: async () => ({ token: 'mocked-token-123' })
    } as Response);
  }

  if (url === 'http://localhost:8081/logout') {
    return Promise.resolve({
      ok: true,
      status: 200,
      json: async () => ({}),
    } as Response);
  }

  return Promise.reject(new Error('Unknown URL: ' + url));
});

global.fetch = mockFetch;

// AT THE TOP OF THE FILE BEFORE EVERYTHING ELSE
vi.mock('../lib/auth', async () => {
  const actual = await vi.importActual<typeof import('../lib/auth')>('../lib/auth');
  return {
    ...actual,
    logout: vi.fn(actual.logout),
  };
});

beforeAll(() => {
  // Simulate a delay to ensure the page is ready
  return new Promise((resolve) => setTimeout(resolve, 1000)); // 1 second delay
});

describe('LoginPage', () => {
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

  it('should perform login and store token (Cookie)', async () => {
    const { getByLabelText, getByText } = render(LoginPage);

    const usernameInput = getByLabelText('Username') as HTMLInputElement;
    const passwordInput = getByLabelText('Password') as HTMLInputElement;
    const loginButton = getByText('Log In');

    await fireEvent.input(usernameInput, { target: { value: 'user1' } });
    await fireEvent.input(passwordInput, { target: { value: 'pass1' } });

    await fireEvent.click(loginButton);

    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8081/login',
      expect.objectContaining({
        method: 'POST',
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ username: 'user1', password: 'pass1' }),
        credentials: 'include'
      })
    );

    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:8081/fetchCookie',
      expect.objectContaining({
        method: 'GET',
        credentials: 'include',
      })
    );

    // Check that getToken correctly retrieved the token
    await waitFor(async () => {
      const token = await auth.getToken();
      expect(token).toBe('mocked-token-123');
    });
  });
});

describe('Logout functionality', () => {
  beforeEach(() => {
    userInfo.set({ token: 'fake-token-123' });
    isLoggedIn.set(true);
  });

  it('should trigger logout and update store state', async () => {
    const { getByTestId, getByText, queryByText } = render(Sidebar);

    // Opens the confirmation popup
    await fireEvent.click(getByTestId('logout-button'));

    expect(getByText('Are you sure you want to log out?')).toBeInTheDocument();

    // Clicks on "Log out"
    await fireEvent.click(getByText('Log out'));

    await waitFor(() => {
      // Popup disappears
      expect(queryByText('Are you sure you want to log out?')).toBeNull();
    });

    // Checks that logout was called
    expect(auth.logout).toHaveBeenCalled();

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        'http://localhost:8081/logout',
        expect.objectContaining({
          method: 'POST',
          credentials: 'include',
        }),
      );
    });

    // Check side effects on stores
    expect(get(userInfo)).toEqual({ token: null });
    expect(get(isLoggedIn)).toBe(false);
  });

  it('should cancel logout when clicking cancel', async () => {
    const { getByTestId, getByText } = render(Sidebar);

    await fireEvent.click(getByTestId('logout-button'));
    expect(getByText('Are you sure you want to log out?')).toBeInTheDocument();

    await fireEvent.click(getByText('Cancel'));

    await waitFor(() => {
      expect(getByText('Log Out')).toBeInTheDocument(); // still visible
    });
  });
});
