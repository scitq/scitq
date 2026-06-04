package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/lib"
)

const idleTimeout = 60 * time.Second

// send simple log message
func LogMessage(msg string, client pb.TaskQueueClient, taskID int32) {
	stream, serr := client.SendTaskLogs(context.Background())
	if serr != nil {
		log.Printf("❌ Failed to open error log stream: %v", serr)
		return
	}
	if stream == nil {
		log.Printf("❌ Received nil stream from SendTaskLogs")
		return
	}
	defer stream.CloseSend()

	if err := stream.Send(&pb.TaskLog{
		TaskId:  taskID,
		LogType: "stderr",
		LogText: msg,
	}); err != nil {
		log.Printf("❌ Failed to send log message: %v", err)
	}
}

func ReportInstallError(c pb.TaskQueueClient, workerName, msg string, err error, ctx context.Context) {
	details := fmt.Sprintf(`{"error":%q}`, err.Error())
	ack, rpcErr := c.ReportWorkerEvent(ctx, &pb.WorkerEvent{
		WorkerName:  workerName,
		Level:       "E",
		EventClass:  "install",
		Message:     msg,
		DetailsJson: details,
	})
	if rpcErr != nil {
		// couldn’t reach server; your policy is to keep trying where appropriate
		// (retry/backoff handled by caller)
		return
	}
	// if your Ack has a boolean, check it
	_ = ack // optional: inspect ack.Ok / ack.Message if you expose that
}

