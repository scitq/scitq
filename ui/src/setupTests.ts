// src/setupTests.ts
import '@testing-library/jest-dom';
import { vi } from 'vitest';
import { mockApi } from './mocks/api_mock';
import { mockAuth } from './mocks/auth_mock';

vi.mock('grpc-web', () => {
  return {
    grpc: {}
  };
});

if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = vi.fn().mockImplementation((query) => ({
    matches: query === '(prefers-color-scheme: dark)', // or false depending on your needs
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(), // legacy support
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
}

// Global API mock
vi.mock('../lib/api', () => mockApi);
vi.mock('../lib/auth', () => mockAuth);

// Suppress console logs during tests
vi.spyOn(console, 'error').mockImplementation(() => {});
vi.spyOn(console, 'log').mockImplementation(() => {});
