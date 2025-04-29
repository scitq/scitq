package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gmtsciencedev/scitq2/fetch"
	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

const (
	maxUploads     = 20
	uploadRetry    = 3
	uploadInterval = 10 * time.Second
)

type SyncCounter struct {
	m sync.Map // map[uint32]int
}

func (sc *SyncCounter) Set(taskID uint32, count int) {
	sc.m.Store(taskID, count)
}

func (sc *SyncCounter) Decrement(taskID uint32) (int, bool) {
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
	pendingUploads SyncCounter // taskID → remaining files
}

func NewUploadManager(store string, client pb.TaskQueueClient) *UploadManager {
	return &UploadManager{
		UploadQueue:    make(chan *UploadFile, maxUploads),
		Completion:     make(chan *pb.Task, maxQueueSize),
		Store:          store,
		Client:         client,
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
	outputDir := filepath.Join(um.Store, "tasks", fmt.Sprint(task.TaskId), "output")
	count := 0
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("❌ Error walking path %s: %v", path, err)
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(outputDir, path)
		if err != nil {
			log.Printf("❌ Failed to get relative path for %s: %v", path, err)
			return err
		}
		target := fetch.Join(*task.Output, relPath)
		um.UploadQueue <- &UploadFile{Task: task, SourcePath: path, TargetPath: target}
		count++
		return nil
	})
	if err != nil {
		message := fmt.Sprintf("❌ Failed walking output directory: %v", err)
		log.Print(message)
		logMessage(message, um.Client, task.TaskId)
		task.Status = "F"
		um.Completion <- task
		return
	}
	if count == 0 {
		log.Printf("⚠️ No files to upload for task %d", task.TaskId)
		um.Completion <- task
		return
	}
	um.pendingUploads.Set(task.TaskId, count)
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
	log.Printf("❌ Failed to upload %s after %d attempts", file.SourcePath, uploadRetry)
	logMessage(message, um.Client, file.Task.TaskId)
	file.Task.Status = "F"
	um.Completion <- file.Task
	return err
}

func retryUpdateTaskStatus(client pb.TaskQueueClient, taskID uint32, status string) {
	retries := 0
	maxRetries := 1000
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    taskID,
			NewStatus: status,
		})
		if err == nil {
			log.Printf("✅ Task %d updated to status: %s", taskID, status)
			return
		}

		retries++
		log.Printf("⚠️ Failed to update task %d to %s (attempt %d): %v", taskID, status, retries, err)

		if retries >= maxRetries {
			log.Printf("❌ Gave up updating task %d after %d retries.", taskID, retries)
			return
		}

		time.Sleep(5 * time.Second)
	}
}

func (um *UploadManager) watchCompletions(activeTasks *sync.Map) {
	for task := range um.Completion {
		taskID := task.TaskId
		if remaining, done := um.pendingUploads.Decrement(taskID); done {
			log.Printf("📤 Upload completed for task %d, marking as %s", taskID, task.Status)
			retryUpdateTaskStatus(um.Client, taskID, task.Status)
			activeTasks.Delete(taskID)
		} else {
			log.Printf("📤 Remaining uploads for task %d: %d", taskID, remaining)
		}
	}
}

// RunUploader starts upload workers and returns the manager.
func RunUploader(store string, client pb.TaskQueueClient, activeTasks *sync.Map) *UploadManager {
	um := NewUploadManager(store, client)
	go um.StartUploadWorkers(activeTasks)
	return um
}
