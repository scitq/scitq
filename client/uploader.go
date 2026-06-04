package client

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/scitq/scitq/client/event"
	"github.com/scitq/scitq/fetch"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

const (
	maxUploads     = 20
	uploadRetry    = 3
	uploadInterval = 10 * time.Second
)

// UploadFailure carries a unified failure notification from the uploader
// to a single status update point in the client loop.
type UploadFailure struct {
	Task    *pb.Task
	Message string
}

type SyncCounter struct {
	m sync.Map // map[int32]int
}

func (sc *SyncCounter) Set(taskID int32, count int) {
	sc.m.Store(taskID, count)
}

func (sc *SyncCounter) Decrement(taskID int32) (int, bool) {
	val, ok := sc.m.Load(taskID)
	if !ok {
		return 0, true
	}
	count := val.(int) - 1
	if count <= 0 {
		sc.m.Delete(taskID)
		return 0, true
	}
	sc.m.Store(taskID, count)
	return count, false
}

type UploadFile struct {
	Task       *pb.Task
	SourcePath string
	TargetPath string
}

type UploadManager struct {
	UploadQueue    chan *UploadFile
	Completion     chan *pb.Task
	FailedQueue    chan *UploadFailure
	Store          string
	Client         pb.TaskQueueClient
	reporter       *event.Reporter
	pendingUploads SyncCounter // taskID → remaining files
	uploadStart    sync.Map    // taskID -> time.Time
	RcloneRemotes  *pb.RcloneRemotes
}

func NewUploadManager(store string, client pb.TaskQueueClient, reporter *event.Reporter, rcloneRemotes *pb.RcloneRemotes) *UploadManager {
	return &UploadManager{
		UploadQueue:    make(chan *UploadFile, maxUploads),
		Completion:     make(chan *pb.Task, maxQueueSize),
		FailedQueue:    make(chan *UploadFailure, maxQueueSize),
		Store:          store,
		Client:         client,
		reporter:       reporter,
		pendingUploads: SyncCounter{},
		RcloneRemotes:  rcloneRemotes,
	}
}

func (um *UploadManager) StartUploadWorkers(activeTasks *sync.Map) {
	for i := 0; i < maxUploads; i++ {
		go func() {
			for file := range um.UploadQueue {
				um.uploadFile(file)
			}
		}()
	}

	go um.watchCompletions(activeTasks)
}

func (um *UploadManager) EnqueueTaskOutput(task *pb.Task) {
	// Determine upload target(s). Default ("move"): on success with publish,
	// upload to publish path *only*. On failure or no publish, upload to
	// workspace output path *only*.
	//
	// publish_mode="copy" (spec: addition_from_nextflow.md B): on success with
	// publish, upload to *both* workspace and publish so a same-workflow
	// downstream consumer can still fetch from the workspace while the
	// results bucket also receives the artefacts. publish_mode is ignored
	// on failure (failed tasks always upload to workspace only, never
	// publish — see the docs).
	var uploadTargets []*string
	if task.Status == "S" && task.Publish != nil && *task.Publish != "" {
		uploadTargets = append(uploadTargets, task.Publish)
		if task.PublishMode != nil && *task.PublishMode == "copy" && task.Output != nil && *task.Output != "" {
			uploadTargets = append(uploadTargets, task.Output)
		}
	} else if task.Output != nil {
		uploadTargets = append(uploadTargets, task.Output)
	}

	if len(uploadTargets) == 0 {
		log.Printf("⚠️ Task %d has no output target; skipping upload", task.TaskId)
		um.Completion <- task
		return
	}

	outputDir := filepath.Join(um.Store, "tasks", fmt.Sprint(task.TaskId), "output")
	files := make([]string, 0, 64)
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("⚠️ Output directory does not exist for task %d", task.TaskId)
			um.Completion <- task
			return
		}
		message := fmt.Sprintf("Failed walking output directory: %v", err)
		log.Printf("❌ %s", message)
		um.reporter.Event("E", "upload", message, map[string]any{
			"task_id": task.TaskId,
			"error":   err.Error(),
		})
		task.Status = "F"
		// "network" covers the uploader's I/O failures (walk, copy,
		// transport). The retry-decision path treats it the same as
		// oom/timeout/other (it advances the curve); the distinction
		// is preserved for operator visibility / future policy.
		fc := "network"
		task.FailureClass = &fc
		um.Completion <- task
		return
	}
	count := len(files)
	if count == 0 {
		log.Printf("⚠️ No files to upload for task %d", task.TaskId)
		um.Completion <- task
		return
	}
	// Set counters/timers BEFORE enqueuing to avoid races. With publish_mode=copy
	// each file is enqueued to *both* targets, so the pending counter is
	// scaled by the number of targets — that's what watchCompletions decrements
	// once per upload.
	um.uploadStart.Store(task.TaskId, time.Now())
	um.pendingUploads.Set(task.TaskId, count*len(uploadTargets))

	for _, path := range files {
		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			log.Printf("❌ Failed to get relative path for %s: %v", path, err)
			// degrade gracefully: skip this file and decrement the expected
			// count by one per target (each missing relpath would have
			// produced len(uploadTargets) enqueues).
			for range uploadTargets {
				if remaining, done := um.pendingUploads.Decrement(task.TaskId); done {
					um.Completion <- task
				} else {
					log.Printf("📤 Remaining uploads for task %d after relpath error: %d", task.TaskId, remaining)
				}
			}
			continue
		}
		for _, ut := range uploadTargets {
			target := fetch.Join(*ut, relPath)
			um.UploadQueue <- &UploadFile{Task: task, SourcePath: path, TargetPath: target}
		}
	}
}

