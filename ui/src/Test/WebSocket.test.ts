// src/Test/WebSocket.test.ts

vi.mock('../lib/wsClient', () => {
  return {
    wsClient: {
      subscribeToMessages: vi.fn(),
    }
  };
});

import { wsClient } from '../lib/wsClient';
import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('WebSocket mock test', () => {
  let handlerCalled = false;

  beforeEach(() => {
    vi.clearAllMocks();

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler) => {
      handlerCalled = true;
      handler({ type: 'test-message', payload: 'hello' });
      return () => true;
    });
  });

  it('should mock subscribeToMessages', () => {
    const dummyHandler = vi.fn();
    const unsub = wsClient.subscribeToMessages(dummyHandler);

    expect(dummyHandler).toHaveBeenCalledWith({ type: 'test-message', payload: 'hello' });
    expect(handlerCalled).toBe(true);
    expect(typeof unsub).toBe('function');
  });
});
