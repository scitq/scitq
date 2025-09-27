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
	lastPing     sync.Map // workerID -> time.Time
	lastNotIdle  sync.Map // workerID -> time.Time
	lastStatus   sync.Map // workerID -> string
	idleStatus   sync.Map // workerID -> IdleStatus
	activeTasks  sync.Map // workerID -> int
	isPermanent  sync.Map // workerID -> bool
	offlineSince sync.Map // workerID -> time.Time (when first marked offline)

	recentlyDeleted sync.Map // workerID -> time.Time

	idleTimeout           time.Duration
	bornIdleTimeout       time.Duration
	offlineTimeout        time.Duration
	consideredLostTimeout time.Duration

	updateWorker func(workerID int32, newStatus string) error
	deleteWorker func(workerID int32) error
	warmCheck    func(workerID int32) (bool, time.Duration) // (has A/C/O, extra delay)

	tickerInterval time.Duration
}

func NewWatchdog(
	idleTimeout, bornIdleTimeout, offlineTimeout, consideredLostTimeout, tickerInterval time.Duration,
	updateWorker func(workerID int32, newStatus string) error,
	deleteWorker func(workerID int32) error,
	warmCheck func(workerID int32) (bool, time.Duration),
) *Watchdog {
	return &Watchdog{
		idleTimeout:           idleTimeout,
		bornIdleTimeout:       bornIdleTimeout,
		offlineTimeout:        offlineTimeout,
		consideredLostTimeout: consideredLostTimeout,
		updateWorker:          updateWorker,
		deleteWorker:          deleteWorker,
		warmCheck:             warmCheck,
		tickerInterval:        tickerInterval,
	}
}

// Called right after we create the DB row + cloud deploy job, before the worker is ready
func (w *Watchdog) WorkerSpawned(workerID int32, isPermanent bool) {
	now := time.Now()
	w.lastPing.Store(workerID, now)
	w.lastNotIdle.Store(workerID, now)
	w.lastStatus.Store(workerID, "I")
	w.idleStatus.Store(workerID, IdleStatusBornIdle)
	w.activeTasks.Store(workerID, 0)
	w.isPermanent.Store(workerID, isPermanent)
	// make sure long-offline timers start from spawn time if needed
	w.offlineSince.Store(workerID, now)
	log.Printf("[watchdog] spawned worker %d tracked as Installing", workerID)
}

// Called when a worker is registered (comes online and is running)
func (w *Watchdog) WorkerRegistered(workerID int32, isPermanent bool) {
	now := time.Now()
	w.lastPing.Store(workerID, now)
	w.offlineSince.Delete(workerID)
	w.lastStatus.Store(workerID, "R")
	w.isPermanent.Store(workerID, isPermanent)
	log.Printf("[watchdog] worker %d registered as Running", workerID)
}

