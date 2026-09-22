package websocket

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
)

// WS event envelope and helper. Every emitted message carries a
// monotonically-increasing EventId so a client that reconnects after a
// dropped WebSocket can ask for replay via ?since=<lastEventId>. Without
// this the UI would silently miss a status flip and stay stale until a
// full page refresh (the source of the "compile appears done before
// hermes finishes" symptom on 2026-09-21).
type WSEvent struct {
	EventId uint64      `json:"event_id"`          // monotonic, filled by EmitWS
	Type    string      `json:"type"`              // entity: "task","worker","workflow","step","job","user"
	Id      int32       `json:"id,omitempty"`      // optional: present for single-entity events
	Action  string      `json:"action,omitempty"`  // e.g., "created","updated","deleted","status"
	Payload interface{} `json:"payload,omitempty"` // additional data
}

// eventCounter feeds EventId. Starts at 1 so a client sending ?since=0
// on a fresh connection asks for "everything currently buffered", which
// is what a new subscriber wants when it wasn't around before.
var eventCounter atomic.Uint64

// nextEventID returns the next monotonic id. Package-scope wrapper so
// tests can reset it (see websocket_reset_test.go).
func nextEventID() uint64 {
	return eventCounter.Add(1)
}

func EmitWS(evType string, id int32, action string, payload interface{}) {
	evID := nextEventID()
	b, err := json.Marshal(WSEvent{
		EventId: evID,
		Type:    evType,
		Id:      id,
		Action:  action,
		Payload: payload,
	})
	if err != nil {
		log.Printf("⚠️ Failed to marshal WS event %s/%d: %v", evType, id, err)
		return
	}
	rememberForReplay(evID, b)
	Broadcast(b)
}

// -- Replay ring buffer -----------------------------------------------------
//
// Keeps the last replayBufferSize emitted messages so a reconnecting
// client can catch up on anything it missed while offline. The buffer
// is per-server-process — restart drops the history, which is fine
// because a new server has no prior state to catch up on and the
// client's ?since= will fall past the oldest id and trigger a reset.
//
// 4096 slots at ~500B/msg ≈ 2 MB. On a busy server that's about a
// minute of history at the highest bursts we've observed (100+
// step-stats/s during a Big Recompute). Bumping is cheap; polling
// interval isn't a concern.
const replayBufferSize = 4096

type replayEntry struct {
	id   uint64
	data []byte
}

var (
	replayMu   sync.RWMutex
	replayBuf  [replayBufferSize]replayEntry
	replayHead int
	replayN    int // number of valid entries (grows up to replayBufferSize, then stays)
)

// rememberForReplay appends one entry to the ring buffer. Overwrites
// the oldest slot when the buffer is full. Bytes stored verbatim so
// replay is a straight write with no re-marshal.
func rememberForReplay(id uint64, data []byte) {
	replayMu.Lock()
	replayBuf[replayHead] = replayEntry{id: id, data: data}
	replayHead = (replayHead + 1) % replayBufferSize
	if replayN < replayBufferSize {
		replayN++
	}
	replayMu.Unlock()
}

// replaySince returns the buffered messages with id > since, oldest
// first. The second return is TRUE when the buffer's window fully
// covers (since, latest] — the client caught up cleanly. FALSE when
// `since` is older than the oldest buffered id, meaning some events
// were lost forever and the caller should send a "reset" marker so
// the client re-fetches state from scratch.
//
// since == 0 is a special case: caller has no prior state, so replay
// nothing and skip the reset marker (returns nil, true).
func replaySince(since uint64) ([][]byte, bool) {
	if since == 0 {
		return nil, true
	}
	replayMu.RLock()
	defer replayMu.RUnlock()
	if replayN == 0 {
		// Never emitted anything since start; nothing to replay, but
		// no gap either — the caller is up to date by definition.
		return nil, true
	}
	// Find the range of valid entries. Slots are populated
	// [replayHead-replayN, replayHead) in mod-replayBufferSize order.
	start := (replayHead - replayN + replayBufferSize) % replayBufferSize
	oldestID := replayBuf[start].id
	covered := since >= oldestID-1 // since=oldestID-1 means "give me from oldest onward"
	var out [][]byte
	for i := 0; i < replayN; i++ {
		e := replayBuf[(start+i)%replayBufferSize]
		if e.id > since {
			// Copy is not needed — the byte slice is immutable once
			// stored (rememberForReplay stores whatever EmitWS built,
			// which nobody mutates after Broadcast). Passing the same
			// slice to multiple clients is safe.
			out = append(out, e.data)
		}
	}
	return out, covered
}

// resetMarker is emitted (out-of-band, without an event_id) to a
// reconnecting client whose ?since= is older than the oldest buffered
// event. Signals "you missed too much — re-fetch state from the REST
// endpoints, then resume from live". No payload; the top-level
// `event_id` field is set to 0 (the sentinel the client uses to skip
// tracking).
func resetMarker() []byte {
	b, _ := json.Marshal(WSEvent{
		Type:   "system",
		Action: "reset",
	})
	return b
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
