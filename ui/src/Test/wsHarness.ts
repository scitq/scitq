// Feeds WebSocket messages to the real wsClient through a fake socket, and
// records every text an element displays, so a test can assert a value
// never shows a wrong or empty intermediate state.
import { tick } from 'svelte';
import { vi } from 'vitest';
import { wsClient, BATCH_MAX_MS } from '../lib/wsClient';

export class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static last: FakeWebSocket | null = null;

  readyState = FakeWebSocket.CONNECTING;
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onerror: ((e: unknown) => void) | null = null;

  constructor(public url: string) {
    FakeWebSocket.last = this;
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close(code = 1000) {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.({ code });
  }
  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }
  deliver(msg: object) {
    this.onmessage?.({ data: JSON.stringify(msg) });
  }
}

/** Opens the client's socket. Call with fake timers installed. */
export function wsConnect(): FakeWebSocket {
  wsClient.connect();
  const ws = FakeWebSocket.last!;
  ws.open();
  return ws;
}

/** Closes the socket and forgets every subscription. */
export function wsReset() {
  wsClient.disconnect();
  FakeWebSocket.last = null;
}

/**
 * Delivers each [delayMs, message] in order, advancing the fake clock by
 * delayMs before each, then lets the last batch flush and Svelte render.
 */
export async function wsPlay(ws: FakeWebSocket, steps: Array<[number, object]>) {
  for (const [delay, msg] of steps) {
    await vi.advanceTimersByTimeAsync(delay);
    ws.deliver(msg);
  }
  await settle();
}

/** Lets pending batches flush and the DOM update. */
export async function settle() {
  await vi.advanceTimersByTimeAsync(BATCH_MAX_MS + 10);
  await tick();
}

/**
 * Records every distinct text the element shows from now on, starting with
 * its current text. Consecutive duplicates are dropped.
 */
export function watchText(el: Element): string[] {
  const norm = () => (el.textContent ?? '').replace(/\s+/g, ' ').trim();
  const seen = [norm()];
  new MutationObserver(() => {
    const t = norm();
    if (t !== seen[seen.length - 1]) seen.push(t);
  }).observe(el, { subtree: true, childList: true, characterData: true, attributes: true });
  return seen;
}

/** Like watchText, for an attribute of the element. */
export function watchAttr(el: Element, name: string): Array<string | null> {
  const seen = [el.getAttribute(name)];
  new MutationObserver(() => {
    const v = el.getAttribute(name);
    if (v !== seen[seen.length - 1]) seen.push(v);
  }).observe(el, { attributes: true, attributeFilter: [name] });
  return seen;
}
