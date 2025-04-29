import { beforeAll } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { describe, it, expect } from 'vitest';
import LoginPage from '../pages/LoginPage.svelte';

beforeAll(() => {
    // Simulation d'un délai pour garantir que la page est prête
    return new Promise((resolve) => setTimeout(resolve, 1000));  // Délai de 1 seconde
  });


describe('LoginPage', () => {
  it('should render email and password inputs', () => {
    const { getByLabelText } = render(LoginPage);

    expect(getByLabelText('Email')).toBeInTheDocument();
    expect(getByLabelText('Password')).toBeInTheDocument();
  });

  it('should render the login button', () => {
    const { getByText } = render(LoginPage);

    expect(getByText('Log In')).toBeInTheDocument();
  });

  it('should allow typing into inputs', async () => {
    const { getByLabelText } = render(LoginPage);

    const emailInput = getByLabelText('Email') as HTMLInputElement;
    const passwordInput = getByLabelText('Password') as HTMLInputElement;

    await fireEvent.input(emailInput, { target: { value: 'test@example.com' } });
    await fireEvent.input(passwordInput, { target: { value: 'mypassword' } });

    expect(emailInput.value).toBe('test@example.com');
    expect(passwordInput.value).toBe('mypassword');
  });
});
