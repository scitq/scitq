package websocket

import (
	"log"
	"github.com/gorilla/websocket"
)

// Broadcast sends a message to all connected WebSocket clients
func Broadcast(message []byte) {
	mu.Lock()
	defer mu.Unlock()
	for client := range clients {
		err := client.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			log.Printf("WS send error: %v", err)
			client.Close()
			delete(clients, client)
		}
	}
}