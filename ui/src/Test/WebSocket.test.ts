// src/Test/WebSocket.test.ts

import { wsClient } from '../lib/wsClient';
import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('WebSocket topic-aware subscription mock test', () => {
  let handlerCalled = false;

  beforeEach(() => {
    vi.clearAllMocks();
    handlerCalled = false;

    // Mock the topic-aware WebSocket subscription functionality
    vi.spyOn(wsClient, 'subscribeWithTopics').mockImplementation((topics: Record<string, number[] | []>, handler: any) => {
      handlerCalled = true;
      // Immediately call the handler with a test message in the new envelope
      handler({ type: 'test', action: 'ping', payload: { hello: 'world' } });
      // Return unsubscribe function
      return () => true;
    });
  });

  it('should mock subscribeWithTopics and invoke handler', () => {
    // Create mock handler function
    const dummyHandler = vi.fn();

    // Test subscription functionality with a dummy topic
    const unsub = wsClient.subscribeWithTopics({ test: [] }, dummyHandler);

    // Verify handler was called with expected message
    expect(dummyHandler).toHaveBeenCalledWith({ type: 'test', action: 'ping', payload: { hello: 'world' } });

    // Verify subscription was triggered
    expect(handlerCalled).toBe(true);

    // Verify unsubscribe function is returned
    expect(typeof unsub).toBe('function');

    // Call the unsubscribe to ensure no throw
    expect(() => unsub()).not.toThrow();
  });
});