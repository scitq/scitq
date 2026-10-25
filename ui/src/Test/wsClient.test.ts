import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { wsClient, BATCH_QUIET_MS, BATCH_MAX_MS } from '../lib/wsClient';
import { wsConnect, wsReset, type FakeWebSocket } from './wsHarness';

let ws: FakeWebSocket;
beforeEach(() => {
  vi.useFakeTimers();
  ws = wsConnect();
});
afterEach(() => {
  wsReset();
  vi.useRealTimers();
});

const lastFor = (event: string) => [...ws.sent].reverse().find((m) => m.event === event);

describe('wsClient batching', () => {
  it('delivers messages that arrive close together in one batch', () => {
    const calls: Array<{ msg: number; at: number }> = [];
    wsClient.subscribeWithTopics({ task: [] }, (m) => calls.push({ msg: m.id, at: Date.now() }));

    ws.deliver({ type: 'task', id: 1 });
    vi.advanceTimersByTime(10);
    ws.deliver({ type: 'task', id: 2 });
    vi.advanceTimersByTime(BATCH_QUIET_MS - 1);
    expect(calls).toEqual([]);

    vi.advanceTimersByTime(1);
    expect(calls.map((c) => c.msg)).toEqual([1, 2]);
    expect(calls[0].at).toBe(calls[1].at);
  });

  it('flushes a continuous stream after the maximum delay', () => {
    const got: number[] = [];
    wsClient.subscribeWithTopics({ task: [] }, (m) => got.push(m.id));
    for (let i = 0; i < 10; i++) {
      ws.deliver({ type: 'task', id: i });
      vi.advanceTimersByTime(BATCH_QUIET_MS - 10);
    }
    // 10 messages 40 ms apart: never quiet, so the cap flushed them.
    expect(got.length).toBeGreaterThan(0);
    expect(got.length).toBeLessThan(10);
    expect(BATCH_MAX_MS).toBeLessThan(10 * (BATCH_QUIET_MS - 10));
  });
});

describe('wsClient subscriptions', () => {
  it('keeps an all-ids subscription when a narrower one is disposed', () => {
    const all = wsClient.subscribeWithTopics({ 'step-stats': [] }, () => {});
    const one = wsClient.subscribeWithTopics({ 'step-stats': [7] }, () => {});
    one();
    expect(lastFor('step-stats')).toEqual({ type: 'subscribe', event: 'step-stats', ids: [] });
    all();
    expect(lastFor('step-stats')).toEqual({ type: 'unsubscribe', event: 'step-stats', ids: [] });
  });

  it('subscribes to the union of the ids handlers need', () => {
    const a = wsClient.subscribeWithTopics({ step: [1] }, () => {});
    wsClient.subscribeWithTopics({ step: [2] }, () => {});
    expect(lastFor('step')).toEqual({ type: 'subscribe', event: 'step', ids: [1, 2] });
    a();
    expect(lastFor('step')).toEqual({ type: 'subscribe', event: 'step', ids: [2] });
  });

  it('re-subscribes every wanted topic after a reconnect', () => {
    wsClient.subscribeWithTopics({ worker: [], step: [3] }, () => {});
    ws.close(1006);
    vi.advanceTimersByTime(3000);
    const ws2 = (globalThis as any).WebSocket.last as FakeWebSocket;
    ws2.open();
    expect(ws2.sent).toEqual(expect.arrayContaining([
      { type: 'subscribe', event: 'worker', ids: [] },
      { type: 'subscribe', event: 'step', ids: [3] },
    ]));
  });
});
