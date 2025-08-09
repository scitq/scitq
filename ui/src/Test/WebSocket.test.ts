// src/Test/WebSocket.test.ts

import { wsClient } from '../lib/wsClient';
import { describe, it, expect, vi, beforeEach } from 'vitest';

describe('WebSocket mock test', () => {
  let handlerCalled = false;

  beforeEach(() => {
    vi.clearAllMocks();

    // Mock the WebSocket subscription functionality
    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
      handlerCalled = true; 
      // Immediately call the handler with test message
      handler({ type: 'test-message', payload: 'hello' });
      // Return unsubscribe function
      return () => true;
    });
  });

  it('should mock subscribeToMessages', () => {
    // Create mock handler function
    const dummyHandler = vi.fn();
    
    // Test subscription functionality
    const unsub = wsClient.subscribeToMessages(dummyHandler);

    // Verify handler was called with expected message
    expect(dummyHandler).toHaveBeenCalledWith({ type: 'test-message', payload: 'hello' });
    
    // Verify subscription was triggered
    expect(handlerCalled).toBe(true);
    
    // Verify unsubscribe function is returned
    expect(typeof unsub).toBe('function');
  });
});