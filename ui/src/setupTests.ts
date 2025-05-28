// src/setupTests.ts
import '@testing-library/jest-dom';
import { vi } from 'vitest';
import { mockApi } from './mocks/api_mock';

vi.mock('grpc-web', () => {
  return {
    grpc: {}
  };
});

// Mock global de l'API
vi.mock('../lib/api', () => mockApi);


// Suppression des logs en test
vi.spyOn(console, 'error').mockImplementation(() => {});
vi.spyOn(console, 'log').mockImplementation(() => {});

(globalThis as any).mockApi = mockApi;