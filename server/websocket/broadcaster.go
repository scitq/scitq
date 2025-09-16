package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Maximum message size allowed from peer (adjust if you expect larger control messages).
	maxMessageSize = 1 << 20 // 1 MiB
)

type Client struct {
	conn      *websocket.Conn
	send      chan []byte
	closeOnce sync.Once
	// subscriptions by event type to list of ids; nil slice means all ids for this event
	subs map[string][]int32
}

// Global list of active WebSocket clients
var clients = make(map[*Client]struct{})
var mu sync.Mutex

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // adjust for CORS in production
	},
}

// Message represents the structure of incoming messages
type Message struct {
	Type  string  `json:"type"`            // "ping" | "subscribe" | "unsubscribe"
	Event string  `json:"event,omitempty"` // event type, e.g., "step-stats"
	Ids   []int32 `json:"ids,omitempty"`   // subscribed ids; empty slice means "all ids for this event"
}

func closeClient(c *Client) {
	// Unregister
	mu.Lock()
	delete(clients, c)
	mu.Unlock()
	// Close channel and connection once
	c.closeOnce.Do(func() {
		// Close the sender; writer pump will attempt a normal close frame
		close(c.send)
		// Ensure we don't block forever on close
		_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
		_ = c.conn.Close()
	})
}

func writePump(c *Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			// Set a write deadline for every write operation
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel closed: tell peer we're closing normally, then exit
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("WebSocket write error: %v", err)
				return
			}
		case <-ticker.C:
			// Send ping regularly so intermediaries and clients keep the connection open
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(writeWait)); err != nil {
				// If ping fails, close out — reader will observe the error too
				log.Printf("WebSocket ping error: %v", err)
				return
			}
		}
	}
}

// Handler to be used in ServeMux
func Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	// Optional but helpful to reduce bandwidth on large payloads
	conn.EnableWriteCompression(true)

	// Reader-side protections & heartbeats
	conn.SetReadLimit(maxMessageSize)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(appData string) error {
		// Each pong pushes the read deadline forward
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	c := &Client{
		conn: conn,
		send: make(chan []byte, 256),       // buffered to absorb bursts
		subs: map[string][]int32{"*": nil}, // default: receive all events, all ids
	}

	// Register client
	mu.Lock()
	clients[c] = struct{}{}
	mu.Unlock()

	// Start the single writer goroutine for this connection
	go func() {
		defer closeClient(c)
		writePump(c)
	}()

	// Reader goroutine: never writes to conn directly
	go func() {
		defer closeClient(c)

		for {
			// Ensure we enforce a deadline per read in case PongHandler wasn't invoked
			_ = conn.SetReadDeadline(time.Now().Add(pongWait))
			_, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					// normal closure
				} else {
					log.Printf("WebSocket read error: %v", err)
				}
				return
			}

			var incoming Message
			if err := json.Unmarshal(msg, &incoming); err != nil {
				log.Printf("Invalid JSON: %v", err)
				continue
			}

			if incoming.Type == "ping" {
				respBytes, _ := json.Marshal(map[string]string{"type": "pong"})
				select {
				case c.send <- respBytes:
				default:
					// backpressure: drop pong
				}
				continue
			}

			if incoming.Type == "subscribe" {
				if incoming.Event != "" {
					log.Printf("Subscribe request: event=%s ids=%v", incoming.Event, incoming.Ids)
					mu.Lock()
					if len(incoming.Ids) == 0 {
						// empty ids => all ids for this event
						c.subs[incoming.Event] = nil
					} else {
						// copy ids slice to avoid external mutation
						idsCopy := make([]int32, len(incoming.Ids))
						copy(idsCopy, incoming.Ids)
						c.subs[incoming.Event] = idsCopy
					}
					// no longer keep the wildcard "*" once explicit subscriptions start
					delete(c.subs, "*")
					mu.Unlock()
				}
				continue
			}
			if incoming.Type == "unsubscribe" {
				if incoming.Event != "" {
					mu.Lock()
					if len(incoming.Ids) == 0 {
						// remove the whole event subscription
						delete(c.subs, incoming.Event)
					} else {
						// Simplify: for now, only full-event unsubscribe is supported robustly.
						// Clients should re-subscribe with the exact id set they want.
						delete(c.subs, incoming.Event)
					}
					// If no explicit subs remain, fall back to wildcard to preserve legacy behavior
					if len(c.subs) == 0 {
						c.subs["*"] = nil
					}
					mu.Unlock()
				}
				continue
			}

			// Handle other message types here if needed
			log.Printf("Received message: %+v", incoming)
		}
	}()
}
