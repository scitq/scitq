import { vi } from 'vitest';

export const mockAuth = {
  logout: vi.fn(),
  getToken: vi.fn(),
  getLogin : vi.fn(),
  callOptionsUserToken: vi.fn(),
  getWorkerToken: vi.fn().mockResolvedValue('fake-token'),
  callOptionsWorkerToken: vi.fn().mockResolvedValue({ headers: { Authorization: 'Bearer fake-token' } }),
};