// Called when a worker sends a ping
func (w *Watchdog) WorkerPinged(workerID int32) {
	now := time.Now()
	w.lastPing.Store(workerID, now)
	w.offlineSince.Delete(workerID)
	statusVal, ok := w.lastStatus.Load(workerID)
	var newStatus string
	if !ok {
		log.Printf("⚠️ [watchdog error] Worker %d with no known status just pinged, this should not happen", workerID)
	} else {
		s := statusVal.(string)
		switch s {
		case "O":
			newStatus = "R"
		case "Q":
			newStatus = "P"
		case "L":
			newStatus = "F"
		default:
			return
		}
		log.Printf("[watchdog] Worker %d transitionned status %s -> %s", workerID, s, newStatus)
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
	w.offlineSince.Delete(workerID)

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
	go w.startCleanupLoop(stopChan)

	ticker := time.NewTicker(w.tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.checkOffline()
			w.checkIdle()
			w.checkLongOffline()
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
		// Threshold depends on status: 'I' (installing) uses bornIdleTimeout, others use offlineTimeout
		threshold := w.offlineTimeout
		statusVal, ok := w.lastStatus.Load(workerID)
		if ok && statusVal.(string) == "I" {
			threshold = w.bornIdleTimeout
		}
		if elapsed > threshold {
			if !ok {
				log.Printf("⚠️ [watchdog error] Worker %d with no known status just timeout, this should not happen", workerID)
				return true
			}
			s := statusVal.(string)
			var newStatus string
			switch s {
			case "R":
				newStatus = "O"
			case "P":
				newStatus = "Q"
			case "F":
				newStatus = "L"
			case "I":
				newStatus = "O"
			default:
				return true
			}
			log.Printf("[watchdog] worker %d is offline for %v", workerID, elapsed)
			w.lastStatus.Store(workerID, newStatus)
			w.offlineSince.Store(workerID, now)
			go w.safeUpdateWorker(workerID, newStatus)
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
			// Double-check still idle before deletion
			if activeTasks, ok := w.activeTasks.Load(workerID); ok && activeTasks.(int) == 0 {
				if w.warmCheck != nil {
					if isWarm, extra := w.warmCheck(workerID); isWarm {
						effective := w.idleTimeout
						if extra > 0 && w.idleTimeout+extra > effective {
							effective = w.idleTimeout + extra
						}
						if idleTime <= effective {
							log.Printf("[watchdog] worker %d warm; idle=%v <= threshold=%v (postponing deletion)", workerID, idleTime, effective)
							return true
						}
						log.Printf("[watchdog] worker %d warm beyond threshold (idle=%v > threshold=%v); deleting", workerID, idleTime, effective)
					}
				}
				log.Printf("[watchdog] deleting idle worker %d (idle since %v, timeout %v)", workerID, idleTime, timeout)
				go w.safeDeleteWorker(workerID)
				lastActivity = time.Now()
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

// Non-blocking wrappers with timeout logging so slow actions don't stall the watchdog loop
func (w *Watchdog) safeUpdateWorker(workerID int32, newStatus string) {
	done := make(chan struct{})
	go func() {
		if err := w.updateWorker(workerID, newStatus); err != nil {
			log.Printf("⚠️ [watchdog error] Could not update worker %d status in database: %v", workerID, err)
		}
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(60 * time.Second):
		log.Printf("⏱️ [watchdog] updateWorker(%d,%s) still running after 60s; continuing loop", workerID, newStatus)
		return
	}
}

func (w *Watchdog) safeDeleteWorker(workerID int32) {
	if t, ok := w.recentlyDeleted.Load(workerID); ok {
		log.Printf("[watchdog] skipping duplicate delete for worker %d (already deleted at %v)", workerID, t)
		return
	}
	w.recentlyDeleted.Store(workerID, time.Now())

	done := make(chan struct{})
	go func() {
		if err := w.deleteWorker(workerID); err != nil {
			log.Printf("⚠️ [watchdog error] Could not delete worker %d: %v", workerID, err)
		}
		close(done)
	}()
	select {
	case <-done:
		return
	case <-time.After(60 * time.Second):
		log.Printf("⏱️ [watchdog] deleteWorker(%d) still running after 60s; continuing loop", workerID)
		return
	}
}

func isOfflineStatus(s string) bool {
	return s == "O" || s == "Q" || s == "L"
}

// If a worker has been offline for > consideredLostTimeout, delete it (non-permanent only)
func (w *Watchdog) checkLongOffline() {
	now := time.Now()
	w.lastStatus.Range(func(key, value any) bool {
		workerID := key.(int32)
		status := value.(string)
		if !isOfflineStatus(status) {
			return true
		}
		// Determine when we first marked it offline
		var since time.Time
		if v, ok := w.offlineSince.Load(workerID); ok {
			since = v.(time.Time)
		} else {
			// seed from lastPing if available; otherwise start the timer now
			if lp, ok := w.lastPing.Load(workerID); ok {
				since = lp.(time.Time)
			} else {
				since = now
			}
			w.offlineSince.Store(workerID, since)
		}

		if now.Sub(since) > w.consideredLostTimeout {
			if v, ok := w.isPermanent.Load(workerID); ok && v.(bool) {
				return true
			}
			log.Printf("[watchdog] deleting long-offline worker %d (offline for %v)", workerID, now.Sub(since))
			go w.safeDeleteWorker(workerID)
		}
		return true
	})
}

func (w *Watchdog) startCleanupLoop(stopChan <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-4 * time.Minute)
			w.recentlyDeleted.Range(func(key, value any) bool {
				if ts, ok := value.(time.Time); ok && ts.Before(cutoff) {
					w.recentlyDeleted.Delete(key)
				}
				return true
			})
		case <-stopChan:
			return
		}
	}
}
