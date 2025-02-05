package utils

import (
	"context"
	"fmt"
	"sync"
)

// ResizableSemaphore manages dynamic concurrency limits.
type ResizableSemaphore struct {
	mu      sync.Mutex
	cond    *sync.Cond
	tokens  int
	maxSize int
}

// NewResizableSemaphore initializes a semaphore with a given size.
func NewResizableSemaphore(size int) *ResizableSemaphore {
	sem := &ResizableSemaphore{
		tokens:  size,
		maxSize: size,
	}
	sem.cond = sync.NewCond(&sem.mu)
	return sem
}

// Acquire blocks until a token is available.
func (s *ResizableSemaphore) Acquire(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for s.tokens == 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.cond.Wait()
	}

	s.tokens--
	return nil
}

// Release returns a token to the semaphore.
func (s *ResizableSemaphore) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens++
	s.cond.Signal() // Wake up one waiting goroutine
}

// Resize adjusts the semaphore size dynamically.
func (s *ResizableSemaphore) Resize(newSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("🔄 Resizing semaphore from %d to %d\n", s.maxSize, newSize)
	diff := newSize - s.maxSize

	// Adjust token count
	s.tokens += diff
	s.maxSize = newSize

	// Wake up all waiting goroutines in case of expansion
	if diff > 0 {
		s.cond.Broadcast()
	}
}

// Cap returns the total capacity of the semaphore.
func (s *ResizableSemaphore) Cap() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxSize
}

// Len returns the current available slots.
func (s *ResizableSemaphore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}
