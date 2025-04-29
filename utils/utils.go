package utils

import (
	"context"
	"log"
	"sync"
)

// ResizableSemaphore manages dynamic concurrency limits and task weights.
type ResizableSemaphore struct {
	mu           sync.Mutex
	cond         *sync.Cond
	tokens       float64
	maxSize      float64
	runningTasks map[uint32]float64 // task_id → weight
}

// NewResizableSemaphore initializes a semaphore with a given size.
func NewResizableSemaphore(size float64) *ResizableSemaphore {
	sem := &ResizableSemaphore{
		tokens:       size,
		maxSize:      size,
		runningTasks: make(map[uint32]float64),
	}
	sem.cond = sync.NewCond(&sem.mu)
	return sem
}

func (s *ResizableSemaphore) AcquireWithWeight(ctx context.Context, weight float64, taskID uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		if s.tokens >= weight {
			break
		}

		// 🚨 Edge-case: no task is running and maxSize > 0 → allow first task in
		if len(s.runningTasks) == 0 && s.maxSize > 0 {
			log.Printf("⚠️ Allowing task %d to run despite token shortfall (maxSize %.2f, requested %.2f)", taskID, s.maxSize, weight)
			break
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.cond.Wait()
	}

	s.tokens -= weight
	s.runningTasks[taskID] = weight
	return nil
}

// reset the tokens based on the current running tasks
// this is called when a task is released to avoid drifting
func (s *ResizableSemaphore) resetTokensUnsafe() {
	total := 0.0
	for _, weight := range s.runningTasks {
		total += weight
	}
	s.tokens = s.maxSize - total
	if s.tokens < 0 {
		log.Printf("⚠️ ResizableSemaphore drift: total task weight %.2f exceeds maxSize %.2f", total, s.maxSize)
	}
}

// ReleaseWeight returns the specific weight to the semaphore.
func (s *ResizableSemaphore) ReleaseTask(taskID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.runningTasks[taskID]; ok {
		delete(s.runningTasks, taskID)
		s.resetTokensUnsafe()
		s.cond.Signal()
	}
}

// Resize adjusts the semaphore size dynamically.
func (s *ResizableSemaphore) Resize(newSize float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("🔄 Resizing semaphore from %.2f to %.2f\n", s.maxSize, newSize)
	diff := newSize - s.maxSize

	s.tokens += diff
	s.maxSize = newSize

	if diff > 0 {
		s.cond.Broadcast()
	}
}

// ResizeTask updates the weight of a task (if the semaphore knows of it) and adjusts the semaphore accordingly.
func (s *ResizableSemaphore) ResizeTaskIfRunning(taskID uint32, newWeight float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if oldWeight, ok := s.runningTasks[taskID]; ok {
		delta := newWeight - oldWeight
		s.tokens -= delta
		s.runningTasks[taskID] = newWeight
		if delta < 0 {
			// if we shrunk capacity, we may want to wake someone
			s.cond.Signal()
		}
	}
}

// ResizeTasks updates the weights of multiple tasks and adjusts the semaphore accordingly.
// This is useful for bulk updates or when multiple tasks are being resized at once.
func (s *ResizableSemaphore) ResizeTasks(updates map[uint32]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for taskID, newWeight := range updates {
		if _, ok := s.runningTasks[taskID]; ok {
			s.runningTasks[taskID] = newWeight
		}
	}
	s.resetTokensUnsafe()
	s.cond.Broadcast()
}

// Cap returns the total capacity of the semaphore.
func (s *ResizableSemaphore) Cap() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxSize
}

// Len returns the current available slots.
func (s *ResizableSemaphore) Len() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}
