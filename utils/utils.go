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
	runningTasks map[int32]float64 // task_id → weight
}

// NewResizableSemaphore initializes a semaphore with a given size.
func NewResizableSemaphore(size float64) *ResizableSemaphore {
	sem := &ResizableSemaphore{
		tokens:       size,
		maxSize:      size,
		runningTasks: make(map[int32]float64),
	}
	sem.cond = sync.NewCond(&sem.mu)
	return sem
}

func (s *ResizableSemaphore) AcquireWithWeight(ctx context.Context, weight float64, taskID int32) error {
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

// Claim takes weight for taskID without waiting, even beyond the size: for
// a task whose work is already running (re-attached after a client
// restart), which the semaphore must count but cannot hold back.
func (s *ResizableSemaphore) Claim(weight float64, taskID int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens -= weight
	s.runningTasks[taskID] = weight
}

// Share is the fraction of the semaphore that taskID holds: its weight over
// the size, read together so a concurrent resize cannot mix the two. A task
// keeps its share of the worker when the worker is recycled to another step,
// because the server scales the weights of the tasks it holds with the
// concurrency. It is 1 when the size is 0.
func (s *ResizableSemaphore) Share(taskID int32) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxSize <= 0 {
		return 1
	}
	return s.runningTasks[taskID] / s.maxSize
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
func (s *ResizableSemaphore) ReleaseTask(taskID int32) {
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

// return current size of semaphore
func (s *ResizableSemaphore) Size() float64 {
	return s.maxSize
}

// ResizeTasks updates the weights of multiple tasks and adjusts the semaphore accordingly.
// This is useful for bulk updates or when multiple tasks are being resized at once.
func (s *ResizableSemaphore) ResizeTasks(updates map[int32]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for taskID, newWeight := range updates {
		if _, ok := s.runningTasks[taskID]; ok {
			log.Printf("[SEM] Resizing task %d from %.3f to %.3f", taskID, s.runningTasks[taskID], newWeight)
			s.runningTasks[taskID] = newWeight
		}
	}
	s.resetTokensUnsafe()
	s.cond.Broadcast()
}

// ResizeTasks updates the weights of multiple tasks and adjusts the semaphore accordingly.
// This is useful for bulk updates or when multiple tasks are being resized at once.
func (s *ResizableSemaphore) ResizeAll(newSize float64, updates map[int32]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("🔄 Resizing semaphore from %.2f to %.2f\n", s.maxSize, newSize)
	diff := newSize - s.maxSize

	s.tokens += diff
	s.maxSize = newSize

	if diff > 0 {
		s.cond.Broadcast()
	}

	for taskID, newWeight := range updates {
		if _, ok := s.runningTasks[taskID]; ok {
			log.Printf("🔄 Resizing task %d weight from %.2f to %.2f\n", taskID, s.runningTasks[taskID], newWeight)
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
