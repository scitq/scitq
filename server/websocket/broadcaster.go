package websocket

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Gestionnaire WebSocket
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // à adapter pour CORS en prod
	},
}

var clients = make(map[*websocket.Conn]bool)
var mu sync.Mutex()

// Handler à utiliser dans ServeMux
func Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Erreur WebSocket upgrade: %v", err)
		return
	}

	mu.Lock()
	clients[conn] = true
	mu.Unlock()

	// Optionnel : écoute des messages entrants si tu veux
	go func() {
		defer func() {
			mu.Lock()
			delete(clients, conn)
			mu.Unlock()
			conn.Close()
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}
