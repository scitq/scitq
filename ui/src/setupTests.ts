// src/setupTests.ts
import '@testing-library/jest-dom';
import { vi } from 'vitest';

vi.mock('grpc-web', () => {
  return {
    grpc: {}
  };
});