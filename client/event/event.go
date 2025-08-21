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
func LogMessage(msg string, client pb.TaskQueueClient, taskID uint32) {
	stream, serr := client.SendTaskLogs(context.Background())
	if serr != nil {
		log.Printf("❌ Failed to open error log stream: %v", serr)
	}
	defer stream.CloseSend()

	stream.Send(&pb.TaskLog{
		TaskId:  taskID,
		LogType: "stderr",
		LogText: msg,
	})
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
func SendRuntimeEventWithRetry(serverAddr, token string, workerID uint32, workerName, level, class, msg string, details map[string]any) {
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
	kind   itemKind
	status string // for itStatus
	msg    string // for itLog or optional accompanying text
}

type taskWorker struct {
	ch   chan item     // FIFO queue; bounded; producers block when full
	quit chan struct{} // stop signal
}

// Reporter now manages per-task workers instead of a global outbox.
type Reporter struct {
	Client     pb.TaskQueueClient
	WorkerID   uint32
	WorkerName string
	Timeout    time.Duration // per-call timeout, e.g. 5s

	mu      sync.Mutex
	workers map[uint32]*taskWorker // taskID -> worker
	wg      sync.WaitGroup         // wait for workers to stop
}

// NewReporter constructs a Reporter; workers are created lazily per task on first update.
func NewReporter(client pb.TaskQueueClient, workerID uint32, workerName string, timeout time.Duration) *Reporter {
	return &Reporter{
		Client:     client,
		WorkerID:   workerID,
		WorkerName: workerName,
		Timeout:    timeout,
		workers:    make(map[uint32]*taskWorker),
	}
}

// getWorker returns (or creates) the per-task worker responsible for serializing sends.
func (r *Reporter) getWorker(taskID uint32) *taskWorker {
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
func (r *Reporter) UpdateTaskAsync(taskID uint32, status, msg string) {
	if r.Client == nil || taskID == 0 || status == "" {
		return
	}
	w := r.getWorker(taskID)
	// enqueue the status item (blocks if queue is full)
	w.ch <- item{kind: itStatus, status: status}
	// optionally enqueue a human/audit message
	if msg != "" {
		w.ch <- item{kind: itLog, msg: msg}
	}
}

// QueueLog enqueues a log message to be sent for the given task.
func (r *Reporter) QueueLog(taskID uint32, msg string) {
	if r.Client == nil || taskID == 0 || msg == "" {
		return
	}
	w := r.getWorker(taskID)
	w.ch <- item{kind: itLog, msg: msg} // blocks if full
}

// runWorker serializes status updates for a single task and ensures the server
// eventually observes the latest state. It performs a few retries per send.
func (r *Reporter) runWorker(taskID uint32, w *taskWorker) {
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
			// Send status with small retries
			timeout := max(r.Timeout, 5*time.Second)
			for attempt := 0; attempt < 3; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				_, err := r.Client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
					TaskId:    taskID,
					NewStatus: it.status,
				})
				cancel()
				if err == nil {
					break
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

func (r *Reporter) UpdateTask(taskID uint32, status string, logMessage string) error {
	if r.Client == nil || taskID == 0 || status == "" {
		return fmt.Errorf("invalid reporter or task parameters")
	}
	ctx, cancel := context.WithTimeout(context.Background(), max(r.Timeout, 5*time.Second))
	defer cancel()

	_, err := r.Client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    taskID,
		NewStatus: status,
	})
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
