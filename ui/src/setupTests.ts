// src/setupTests.ts
import '@testing-library/jest-dom';
import { vi } from 'vitest';
import { FakeWebSocket } from './Test/wsHarness';

vi.mock('grpc-web', () => {
  return {
    grpc: {}
  };
});

// Node 22+ defines a global localStorage/sessionStorage that is undefined
// unless started with --localstorage-file, and it shadows jsdom's.
class MemoryStorage {
  private m = new Map<string, string>();
  get length() { return this.m.size; }
  key(i: number) { return Array.from(this.m.keys())[i] ?? null; }
  getItem(k: string) { return this.m.has(k) ? this.m.get(k)! : null; }
  setItem(k: string, v: string) { this.m.set(k, String(v)); }
  removeItem(k: string) { this.m.delete(k); }
  clear() { this.m.clear(); }
}
for (const name of ['localStorage', 'sessionStorage']) {
  if (!(globalThis as any)[name]) {
    Object.defineProperty(globalThis, name, { value: new MemoryStorage(), configurable: true });
  }
}

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

// The real wsClient runs over a fake socket: tests feed it messages with
// Test/wsHarness.ts.
(globalThis as any).WebSocket = FakeWebSocket;

// Global API mock
vi.mock('./lib/api', async () => (await import('./mocks/api_mock')).mockApi);
vi.mock('./lib/auth', async () => (await import('./mocks/auth_mock')).mockAuth);

// Suppress console logs during tests
vi.spyOn(console, 'error').mockImplementation(() => {});
vi.spyOn(console, 'log').mockImplementation(() => {});
