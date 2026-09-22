package websocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// resetReplay clears the ring buffer and event counter so tests don't
// see leakage from prior EmitWS calls or other tests in this package.
func resetReplay() {
	replayMu.Lock()
	replayHead = 0
	replayN = 0
	replayBuf = [replayBufferSize]replayEntry{}
	replayMu.Unlock()
	eventCounter.Store(0)
}

// eventIDOf pulls the event_id off a raw envelope. Small helper — the
// tests only care about the id, not the whole payload.
func eventIDOf(t *testing.T, b []byte) uint64 {
	t.Helper()
	var env WSEvent
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("parse envelope: %v (raw %q)", err, string(b))
	}
	return env.EventId
}

// TestEmit_AssignsMonotonicID: successive EmitWS calls must produce
// strictly increasing event_ids. This is the load-bearing invariant
// for replay: if two events share an id, a client can't tell them
// apart and either duplicates or drops one on replay.
func TestEmit_AssignsMonotonicID(t *testing.T) {
	resetReplay()
	EmitWS("worker", 1, "created", nil)
	EmitWS("worker", 2, "created", nil)
	EmitWS("worker", 3, "created", nil)
	replayMu.RLock()
	defer replayMu.RUnlock()
	if replayN != 3 {
		t.Fatalf("expected 3 buffered entries, got %d", replayN)
	}
	// Ring layout after 3 emits: entries at [0], [1], [2] with ids 1, 2, 3.
	for i := 0; i < 3; i++ {
		want := uint64(i + 1)
		if replayBuf[i].id != want {
			t.Errorf("slot %d: id=%d, want %d", i, replayBuf[i].id, want)
		}
	}
}

// TestEmit_MonotonicUnderConcurrency: even with many goroutines emitting
// simultaneously, ids stay unique and cover [1..N] with no gaps.
// If atomic.Add were dropped for a plain increment this would flake.
func TestEmit_MonotonicUnderConcurrency(t *testing.T) {
	resetReplay()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			EmitWS("t", int32(i), "x", nil)
		}(i)
	}
	wg.Wait()
	// Collect all ids from the buffer and verify the set is {1..N}.
	seen := map[uint64]bool{}
	replayMu.RLock()
	for i := 0; i < replayN; i++ {
		seen[replayBuf[i].id] = true
	}
	replayMu.RUnlock()
	if len(seen) != N {
		t.Fatalf("expected %d unique ids, got %d", N, len(seen))
	}
	for i := uint64(1); i <= N; i++ {
		if !seen[i] {
			t.Errorf("missing id %d", i)
		}
	}
}

// TestReplaySince_DeliversTail: replaySince(K) returns exactly the
// entries with id > K, in order, when K is inside the buffer window.
// This is the happy-path reconnect: client last saw event 5, server
// has 1..10 buffered, client should receive 6..10.
func TestReplaySince_DeliversTail(t *testing.T) {
	resetReplay()
	for i := 0; i < 10; i++ {
		EmitWS("t", int32(i), "x", nil)
	}
	msgs, covered := replaySince(5)
	if !covered {
		t.Fatal("covered should be true when since is inside the buffer")
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages (ids 6..10), got %d", len(msgs))
	}
	for i, m := range msgs {
		want := uint64(6 + i)
		if got := eventIDOf(t, m); got != want {
			t.Errorf("msg %d: id=%d, want %d", i, got, want)
		}
	}
}

// TestReplaySince_ZeroReturnsNothing: since=0 is the fresh-boot case
// (client has no prior state); we must not flood it with the buffer.
func TestReplaySince_ZeroReturnsNothing(t *testing.T) {
	resetReplay()
	for i := 0; i < 5; i++ {
		EmitWS("t", int32(i), "x", nil)
	}
	msgs, covered := replaySince(0)
	if !covered {
		t.Fatal("covered should be true for since=0 (no gap)")
	}
	if len(msgs) != 0 {
		t.Fatalf("since=0 must not replay; got %d messages", len(msgs))
	}
}

