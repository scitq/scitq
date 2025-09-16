import { writable } from 'svelte/store';
import { CONFIG } from './config';

type WSMessage = any; // adapte selon tes types
type MessageHandler = (data: WSMessage) => void;

function createWebSocketStore() {
  const { subscribe, set } = writable<WebSocket | null>(null);

  let socket: WebSocket | null = null;
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  let pingInterval: ReturnType<typeof setInterval> | null = null;
  let shouldReconnect = true;

  const handlers = new Set<MessageHandler>();

  // Per-event subscription cache:
  // Map event -> null (means "all ids") OR Set<number> of ids
  const subs = new Map<string, Set<number> | null>();

  // Per-handler contributions to topics (for union-based subscription management)
  // Map handler -> (event -> Set<ids>)
  const contrib = new Map<MessageHandler, Map<string, Set<number>>>();

  function recomputeEvent(event: string) {
    // Build union of all contributed ids for this event
    let union: Set<number> | null = new Set<number>();
    for (const [, eventsMap] of contrib) {
      const s = eventsMap.get(event);
      if (s) {
        for (const id of s) union.add(id);
      }
    }
    // If no contributors, drop subscription entirely
    if (union.size === 0) {
      subs.delete(event);
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'unsubscribe', event, ids: [] }));
      }
      return;
    }
    // Update cache and push subscribe with new union
    subs.set(event, union);
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'subscribe', event, ids: Array.from(union) }));
    }
  }

  /**
   * Subscribe a handler and declare the topics (event -> ids) it needs.
   * - topics: e.g., { 'step-stats': [55], 'worker': [] } (empty array means "all ids" for that event)
   * - Returns a disposer that removes the handler and updates server subscriptions accordingly.
   */
  function subscribeWithTopics(
    topics: Record<string, number[]>,
    handler: MessageHandler
  ): () => void {
    // 1) register handler
    handlers.add(handler);

    // 2) record contributions
    const eventsMap = new Map<string, Set<number>>();
    for (const [event, ids] of Object.entries(topics || {})) {
      if (!event) continue;
      if (!Array.isArray(ids)) continue;

      // Empty array => "all ids" for this event.
      // For union semantics, treat "all ids" as a special case: we store a null in subs and short-circuit.
      if (ids.length === 0) {
        // Mark subs as "all ids" for that event
        subs.set(event, null);
        // Tell server we want all ids for this event
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'subscribe', event, ids: [] }));
        }
        // Do not record concrete id contributions for this handler (null dominates the union)
        continue;
      }

      const set = new Set<number>(ids);
      eventsMap.set(event, set);

      // Merge into existing union in subs
      const current = subs.get(event);
      if (current === null) {
        // already subscribed to all ids; nothing to do
      } else {
        const union = current ?? new Set<number>();
        for (const id of set) union.add(id);
        subs.set(event, union);
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'subscribe', event, ids: Array.from(union) }));
        }
      }
    }
    contrib.set(handler, eventsMap);

    // 3) return disposer
    return () => {
      // Remove handler from handler set
      handlers.delete(handler);

      // Remove this handler's contributions and recompute per event
      const map = contrib.get(handler);
      contrib.delete(handler);

      if (map) {
        for (const [event, idsSet] of map) {
          // If someone previously asked for "all ids" (subs.get(event) === null),
          // we keep "all ids" until that component unmounts. If this handler wasn't the one
          // that requested "all ids", we just recompute the union of concrete ids.
          if (subs.get(event) === null) {
            // Check if ANY remaining contributor had empty ids (i.e., wants all)
            let stillAll = false;
            for (const [, otherMap] of contrib) {
              const otherSet = otherMap.get(event);
              // otherSet being undefined means that handler didn't contribute;
              // "all ids" contributors don't appear in contrib map (treated specially),
              // so we cannot detect them here. We keep "all ids" unless we see there's no subscriber left.
            }
            // If there are no contributors left for this event (no handler has it in their map),
            // drop to recomputeEvent (which will unsubscribe).
            let hasAnyContributor = false;
            for (const [, otherMap] of contrib) {
              if (otherMap.has(event)) { hasAnyContributor = true; break; }
            }
            if (!hasAnyContributor) {
              // No contributors left; unsubscribe
              subs.delete(event);
              if (socket?.readyState === WebSocket.OPEN) {
                socket.send(JSON.stringify({ type: 'unsubscribe', event, ids: [] }));
              }
            }
            // If stillAll is true, do nothing (keep global subscription).
            // Otherwise, if some contributors remain with concrete ids, recompute union:
            if (!hasAnyContributor) {
              // already unsubscribed above
            } else {
              // recompute union from remaining contributors
              recomputeEvent(event);
            }
            continue;
          }

          // Normal case: recompute union by removing this handler's ids
          recomputeEvent(event);
        }
      }
    };
  }

  function connect() {
    if (socket && socket.readyState === WebSocket.OPEN) return;

    socket = new WebSocket(`${CONFIG.apiWs}/ws`);

    socket.onopen = () => {
      console.log('✅ WebSocket connected');
      set(socket);

      if (reconnectTimeout) {
        clearTimeout(reconnectTimeout);
        reconnectTimeout = null;
      }

      pingInterval = setInterval(() => {
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'ping' }));
        }
      }, 30000); // 30s ping

      // Re-subscribe to previous topics on reconnect
      for (const [event, idset] of subs.entries()) {
        const ids = idset === null ? [] : Array.from(idset);
        socket!.send(JSON.stringify({ type: 'subscribe', event, ids }));
      }
    };

    socket.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'pong') return;

        handlers.forEach((handler) => handler(data));
      } catch (err) {
        console.error('❌ Error parsing WebSocket message', err);
      }
    };

    socket.onclose = (event) => {
      console.warn(`⚠️ WebSocket closed (code: ${event.code})`);
      cleanup();
      set(null);

      if (shouldReconnect) {
        reconnectTimeout = setTimeout(() => {
          connect();
        }, 3000);
      }
    };

    socket.onerror = (err) => {
      console.error('❌ WebSocket error', err);
      socket?.close();
    };
  }

  function disconnect() {
    console.log('🔌 WebSocket disconnect requested');
    shouldReconnect = false;
    cleanup();

    if (socket) {
      socket.close(1000, 'Client closed connection');
      socket = null;
      set(null);
    }

    handlers.clear();
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

  function subscribeToMessages(handler: MessageHandler) {
    handlers.add(handler);
    return () => handlers.delete(handler); 
  }

  function wsSubscribe(event: string, ids: number[] = []) {
    if (!event) return;
    // Update local cache first
    if (ids.length === 0) {
      subs.set(event, null); // null => all ids for this event
    } else {
      subs.set(event, new Set(ids));
    }
    // Send to server if socket is open
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'subscribe', event, ids }));
    }
  }

  function wsUnsubscribe(event: string) {
    if (!event) return;
    // Remove from cache
    subs.delete(event);
    // Notify server (ids: [] means full event unsubscribe in our protocol)
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'unsubscribe', event, ids: [] }));
    }
  }

  return {
    subscribe,
    connect,
    disconnect,
    subscribeToMessages,
    wsSubscribe,
    wsUnsubscribe,
    subscribeWithTopics,
  };
}

export const wsClient = createWebSocketStore();
