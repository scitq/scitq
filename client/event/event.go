package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/lib"
)

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

type Reporter struct {
	Client     pb.TaskQueueClient
	WorkerID   uint32
	WorkerName string
	Timeout    time.Duration // per-call timeout, e.g. 5s
}

func (r Reporter) Event(level, class, msg string, details map[string]any) {
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

func (r Reporter) UpdateTask(taskID uint32, status string, logMessage string) error {
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