// TestReplaySince_OlderThanBuffer_NotCovered: when since points at an
// id the buffer has already evicted, the caller must learn about the
// gap so it can send a reset marker. We deliver whatever's in the
// buffer AND return covered=false.
func TestReplaySince_OlderThanBuffer_NotCovered(t *testing.T) {
	resetReplay()
	// Emit more than the buffer holds so old ids get evicted.
	const emitCount = replayBufferSize + 100
	for i := 0; i < emitCount; i++ {
		EmitWS("t", 0, "x", nil)
	}
	// since = 10 is definitely evicted.
	msgs, covered := replaySince(10)
	if covered {
		t.Fatal("covered should be false when since is older than the buffer's oldest")
	}
	// The buffer holds the LAST replayBufferSize entries: ids
	// (emitCount-replayBufferSize+1)..emitCount. All of them have id > 10,
	// so replaySince returns the entire buffer.
	if len(msgs) != replayBufferSize {
		t.Fatalf("expected full buffer (%d), got %d", replayBufferSize, len(msgs))
	}
	// Sanity: the first replayed id must be greater than the buffer's
	// oldest boundary and monotonically increasing.
	var last uint64
	for i, m := range msgs {
		id := eventIDOf(t, m)
		if i > 0 && id <= last {
			t.Errorf("msg %d: id=%d not > previous %d", i, id, last)
		}
		last = id
	}
}

// TestResetMarker_IsWellFormed: the reset marker is a valid JSON
// envelope with type=system, action=reset, and event_id=0 (so the
// client doesn't advance its lastEventId to a synthetic value).
func TestResetMarker_IsWellFormed(t *testing.T) {
	m := resetMarker()
	var env WSEvent
	if err := json.Unmarshal(m, &env); err != nil {
		t.Fatalf("marker not valid JSON: %v", err)
	}
	if env.Type != "system" || env.Action != "reset" {
		t.Fatalf("unexpected marker: type=%q action=%q", env.Type, env.Action)
	}
	if env.EventId != 0 {
		t.Fatalf("marker event_id must be 0, got %d", env.EventId)
	}
}

// TestReplaySince_BufferWrap: after the ring wraps at replayBufferSize,
// entries are still returned in monotonic id order — the "read from
// oldest slot around to head" logic must be correct.
func TestReplaySince_BufferWrap(t *testing.T) {
	resetReplay()
	// Fill + wrap by half.
	const total = replayBufferSize + replayBufferSize/2
	for i := 0; i < total; i++ {
		EmitWS("t", 0, "x", nil)
	}
	// since = total - 10 is inside the buffer window; expect the last 10.
	sinceID := uint64(total - 10)
	msgs, covered := replaySince(sinceID)
	if !covered {
		t.Fatal("covered should be true (since inside buffer)")
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		want := sinceID + uint64(i+1)
		if got := eventIDOf(t, m); got != want {
			t.Errorf("msg %d: id=%d, want %d", i, got, want)
		}
	}
}

// TestReplay_NoRaceWithConcurrentEmit: replaySince taken during a
// burst of EmitWS must return a consistent snapshot (no partially-
// written entries, no torn ids). The exact set of returned ids
// depends on scheduling — we just check invariants: monotonic ids,
// all > since, no duplicates.
func TestReplay_NoRaceWithConcurrentEmit(t *testing.T) {
	resetReplay()
	var wg sync.WaitGroup
	var started atomic.Bool
	// Start emitter
	wg.Add(1)
	go func() {
		defer wg.Done()
		started.Store(true)
		for i := 0; i < 500; i++ {
			EmitWS("t", 0, "x", nil)
		}
	}()
	// Wait for emitter to actually start so replay races something.
	for !started.Load() {
	}
	// Take a replay snapshot mid-stream.
	msgs, _ := replaySince(1)
	// Wait for the emitter to finish so no goroutines outlive the test.
	wg.Wait()

	// Snapshot invariants.
	seen := map[uint64]bool{}
	var prev uint64
	for i, m := range msgs {
		id := eventIDOf(t, m)
		if id == 0 {
			t.Fatalf("msg %d: id=0 (torn read)", i)
		}
		if seen[id] {
			t.Errorf("duplicate id %d in snapshot", id)
		}
		seen[id] = true
		if i > 0 && id <= prev {
			t.Errorf("msg %d: id=%d not > previous %d", i, id, prev)
		}
		prev = id
	}
}

// TestEmit_IncludesEventIdInJSON: the wire format carries event_id as
// a top-level field so a client can parse the JSON and advance its
// counter without any framing gymnastics.
func TestEmit_IncludesEventIdInJSON(t *testing.T) {
	resetReplay()
	EmitWS("worker", 42, "created", map[string]string{"hello": "world"})
	replayMu.RLock()
	data := replayBuf[0].data
	replayMu.RUnlock()
	// The JSON must contain the key "event_id" — presence check is
	// enough; TestEmit_AssignsMonotonicID covers the value.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := m["event_id"]; !ok {
		t.Fatalf("envelope missing event_id key: %s", data)
	}
}

// Ensure `fmt` stays used if the file grows a Sprintf-based helper later.
var _ = fmt.Sprintf
