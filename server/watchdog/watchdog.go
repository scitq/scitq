package watchdog

import (
	"log"
	"sync"
	"time"
)

type IdleStatus int

const (
	IdleStatusBornIdle IdleStatus = iota
	IdleStatusIdle
	IdleStatusNotIdle
)

type Watchdog struct {
	lastPing    sync.Map // workerID -> time.Time
	lastNotIdle sync.Map // workerID -> time.Time
	lastStatus  sync.Map // workerID -> string
	idleStatus  sync.Map // workerID -> IdleStatus
	activeTasks sync.Map // workerID -> int
	isPermanent sync.Map // workerID -> bool

	idleTimeout     time.Duration
	bornIdleTimeout time.Duration
	offlineTimeout  time.Duration

	updateWorker func(workerID int32, newStatus string) error
	deleteWorker func(workerID int32) error

	tickerInterval time.Duration
}

func NewWatchdog(
	idleTimeout, bornIdleTimeout, offlineTimeout, tickerInterval time.Duration,
	updateWorker func(workerID int32, newStatus string) error,
	deleteWorker func(workerID int32) error,
) *Watchdog {
	return &Watchdog{
		idleTimeout:     idleTimeout,
		bornIdleTimeout: bornIdleTimeout,
		offlineTimeout:  offlineTimeout,
		updateWorker:    updateWorker,
		deleteWorker:    deleteWorker,
		tickerInterval:  tickerInterval,
	}
}

// Called when a worker is created
func (w *Watchdog) WorkerRegistered(workerID int32, isPermanent bool) {
	now := time.Now()
	w.lastPing.Store(workerID, now)
	w.lastNotIdle.Store(workerID, now)
	w.lastStatus.Store(workerID, "R")
	w.idleStatus.Store(workerID, IdleStatusBornIdle)
	w.activeTasks.Store(workerID, 0)
	w.isPermanent.Store(workerID, isPermanent)
}

// Called when a worker sends a ping
func (w *Watchdog) WorkerPinged(workerID int32) {
	now := time.Now()
	w.lastPing.Store(workerID, now)
	status, ok := w.lastStatus.Load(workerID)
	var newStatus string
	if !ok {
		log.Printf("⚠️ [watchdog error] Worker %d with no known status just pinged, this should not happend", workerID)
	} else {
		switch status {
		case "O":
			newStatus = "R"
		case "Q":
			newStatus = "P"
		case "L":
			newStatus = "F"
		default:
			return
		}
		log.Printf("[watchdog] Worker %d transitionned status %s -> %s", workerID, status, newStatus)
		w.lastStatus.Store(workerID, newStatus)
		err := w.updateWorker(workerID, newStatus)
		if err != nil {
			log.Printf("⚠️ [watchdog error] Could not update worker %d status in database: %v", workerID, err)
		}
	}
}

// WorkerDeleted should be called when a worker is deleted from the database
func (w *Watchdog) WorkerDeleted(workerID int32) {
	w.lastPing.Delete(workerID)
	w.lastNotIdle.Delete(workerID)
	w.lastStatus.Delete(workerID)
	w.idleStatus.Delete(workerID)
	w.activeTasks.Delete(workerID)
	w.isPermanent.Delete(workerID)

	log.Printf("[watchdog] cleaned up memory for deleted worker %d", workerID)
}

// Called when a worker accepts a task (Assigned ➔ Accepted)
func (w *Watchdog) TaskAccepted(workerID int32) {
	w.activeTasksUpdate(workerID, +1)
	w.idleStatus.Store(workerID, IdleStatusNotIdle)
}

// Called when a worker finishes a task (Success or Failure)
func (w *Watchdog) TaskFinished(workerID int32) {
	now := time.Now()
	w.lastNotIdle.Store(workerID, now)
	if w.activeTasksUpdate(workerID, -1) == 0 {
		w.idleStatus.Store(workerID, IdleStatusIdle)
	}
}

// Internal: updates activeTasks counter
func (w *Watchdog) activeTasksUpdate(workerID int32, delta int) int {
	val, _ := w.activeTasks.LoadOrStore(workerID, 0)
	newCount := val.(int) + delta
	if newCount < 0 {
		newCount = 0
	}
	w.activeTasks.Store(workerID, newCount)
	return newCount
}

// Main watchdog loop
func (w *Watchdog) Run(stopChan <-chan struct{}) {
	ticker := time.NewTicker(w.tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.checkOffline()
			w.checkIdle()
		case <-stopChan:
			log.Println("[watchdog] stopping")
			return
		}
	}
}

