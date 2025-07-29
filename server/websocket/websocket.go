package websocket

import (
	"log"
)

func Broadcast(message []byte) {
	mu.Lock()
	defer mu.Unlock()
	for client := range clients {
		err := client.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			log.Printf("Erreur d'envoi WS: %v", err)
			client.Close()
			delete(clients, client)
		}
	}
}
