package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gmtsciencedev/scitq2/fetch"
	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

const (
	maxUploads     = 20
	uploadRetry    = 3
	uploadInterval = 10 * time.Second
)

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
	pendingUploads map[uint32]int // taskID → remaining files
}

func NewUploadManager(store string, client pb.TaskQueueClient) *UploadManager {
	return &UploadManager{
		UploadQueue:    make(chan *UploadFile, maxUploads),
		Completion:     make(chan *pb.Task, maxQueueSize),
		Store:          store,
		Client:         client,
		pendingUploads: make(map[uint32]int),
	}
}

func (um *UploadManager) StartUploadWorkers() {
	for i := 0; i < maxUploads; i++ {
		go func() {
			for file := range um.UploadQueue {
				um.uploadFile(file)
			}
		}()
	}

	go um.watchCompletions()
}

func (um *UploadManager) EnqueueTaskOutput(task *pb.Task) {
	outputDir := filepath.Join(um.Store, "tasks", fmt.Sprint(*task.TaskId), "output")
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
		log.Printf("❌ Failed walking output directory: %v", err)
		task.Status = "F"
		um.Completion <- task
		//um.Client.SendTaskLogs(context.Background(), &pb.TaskLog{
		//	TaskId:    task.TaskId,
		//	Log:       err.Error(),
		//	Timestamp: time.Now().Unix(),
		//})
		return
	}
	if count == 0 {
		log.Printf("⚠️ No files to upload for task %d", *task.TaskId)
		um.Completion <- task
		return
	}
	um.pendingUploads[*task.TaskId] = count
}

func (um *UploadManager) uploadFile(file *UploadFile) {
	var err error
	success := false

	for attempt := 1; attempt <= uploadRetry; attempt++ {
		err = fetch.Copy(fetch.DefaultRcloneConfig, file.SourcePath, file.TargetPath)
		if err == nil {
			log.Printf("✅ Uploaded: %s → %s", file.SourcePath, file.TargetPath)
			success = true
			break
		}
		log.Printf("⚠️ Upload attempt %d failed for %s: %v", attempt, file.SourcePath, err)
		time.Sleep(uploadInterval)
	}

	if !success {
		log.Printf("❌ Failed to upload %s after %d attempts", file.SourcePath, uploadRetry)
		file.Task.Status = "F"
	}

	// decrement counter
	taskID := *file.Task.TaskId
	if count, ok := um.pendingUploads[taskID]; ok {
		count--
		if count == 0 {
			delete(um.pendingUploads, taskID)
			um.Completion <- file.Task
		} else {
			um.pendingUploads[taskID] = count
		}
	}
}

func (um *UploadManager) watchCompletions() {
	for task := range um.Completion {
		log.Printf("📤 Upload completed for task %d, marking as %s", *task.TaskId, task.Status)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := um.Client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
			TaskId:    *task.TaskId,
			NewStatus: task.Status,
		})
		if err != nil {
			log.Printf("❌ Failed to update status for task %d: %v", *task.TaskId, err)
		} else {
			log.Printf("✅ Status updated for task %d to %s", *task.TaskId, task.Status)
		}
		cancel()
	}
}

// RunUploader starts upload workers and returns the manager.
func RunUploader(store string, client pb.TaskQueueClient) *UploadManager {
	um := NewUploadManager(store, client)
	go um.StartUploadWorkers()
	return um
}
