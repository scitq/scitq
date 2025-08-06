import { writable } from 'svelte/store';

type WSMessage = any; // adapte selon tes types
type MessageHandler = (data: WSMessage) => void;

function createWebSocketStore() {
  const { subscribe, set } = writable<WebSocket | null>(null);

  let socket: WebSocket | null = null;
  let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
  let pingInterval: ReturnType<typeof setInterval> | null = null;
  let shouldReconnect = true;

  const handlers = new Set<MessageHandler>();

  function connect() {
    if (socket && socket.readyState === WebSocket.OPEN) return;

    socket = new WebSocket('wss://alpha2.gmt.bio/ws');

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
    return () => handlers.delete(handler); // unsubscribe function
  }

  return {
    subscribe,
    connect,
    disconnect,
    subscribeToMessages,
  };
}

export const wsClient = createWebSocketStore();