// in cmd/client (or client/event), mirroring sendInstallErrorWithRetry
// prefer using the Reporter struct instead: this function is used in case ther Reporter is not available
func SendRuntimeEventWithRetry(serverAddr, token string, workerID int32, workerName, level, class, msg string, details map[string]any) {
	const maxAttempts = 4
	backoff := 2 * time.Second
	timeout := 5 * time.Second

	workerIDpointer := &workerID
	if workerID == 0 {
		workerIDpointer = nil // if workerID is 0, we don't send it
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		qc, err := lib.CreateClient(serverAddr, token)
		if err != nil {
			time.Sleep(backoff)
			backoff = min(backoff*2, 15*time.Second)
			timeout = minDur(timeout+5*time.Second, 20*time.Second)
			continue
		}
		func() {
			defer qc.Close()
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			var dj string
			if details != nil {
				if b, e := json.Marshal(details); e == nil {
					dj = string(b)
				}
			}
			_, _ = qc.Client.ReportWorkerEvent(ctx, &pb.WorkerEvent{
				WorkerId:    workerIDpointer,
				WorkerName:  workerName,
				Level:       level, // "E","W","I","D"
				EventClass:  class, // "runtime","docker","task","stream","upload","filesystem","auth"
				Message:     msg,
				DetailsJson: dj,
			})
		}()
		return
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// --- Per-task worker with FIFO queue for task status updates and logs -------------

type itemKind int

const (
	itStatus itemKind = iota
	itLog
)

type item struct {
	kind         itemKind
	status       string // for itStatus
	msg          string // for itLog or optional accompanying text
	duration     *int32 // optional duration in seconds for status updates
	failureClass string // for itStatus when status == "F" (oom/timeout/network/other); empty otherwise
}

type taskWorker struct {
	ch   chan item     // FIFO queue; bounded; producers block when full
	quit chan struct{} // stop signal
}

// Reporter now manages per-task workers instead of a global outbox.
type Reporter struct {
	Client     pb.TaskQueueClient
	WorkerID   int32
	WorkerName string
	Timeout    time.Duration // per-call timeout, e.g. 5s

	mu      sync.Mutex
	workers map[int32]*taskWorker // taskID -> worker
	wg      sync.WaitGroup        // wait for workers to stop
}

// NewReporter constructs a Reporter; workers are created lazily per task on first update.
func NewReporter(client pb.TaskQueueClient, workerID int32, workerName string, timeout time.Duration) *Reporter {
	return &Reporter{
		Client:     client,
		WorkerID:   workerID,
		WorkerName: workerName,
		Timeout:    timeout,
		workers:    make(map[int32]*taskWorker),
	}
}

// getWorker returns (or creates) the per-task worker responsible for serializing sends.
func (r *Reporter) getWorker(taskID int32) *taskWorker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w := r.workers[taskID]; w != nil {
		return w
	}
	w := &taskWorker{
		ch:   make(chan item, 256), // bounded FIFO; senders will block when full
		quit: make(chan struct{}),
	}
	r.workers[taskID] = w
	r.wg.Add(1)
	go r.runWorker(taskID, w)
	return w
}

// UpdateTaskAsync enqueues the latest desired status for a task. Newer updates overwrite
// any queued older one (latest-wins coalescing). Sending is serialized per task by its worker.
//
// failureClass is set on terminal F transitions to classify the failure
// (oom / timeout / network / other). The retry-decision path on the server
// consults this when deciding to advance the resource-escalation curve —
// eviction (server-declared) and unset both leave the curve alone. Pass
// "" when the transition isn't a classified failure (success, intermediate
// statuses, transient updates).
func (r *Reporter) UpdateTaskAsync(taskID int32, status, msg string, duration *int32, failureClass string) {
	if r.Client == nil || taskID == 0 || status == "" {
		return
	}
	w := r.getWorker(taskID)
	// enqueue the status item (blocks if queue is full)
	w.ch <- item{kind: itStatus, status: status, duration: duration, failureClass: failureClass}
	// optionally enqueue a human/audit message
	if msg != "" {
		w.ch <- item{kind: itLog, msg: msg}
	}
}

// QueueLog enqueues a log message to be sent for the given task.
func (r *Reporter) QueueLog(taskID int32, msg string) {
	if r.Client == nil || taskID == 0 || msg == "" {
		return
	}
	w := r.getWorker(taskID)
	w.ch <- item{kind: itLog, msg: msg} // blocks if full
}

// runWorker serializes status updates for a single task and ensures the server
// eventually observes the latest state. It performs a few retries per send.
func (r *Reporter) runWorker(taskID int32, w *taskWorker) {
	defer r.wg.Done()
	for {
		var it item
		select {
		case <-w.quit:
			return
		case it = <-w.ch:
			// got an item, proceed
		case <-time.After(idleTimeout):
			// idle timeout, remove worker and exit
			r.mu.Lock()
			delete(r.workers, taskID)
			r.mu.Unlock()
			return
		}

		switch it.kind {
		case itStatus:
			// Terminal statuses (S/F) are the authoritative end-of-task
			// signal: losing one strands the task on the server (pinned in an
			// active state) while the worker has moved on. So retry those
			// until they land (or the worker is told to quit), instead of the
			// bounded best-effort used for transient statuses (R/D/O/V/…),
			// which a newer update supersedes anyway. This stays on the
			// per-task serialized queue, so ordering behind any earlier
			// transient update is preserved — sending the terminal status
			// out-of-band would let it race ahead and be overwritten.
			terminal := it.status == "S" || it.status == "F"
			timeout := max(r.Timeout, 5*time.Second)
			for attempt := 0; ; attempt++ {
				log.Printf("🔄 Updating task %d status to %s (attempt %d)", taskID, it.status, attempt+1)
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				req := &pb.TaskStatusUpdate{
					TaskId:    taskID,
					NewStatus: it.status,
				}
				if it.duration != nil {
					req.Duration = it.duration
				}
				if it.status == "F" && it.failureClass != "" {
					fc := it.failureClass
					req.FailureClass = &fc
				}
				_, err := r.Client.UpdateTaskStatus(ctx, req)
				cancel()
				if err == nil {
					break
				}
				log.Printf("⚠️ update task %d to %s failed: %v", taskID, it.status, err)
				if !terminal && attempt >= 2 {
					break // transient status: give up after 3 tries
				}
				time.Sleep(2 * time.Second)
				if timeout < 15*time.Second {
					timeout += 5 * time.Second
				}
				select {
				case <-w.quit:
					return
				default:
				}
			}
		case itLog:
			// Send log message (best-effort)
			LogMessage(it.msg, r.Client, taskID)
		}
	}
}

// StopOutbox stops all per-task workers. This is best-effort and returns after
// workers have exited their current send (bounded by per-RPC timeout).
func (r *Reporter) StopOutbox() {
	r.mu.Lock()
	for _, w := range r.workers {
		close(w.quit)
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (r *Reporter) Event(level, class, msg string, details map[string]any) {
	if r.Client == nil || r.WorkerID == 0 {
		return // runtime rule: never send without worker_id
	}
	var dj string
	if details != nil {
		if b, _ := json.Marshal(details); b != nil {
			dj = string(b)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), max(r.Timeout, 5*time.Second))
	defer cancel()
	_, _ = r.Client.ReportWorkerEvent(ctx, &pb.WorkerEvent{
		WorkerId:    &r.WorkerID,
		WorkerName:  r.WorkerName,
		Level:       level,
		EventClass:  class,
		Message:     msg,
		DetailsJson: dj,
	})
}

// UpdateTask is the synchronous status-update helper. Pass `failureClass`
// (oom / timeout / network / other) on terminal F transitions so the
// server's retry-decision path can advance the resource-escalation curve
// appropriately; pass "" otherwise.
func (r *Reporter) UpdateTask(taskID int32, status string, logMessage string, failureClass string) error {
	if r.Client == nil || taskID == 0 || status == "" {
		return fmt.Errorf("invalid reporter or task parameters")
	}
	ctx, cancel := context.WithTimeout(context.Background(), max(r.Timeout, 5*time.Second))
	defer cancel()

	req := &pb.TaskStatusUpdate{
		TaskId:    taskID,
		NewStatus: status,
	}
	if status == "F" && failureClass != "" {
		fc := failureClass
		req.FailureClass = &fc
	}
	_, err := r.Client.UpdateTaskStatus(ctx, req)
	if err != nil {
		log.Printf("⚠️ Failed to update task %d status to %s: %v", taskID, status, err)
	} else {
		log.Printf("✅ Task %d updated to status: %s", taskID, status)
	}

	if logMessage != "" {
		// Send log message if provided
		LogMessage(logMessage, r.Client, taskID)
	}
	return err
}

func max(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
