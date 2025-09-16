package websocket

import (
	"encoding/json"
	"log"
)

// WS event envelope and helper
type WSEvent struct {
	Type    string      `json:"type"`              // entity: "task","worker","workflow","step","job","user"
	Id      int32       `json:"id,omitempty"`      // optional: present for single-entity events
	Action  string      `json:"action,omitempty"`  // e.g., "created","updated","deleted","status"
	Payload interface{} `json:"payload,omitempty"` // additional data
}

func EmitWS(evType string, id int32, action string, payload interface{}) {
	b, err := json.Marshal(WSEvent{
		Type:    evType,
		Id:      id,
		Action:  action,
		Payload: payload,
	})
	if err != nil {
		log.Printf("⚠️ Failed to marshal WS event %s/%d: %v", evType, id, err)
		return
	}
	Broadcast(b)
}

// OutEvent is the minimal envelope we inspect in outgoing messages.
// Server should always set an event Type and a single Id for routing.
type OutEvent struct {
	Type string `json:"type,omitempty"` // e.g., "step-stats"
	Id   int32  `json:"id,omitempty"`   // e.g., workflow id
}

// allowAll reports whether the client's subscriptions mean "receive everything"
func allowAll(subs map[string][]int32) bool {
	_, ok := subs["*"]
	return ok
}

// matchEvent returns true if the client's subs admit this (type,id) pair.
// Rules:
// - "*" key means receive all events (any type/id).
// - If there is a slice for the event type:
//   - nil slice => all ids for this type
//   - non-empty => id must be in the slice
func matchEvent(subs map[string][]int32, evType string, evId int32) bool {
	if allowAll(subs) {
		return true
	}
	ids, ok := subs[evType]
	if !ok {
		return false
	}
	if len(ids) == 0 {
		return true // all ids for this type
	}
	for _, v := range ids {
		if v == evId {
			return true
		}
	}
	return false
}

// Broadcast sends a message to WebSocket clients.
// If the JSON has a {type, id} envelope, it delivers only to clients whose subscriptions
// match that (type,id). If it does not, it delivers to all (backward compatibility).
func Broadcast(message []byte) {
	var env OutEvent
	useFilter := false
	if err := json.Unmarshal(message, &env); err == nil && env.Type != "" && env.Id != 0 {
		useFilter = true
	}

	var toClose []*Client

	mu.Lock()
	for c := range clients {
		if useFilter && !matchEvent(c.subs, env.Type, env.Id) {
			continue
		}
		select {
		case c.send <- message:
			// enqueued
		default:
			log.Printf("WS send buffer full; closing slow client")
			toClose = append(toClose, c)
		}
	}
	mu.Unlock()

	for _, c := range toClose {
		closeClient(c)
	}
}
