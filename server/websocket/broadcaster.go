package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // adjust for CORS in production
	},
}

// Global list of active WebSocket clients
var clients = make(map[*websocket.Conn]bool)
var mu sync.Mutex

// Message represents the structure of incoming messages
type Message struct {
	Type string `json:"type"`
	// Add more fields as needed
}

// Handler to be used in ServeMux
func Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	mu.Lock()
	clients[conn] = true
	mu.Unlock()

	// Optional: listen for incoming messages if needed
	go func() {
		defer func() {
			mu.Lock()
			delete(clients, conn)
			mu.Unlock()
			conn.Close()
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("WebSocket read error: %v", err)
				break
			}

			var incoming Message
			if err := json.Unmarshal(msg, &incoming); err != nil {
				log.Printf("Invalid JSON: %v", err)
				continue
			}

			if incoming.Type == "ping" {
				// Respond with a pong message
				response := map[string]string{"type": "pong"}
				respBytes, _ := json.Marshal(response)
				if err := conn.WriteMessage(websocket.TextMessage, respBytes); err != nil {
					log.Printf("Failed to send pong: %v", err)
				}
				continue
			}

			// Handle other message types here
			log.Printf("Received message: %+v", incoming)
		}
	}()
}