// Offline detection
func (w *Watchdog) checkOffline() {
	now := time.Now()
	w.lastPing.Range(func(key, value any) bool {
		workerID := key.(int32)
		lastPing := value.(time.Time)
		elapsed := now.Sub(lastPing)

		if elapsed > w.offlineTimeout {

			status, ok := w.lastStatus.Load(workerID)
			if !ok {
				log.Printf("⚠️ [watchdog error] Worker %d with no known status just timeout, this should not happend", workerID)
				return true
			}
			var newStatus string
			switch status {
			case "R":
				newStatus = "O"
			case "P":
				newStatus = "Q"
			case "F":
				newStatus = "L"
			default:
				return true
			}
			log.Printf("[watchdog] worker %d is offline for %v", workerID, elapsed)
			w.lastStatus.Store(workerID, newStatus)
			err := w.updateWorker(workerID, newStatus)
			if err != nil {
				log.Printf("⚠️ [watchdog error] Could not update worker %d status in database: %v", workerID, err)
			}
		}
		return true
	})
}

// Idle detection and deletion
func (w *Watchdog) checkIdle() {
	now := time.Now()
	w.idleStatus.Range(func(key, value any) bool {
		workerID := key.(int32)
		status := value.(IdleStatus)

		var timeout time.Duration
		switch status {
		case IdleStatusBornIdle:
			timeout = w.bornIdleTimeout
		case IdleStatusIdle:
			timeout = w.idleTimeout
		case IdleStatusNotIdle:
			return true // Skip active workers
		}

		var lastActivity time.Time
		if v, ok := w.lastNotIdle.Load(workerID); ok {
			lastActivity = v.(time.Time)
		} else {
			log.Printf("⚠️ [watchdog error] Worker %d missing lastAccepted, assuming idle now", workerID)
			lastActivity = time.Now() // fallback
			w.lastNotIdle.Store(workerID, lastActivity)
		}

		idleTime := now.Sub(lastActivity) // Convert to seconds
		if idleTime > timeout {
			if v, ok := w.isPermanent.Load(workerID); ok && v.(bool) {
				log.Printf("[watchdog] permanent worker %d idle for %v (no deletion)", workerID, now.Sub(lastActivity))
				return true
			}

			if s, ok := w.lastStatus.Load(workerID); ok && s.(string) == "P" {
				log.Printf("[watchdog] worker %d in pause and idle for %v (no deletion)", workerID, now.Sub(lastActivity))
				return true
			}

			// Double-check still idle before deletion
			if activeTasks, ok := w.activeTasks.Load(workerID); ok && activeTasks.(int) == 0 {
				log.Printf("[watchdog] deleting idle worker %d (idle since %v, timeout %v)", workerID, idleTime, timeout)
				w.deleteWorker(workerID)
				lastActivity = time.Now() // making dying an activity so that we wait for the worker to delete properly
				w.lastNotIdle.Store(workerID, lastActivity)
			}
		}

		return true
	})
}

type WorkerInfo struct {
	WorkerID    int32
	Status      string
	IsPermanent bool
	ActiveTasks int
	LastNotIdle *time.Time
}

func (w *Watchdog) RebuildFromWorkers(workers []WorkerInfo) {
	now := time.Now()

	for _, info := range workers {
		w.lastPing.Store(info.WorkerID, now) // We'll monitor fresh pinging anyway
		w.lastStatus.Store(info.WorkerID, info.Status)
		w.activeTasks.Store(info.WorkerID, info.ActiveTasks)
		w.isPermanent.Store(info.WorkerID, info.IsPermanent)

		var idleStatus IdleStatus
		if info.ActiveTasks > 0 {
			idleStatus = IdleStatusNotIdle
		} else if info.LastNotIdle != nil {
			idleStatus = IdleStatusIdle
			w.lastNotIdle.Store(info.WorkerID, *info.LastNotIdle)
		} else {
			idleStatus = IdleStatusBornIdle
			log.Printf("⚠️ [watchdog] worker %d has never finished a task (born idle detected)", info.WorkerID)
		}

		w.idleStatus.Store(info.WorkerID, idleStatus)

		log.Printf("[watchdog] rebuilt worker %d (status=%s, isPermanent=%v, activeTasks=%d)", info.WorkerID, info.Status, info.IsPermanent, info.ActiveTasks)
	}

	log.Printf("[watchdog] rebuild complete: %d workers loaded", len(workers))
}
