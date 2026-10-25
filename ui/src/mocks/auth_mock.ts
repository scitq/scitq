import { vi } from 'vitest';

export const mockAuth = {
  logout: vi.fn(),
  getToken: vi.fn(),
  getLogin : vi.fn(),
  callOptionsUserToken: vi.fn(),
};