package utils

import (
	"context"
	"testing"
	"time"
)

// A claimed task counts against the size even beyond it: a new task waits
// until enough claimed weight is released.
func TestClaimBeyondSizeHoldsBackNewTasks(t *testing.T) {
	s := NewResizableSemaphore(1)
	s.Claim(1, 1)
	s.Claim(1, 2) // over the size of 1: re-attached work already running

	started := make(chan struct{})
	go func() {
		_ = s.AcquireWithWeight(context.Background(), 1, 3)
		close(started)
	}()
	waiting := func() bool {
		select {
		case <-started:
			return false
		case <-time.After(50 * time.Millisecond):
			return true
		}
	}

	if !waiting() {
		t.Fatal("a new task started while claimed tasks fill the semaphore")
	}
	s.ReleaseTask(1)
	if !waiting() {
		t.Fatal("a new task started while the remaining claim still fills the semaphore")
	}
	s.ReleaseTask(2)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("a new task could not start once the claims were released")
	}
}

// A task held while its worker is recycled keeps its share of the worker:
// the server scales its weight with the concurrency (2 → 32: 1 → 16).
func TestShareFollowsRecycling(t *testing.T) {
	s := NewResizableSemaphore(2)
	if err := s.AcquireWithWeight(context.Background(), 1, 7); err != nil {
		t.Fatal(err)
	}
	if got := s.Share(7); got != 0.5 {
		t.Fatalf("share before recycling %v, want 0.5", got)
	}
	s.ResizeAll(32, map[int32]float64{7: 16})
	if got := s.Share(7); got != 0.5 {
		t.Fatalf("share after recycling %v, want 0.5", got)
	}
	s.Resize(0)
	if got := s.Share(7); got != 1 {
		t.Fatalf("share on a size of 0: %v, want 1", got)
	}
}