func (um *UploadManager) uploadFile(file *UploadFile) error {
	var err error
	var message string
	for attempt := 1; attempt <= uploadRetry; attempt++ {
		err = fetch.Copy(um.RcloneRemotes, file.SourcePath, file.TargetPath)
		if err == nil {
			log.Printf("✅ Uploaded: %s → %s", file.SourcePath, file.TargetPath)
			um.Completion <- file.Task
			return nil
		}
		message = fmt.Sprintf("⚠️ Upload attempt %d failed for %s: %v", attempt, file.SourcePath, err)
		log.Print(message)
		time.Sleep(uploadInterval)
	}
	message = fmt.Sprintf("Failed to upload %s after %d attempts: %v", file.SourcePath, uploadRetry, err)
	log.Printf("❌ %s", message)
	um.reporter.Event("E", "upload", message, map[string]any{
		"source":   file.SourcePath,
		"target":   file.TargetPath,
		"task_id":  file.Task.TaskId,
		"attempts": uploadRetry,
		"error":    err.Error(),
	})
	// Centralize failure: do not update status here; enqueue to FailedQueue
	select {
	case um.FailedQueue <- &UploadFailure{Task: file.Task, Message: message}:
	default:
		log.Printf("⚠️ FailedQueue full; could not enqueue upload failure for task %d", file.Task.TaskId)
	}
	return err
}

func (um *UploadManager) watchCompletions(activeTasks *sync.Map) {
	for task := range um.Completion {
		taskID := task.TaskId
		if remaining, done := um.pendingUploads.Decrement(taskID); done {
			log.Printf("📤 Upload completed for task %d, marking as %s", taskID, task.Status)
			var secs int32 = 0
			if v, ok := um.uploadStart.Load(taskID); ok {
				if start, ok2 := v.(time.Time); ok2 {
					secs = int32(time.Since(start).Seconds())
				}
				um.uploadStart.Delete(taskID)
			}
			// Send the terminal status through the reporter's per-task
			// serialized queue (NOT a direct/out-of-band send): updates for a
			// task must stay ordered behind any earlier R/V update, otherwise
			// the terminal status can land first and a late-arriving 'V' pins
			// the task as active on the server. Reliability against a lost
			// terminal update is handled server-side by the ping-time
			// reconciliation (it fails tasks the worker no longer tracks),
			// which is ordering-safe and also covers worker crashes.
			// Carry the failure_class set earlier (by executeTask's
			// classifyExecFailure, or by the uploader itself on its own
			// upload failures) through the terminal F status. Empty on
			// success / non-failure transitions.
			fc := ""
			if task.Status == "F" && task.FailureClass != nil {
				fc = *task.FailureClass
			}
			um.reporter.UpdateTaskAsync(taskID, task.Status, "", &secs, fc)
			cleanupTaskWorkingDir(um.Store, taskID, um.reporter)
			activeTasks.Delete(taskID)
		} else {
			log.Printf("📤 Remaining uploads for task %d: %d", taskID, remaining)
		}
	}
}

// RunUploader starts upload workers and returns the manager.
func RunUploader(store string, client pb.TaskQueueClient, activeTasks *sync.Map, reporter *event.Reporter, rcloneRemotes *pb.RcloneRemotes) *UploadManager {
	um := NewUploadManager(store, client, reporter, rcloneRemotes)
	go um.StartUploadWorkers(activeTasks)
	return um
}
