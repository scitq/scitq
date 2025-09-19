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
	Store          string
	Client         pb.TaskQueueClient
	reporter       *event.Reporter
	pendingUploads SyncCounter // taskID → remaining files
	uploadStart    sync.Map    // taskID -> time.Time
}

func NewUploadManager(store string, client pb.TaskQueueClient, reporter *event.Reporter) *UploadManager {
	return &UploadManager{
		UploadQueue:    make(chan *UploadFile, maxUploads),
		Completion:     make(chan *pb.Task, maxQueueSize),
		Store:          store,
		Client:         client,
		reporter:       reporter,
		pendingUploads: SyncCounter{},
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
	if task.Output == nil {
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
		um.Completion <- task
		return
	}
	count := len(files)
	if count == 0 {
		log.Printf("⚠️ No files to upload for task %d", task.TaskId)
		um.Completion <- task
		return
	}
	// Set counters/timers BEFORE enqueuing to avoid races
	um.uploadStart.Store(task.TaskId, time.Now())
	um.pendingUploads.Set(task.TaskId, count)

	for _, path := range files {
		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			log.Printf("❌ Failed to get relative path for %s: %v", path, err)
			// degrade gracefully: skip this file and decrement the expected count
			if remaining, done := um.pendingUploads.Decrement(task.TaskId); done {
				um.Completion <- task
			} else {
				log.Printf("📤 Remaining uploads for task %d after relpath error: %d", task.TaskId, remaining)
			}
			continue
		}
		target := fetch.Join(*task.Output, relPath)
		um.UploadQueue <- &UploadFile{Task: task, SourcePath: path, TargetPath: target}
	}
}

func (um *UploadManager) uploadFile(file *UploadFile) error {
	var err error
	var message string
	for attempt := 1; attempt <= uploadRetry; attempt++ {
		err = fetch.Copy(fetch.DefaultRcloneConfig, file.SourcePath, file.TargetPath)
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
	// Log the failure and update the task status
	file.Task.Status = "F"
	um.Completion <- file.Task
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
			um.reporter.UpdateTaskAsync(taskID, task.Status, "", &secs)
			cleanupTaskWorkingDir(um.Store, taskID, um.reporter)
			activeTasks.Delete(taskID)
		} else {
			log.Printf("📤 Remaining uploads for task %d: %d", taskID, remaining)
		}
	}
}

// RunUploader starts upload workers and returns the manager.
func RunUploader(store string, client pb.TaskQueueClient, activeTasks *sync.Map, reporter *event.Reporter) *UploadManager {
	um := NewUploadManager(store, client, reporter)
	go um.StartUploadWorkers(activeTasks)
	return um
}
