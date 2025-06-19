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

// Global API mock
vi.mock('../lib/api', () => mockApi);
vi.mock('../lib/auth', () => mockAuth);

// Suppress console logs during tests
vi.spyOn(console, 'error').mockImplementation(() => {});
vi.spyOn(console, 'log').mockImplementation(() => {});
