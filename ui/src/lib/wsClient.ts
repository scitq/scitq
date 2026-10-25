import { CONFIG } from './config';
import { scitqDebug } from './debug';

type WSMessage = any;
type MessageHandler = (data: WSMessage) => void;

// Messages are delivered to handlers in batches: a batch closes once the
// socket has been quiet for BATCH_QUIET_MS, or BATCH_MAX_MS after its first
// message. All handlers run over the whole batch in one task, so the UI
// renders once per batch. A task passes through several states within a
// few milliseconds (C→R takes ~10 ms, U→S ~15 ms): delivered one by one,
// each state would be drawn for a frame and the value would blink.
export const BATCH_QUIET_MS = 50;
export const BATCH_MAX_MS = 300;

function createWebSocketClient() {
  let socket: WebSocket | null = null;
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  let pingInterval: ReturnType<typeof setInterval> | null = null;
  let shouldReconnect = true;

  // Last event_id observed on any incoming WS message. Passed back to
  // the server as ?since=<id> on reconnect so we replay anything that
  // arrived while the socket was dead. 0 means "no prior state" — a
  // fresh connect that skips replay. See server/websocket/websocket.go.
  let lastEventId = 0;

  // Topics each handler needs: event -> ids, null meaning all ids.
  const topicsByHandler = new Map<MessageHandler, Map<string, Set<number> | null>>();

  let batch: WSMessage[] = [];
  let quietTimer: ReturnType<typeof setTimeout> | null = null;
  let maxTimer: ReturnType<typeof setTimeout> | null = null;

  /** The ids the server must send for event, over all handlers: null for all ids, undefined for none. */
  function wanted(event: string): Set<number> | null | undefined {
    let union: Set<number> | undefined;
    for (const topics of topicsByHandler.values()) {
      if (!topics.has(event)) continue;
      const ids = topics.get(event)!;
      if (ids === null) return null;
      union = new Set([...(union ?? []), ...ids]);
    }
    return union;
  }

  function send(msg: object) {
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(msg));
  }

  function syncEvent(event: string) {
    const ids = wanted(event);
    if (ids === undefined) send({ type: 'unsubscribe', event, ids: [] });
    else send({ type: 'subscribe', event, ids: ids === null ? [] : Array.from(ids) });
  }

  /**
   * Subscribe a handler to the topics it needs (event -> ids; an empty
   * array means all ids of that event). Returns a disposer.
   */
  function subscribeWithTopics(topics: Record<string, number[]>, handler: MessageHandler): () => void {
    const own = new Map<string, Set<number> | null>();
    for (const [event, ids] of Object.entries(topics)) {
      own.set(event, ids.length === 0 ? null : new Set(ids));
    }
    topicsByHandler.set(handler, own);
    for (const event of own.keys()) syncEvent(event);

    return () => {
      topicsByHandler.delete(handler);
      for (const event of own.keys()) syncEvent(event);
    };
  }

  function flush() {
    if (quietTimer) clearTimeout(quietTimer);
    if (maxTimer) clearTimeout(maxTimer);
    quietTimer = maxTimer = null;
    const messages = batch;
    batch = [];
    const handlers = Array.from(topicsByHandler.keys());
    for (const msg of messages) {
      for (const handler of handlers) {
        try {
          handler(msg);
        } catch (err) {
          console.error('❌ WebSocket handler failed', err);
        }
      }
    }
  }

  function enqueue(msg: WSMessage) {
    batch.push(msg);
    if (quietTimer) clearTimeout(quietTimer);
    quietTimer = setTimeout(flush, BATCH_QUIET_MS);
    if (!maxTimer) maxTimer = setTimeout(flush, BATCH_MAX_MS);
  }

  function connect() {
    if (socket && socket.readyState === WebSocket.OPEN) return;
    shouldReconnect = true;

    // Ask the server for a replay of anything emitted since our last
    // observed event_id. On a truly fresh boot lastEventId is 0 and the
    // server sends no replay — normal live connect.
    const url = new URL(`${CONFIG.apiWs}/ws`);
    if (lastEventId > 0) {
      url.searchParams.set('since', String(lastEventId));
    }
    socket = new WebSocket(url.toString());

    socket.onopen = () => {
      console.log('✅ WebSocket connected');
      scitqDebug.recordWsOpen();
      if (reconnectTimeout) {
        clearTimeout(reconnectTimeout);
        reconnectTimeout = null;
      }
      pingInterval = setInterval(() => send({ type: 'ping' }), 30000);

      // Re-subscribe to every topic still wanted.
      const events = new Set<string>();
      for (const topics of topicsByHandler.values()) for (const e of topics.keys()) events.add(e);
      for (const event of events) syncEvent(event);
    };

    socket.onmessage = (event) => {
      let data: WSMessage;
      try {
        data = JSON.parse(event.data);
      } catch (err) {
        console.error('❌ Error parsing WebSocket message', err);
        return;
      }
      if (data.type === 'pong') return;

      // Track the highest event_id seen so a reconnect can ?since=
      // it. event_id === 0 is either the "reset" system marker
      // (passed through below without advancing) or an
      // older-than-envelope message we shouldn't count.
      if (typeof data.event_id === 'number' && data.event_id > lastEventId) {
        lastEventId = data.event_id;
      }
      if (data.type === 'system' && data.action === 'reset') {
        console.warn('⚠️ WebSocket reset: missed messages, consumers should re-fetch');
      }
      // Count every WS message before dispatch — per-second rates by
      // (type, action), the ring buffer of recent events, and a periodic
      // console summary for diagnosing "the page suddenly froze" cases.
      scitqDebug.recordWs(data);
      enqueue(data);
    };

    socket.onclose = (event) => {
      console.warn(`⚠️ WebSocket closed (code: ${event.code})`);
      scitqDebug.recordWsClose(event.code);
      cleanup();
      if (shouldReconnect) {
        reconnectTimeout = setTimeout(connect, 3000);
      }
    };

    socket.onerror = (err) => {
      console.error('❌ WebSocket error', err);
      socket?.close();
    };
  }

  function disconnect() {
    shouldReconnect = false;
    cleanup();
    if (socket) {
      socket.close(1000, 'Client closed connection');
      socket = null;
    }
    topicsByHandler.clear();
  }

  function cleanup() {
    if (pingInterval) {
      clearInterval(pingInterval);
      pingInterval = null;
    }
    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout);
      reconnectTimeout = null;
    }
  }

  return { connect, disconnect, subscribeWithTopics };
}

export const wsClient = createWebSocketClient();
