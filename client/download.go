package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/scitq/scitq/client/event"
	"github.com/scitq/scitq/fetch"

	"github.com/google/uuid"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

const (
	maxDownloads = 20
	retryLimit   = 3
	retryWait    = 10 * time.Second
	resourceFile = "resources.json"
	maxQueueSize = 200
)

// Timeouts for `docker pull` attempts (exponential backoff with cap).
const (
	dockerPullTimeoutBase = 2 * time.Minute
	dockerPullTimeoutMax  = 10 * time.Minute
)

// FileTransfer represents a download task.
type FileType int

const (
	InputFile FileType = iota
	ResourceFile
	DockerImage
)

type FileTransfer struct {
	TaskId     uint32
	Task       *pb.Task
	FileType   FileType
	SourcePath string
	TargetPath string
}

// ResourceMetadata stores metadata for downloaded resources.
type FileMetadata struct {
	TaskId     uint32
	Task       *pb.Task
	SourcePath string
	FilePath   string
	FileType   FileType
	Size       int64
	Date       time.Time
	MD5        string
	Success    bool // true if download failed
}

// DownloadManager manages task downloads.
type DownloadManager struct {
	TaskDownloads     map[uint32]int          // Task ID → Remaining file count
	ResourceDownloads map[string][]*pb.Task   // SourcePath → Tasks waiting
	ResourceMemory    map[string]FileMetadata // SourcePath → Metadata

	FileQueue       chan *FileTransfer // Limited queue (maxDownloads=20)
	TaskQueue       chan *pb.Task      // Unlimited queue (tasks waiting for download)
	CompletionQueue chan *FileMetadata // Completed downloads
	ExecQueue       chan *pb.Task      // Tasks ready for execution
	FailedQueue     chan *pb.Task      // Tasks that failed during downloads
	Store           string
	reporter        *event.Reporter
	// Tracks tasks currently being scheduled for downloads to ensure idempotency at the boundary.
	EnqueuedTasks map[uint32]bool
}

// NewDownloadManager initializes the download manager.
func NewDownloadManager(store string, reporter *event.Reporter) *DownloadManager {
	return &DownloadManager{
		TaskDownloads:     make(map[uint32]int),
		ResourceDownloads: make(map[string][]*pb.Task),
		ResourceMemory:    make(map[string]FileMetadata),
		FileQueue:         make(chan *FileTransfer, maxDownloads),
		TaskQueue:         make(chan *pb.Task, maxQueueSize),
		CompletionQueue:   make(chan *FileMetadata, maxQueueSize),
		ExecQueue:         make(chan *pb.Task, maxQueueSize),
		FailedQueue:       make(chan *pb.Task, maxQueueSize),
		Store:             store,
		reporter:          reporter,
		EnqueuedTasks:     make(map[uint32]bool),
	}
}

// loadResourceMemory loads the resource memory from a file.
func (dm *DownloadManager) loadResourceMemory() {
	resourcePath := fmt.Sprintf("%s/%s", dm.Store, resourceFile)
	if fileInfo, err := os.Stat(resourcePath); err == nil && !fileInfo.IsDir() {
		file, err := os.Open(resourcePath)
		if err == nil {
			defer file.Close()
			decoder := json.NewDecoder(file)
			decoder.Decode(&dm.ResourceMemory)
			log.Println("🔄 Loaded resource memory from disk")
		}
	}
}

// saveResourceMemory persists the resource memory to a file.
func (dm *DownloadManager) saveResourceMemory() {
	resourcePath := fmt.Sprintf("%s/%s", dm.Store, resourceFile)
	file, err := os.Create(resourcePath)
	if err == nil {
		encoder := json.NewEncoder(file)
		encoder.Encode(dm.ResourceMemory)
		file.Close()
		log.Println("💾 Resource memory saved to disk")
	}
}

// StartDownloadWorkers launches download workers.
func (dm *DownloadManager) StartDownloadWorkers() {
	for i := 0; i < maxDownloads; i++ {
		go func() {
			for file := range dm.FileQueue {
				dm.downloadFile(file)
			}
		}()
	}
}

// pullDockerImage pulls a docker image using the `docker pull` command with a timeout.
func pullDockerImage(image string, timeout time.Duration) error {
	image = strings.TrimPrefix(image, "docker:")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	output, err := cmd.CombinedOutput()

	// If the context timed out, CommandContext will have killed the process.
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("docker pull timed out after %s for image %s\nOutput:\n%s", timeout, image, output)
	}

	if err != nil {
		return fmt.Errorf("failed to pull docker image %s: %v\nOutput:\n%s", image, err, output)
	}

	fmt.Printf("✅ Successfully pulled docker image %s\nOutput:\n%s", image, output)
	return nil
}

// downloadFile simulates a file download and notifies completion.
func (dm *DownloadManager) downloadFile(file *FileTransfer) {
	log.Printf("📥 Downloading: %s → %s", file.SourcePath, file.TargetPath)

	const maxRetries = 3
	const retryDelay = 5 * time.Second

	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		switch file.FileType {
		case ResourceFile, InputFile:
			err = fetch.Copy(fetch.DefaultRcloneConfig, file.SourcePath, file.TargetPath)

		case DockerImage:
			// Exponential backoff for docker pull timeout, capped at dockerPullTimeoutMax
			timeout := dockerPullTimeoutBase * time.Duration(1<<uint(attempt-1))
			if timeout > dockerPullTimeoutMax {
				timeout = dockerPullTimeoutMax
			}
			err = pullDockerImage(file.SourcePath, timeout)
		}

		if err == nil {
			// Success, exit retry loop
			break
		}

		log.Printf("❌ Attempt %d/%d failed for %s: %v", attempt, maxRetries, file.SourcePath, err)

		if attempt < maxRetries {
			log.Printf("🔄 Retrying in %v...", retryDelay)
			time.Sleep(retryDelay)
		}
	}

	success := err == nil
	if err != nil {
		message := fmt.Sprintf("Failed to download %s after %d attempts: %v", file.SourcePath, maxRetries, err)
		log.Printf("🚨 %s", message)

		dm.reporter.Event("E", "download", message, map[string]any{
			"source_path": file.SourcePath,
			"target_path": file.TargetPath,
			"error":       err.Error(),
			"file_type":   file.FileType,
			"task_id":     file.TaskId,
		})
		//dm.reporter.UpdateTask(file.TaskId, "F", message)
		//return
	}

	var size int64
	var md5Str string
	info, err := fetch.Info(fetch.DefaultRcloneConfig, file.SourcePath)
	if err != nil {
		log.Printf("Error fetching file info for %s: %v", file.SourcePath, err)
	} else {
		size = info.Size() // Correctly call the Size function
		md5Str = fetch.GetMD5(info)
	}

	metadata := FileMetadata{
		TaskId:     file.TaskId,
		Task:       file.Task,
		SourcePath: file.SourcePath,
		FilePath:   file.TargetPath,
		FileType:   file.FileType,
		Size:       size,
		Date:       time.Now(),
		MD5:        md5Str,
		Success:    success, // Download succeeded
	}

	dm.CompletionQueue <- &metadata
}

// ProcessDownloads manages incoming tasks and processes downloads.
func (dm *DownloadManager) ProcessDownloads() {
	for {
		select {
		case task := <-dm.TaskQueue:
			dm.handleNewTask(task)
		case completedFile := <-dm.CompletionQueue:
			dm.handleFileCompletion(completedFile)
		}
	}
}

// handleNewTask enqueues necessary downloads.
func (dm *DownloadManager) handleNewTask(task *pb.Task) {
	log.Printf("📝 Processing new task %d for downloads", task.TaskId)
	// Idempotency boundary: if we've already started scheduling this task, ignore duplicates.
	if dm.EnqueuedTasks[task.TaskId] {
		log.Printf("🔁 Task %d already scheduled for downloads — ignoring duplicate", task.TaskId)
		return
	}
	dm.EnqueuedTasks[task.TaskId] = true
	numFiles := 0

	// Queue input files
	for _, input := range task.Input {
		ft := &FileTransfer{
			TaskId:     task.TaskId,
			Task:       task,
			FileType:   InputFile,
			SourcePath: input,
			TargetPath: fmt.Sprintf("%s/tasks/%d/input/", dm.Store, task.TaskId),
		}
		go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		numFiles++
	}

	// Queue resources (if not already downloaded or outdated)
	for _, resource := range task.Resource {
		meta, exists := dm.ResourceMemory[resource]

		if exists {
			log.Printf("Checking resource %v", meta)
			fileInfo, err := fetch.Info(fetch.DefaultRcloneConfig, meta.SourcePath)
			if err != nil {
				// likely a file method that does not support Info, we have little choice but to trust
				continue
			}

			if func() bool { // ✅ Anonymous function to allow early exit
				if meta.MD5 != "" {
					if meta.MD5 == fetch.GetMD5(fileInfo) {
						return true
					} else {
						log.Printf("Resource %s seems to have changed (MD5 differs), redownloading", resource)
						delete(dm.ResourceMemory, resource)
						dm.saveResourceMemory()
						return false
					}
				}

				if !meta.Date.IsZero() {
					ctx := context.Background()
					if meta.Date != fileInfo.ModTime(ctx) {
						log.Printf("Resource %s seems to have changed (date differs), redownloading", resource)
						delete(dm.ResourceMemory, resource)
						dm.saveResourceMemory()
						return false
					}
				}

				if meta.Size != 0 {
					if meta.Size == fileInfo.Size() {
						return true
					} else {
						log.Printf("Resource %s seems to have changed (size differs), redownloading", resource)
						delete(dm.ResourceMemory, resource)
						dm.saveResourceMemory()
						return false
					}
				}

				return true
			}() {
				continue
			}
			// here either MD5 is unchanged or date and size are unchanged or no checking method is available

		}
		rd, exists := dm.ResourceDownloads[resource]
		numFiles++
		if exists {
			dm.ResourceDownloads[resource] = append(rd, task)
		} else {
			dm.ResourceDownloads[resource] = []*pb.Task{task}
			ft := &FileTransfer{
				TaskId:     task.TaskId,
				Task:       task,
				FileType:   ResourceFile,
				SourcePath: resource,
				TargetPath: fmt.Sprintf("%s/resources/%s/", dm.Store, uuid.New()),
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		}
	}

	container := "docker:" + task.Container
	_, exists := dm.ResourceMemory[container]
	if !exists {
		rd, exists := dm.ResourceDownloads[container]
		numFiles++
		if exists {
			dm.ResourceDownloads[container] = append(rd, task)
		} else {
			dm.ResourceDownloads[container] = []*pb.Task{task}
			ft := &FileTransfer{
				TaskId:     task.TaskId,
				Task:       task,
				FileType:   DockerImage,
				SourcePath: container,
				TargetPath: task.Container,
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		}
	}

	// Track total downloads needed
	dm.TaskDownloads[task.TaskId] = numFiles
	if numFiles == 0 {
		log.Printf("🚀 Task %d ready for execution", task.TaskId)
		delete(dm.EnqueuedTasks, task.TaskId)
		dm.ExecQueue <- task
	} else {
		log.Printf("📝 Task %d waiting for %d files", task.TaskId, numFiles)
	}
}

// handleFileCompletion updates the state when a download completes.
func (dm *DownloadManager) handleFileCompletion(fileMeta *FileMetadata) {
	if fileMeta == nil {
		log.Printf("ERROR: empty FileMetadata")
		return
	}
	fm := *fileMeta
	log.Printf("✅ Download completed: %s", fm.SourcePath)
	//dm.ResourceMemory[filePath] = dm.ResourceMemory[filePath]

	if !fm.Success {
		log.Printf("❌ Download failed for %s", fm.SourcePath)
		dm.reporter.UpdateTask(fm.TaskId, "F", fmt.Sprintf("Download failed for %s", fm.SourcePath))
		fm.Task.Status = "F"
	}

	// Check if it's a resource, notify all waiting tasks
	switch fm.FileType {
	case InputFile:
		{
			if count, ok := dm.TaskDownloads[fm.TaskId]; ok {
				dm.TaskDownloads[fm.TaskId] = count - 1
				if count <= 1 {
					// no more input/resource files to wait we can queue task for execution
					delete(dm.TaskDownloads, fm.TaskId)
					delete(dm.EnqueuedTasks, fm.TaskId)
					if fm.Task.Status != "F" {
						log.Printf("🚀 Task %d ready for execution", fm.TaskId)
						dm.ExecQueue <- fm.Task
					} else {
						log.Printf("❌ Task %d failed due to previous download errors", fm.TaskId)
					}
				} else {
					log.Printf("📝 Task %d still waiting for %d files", fm.TaskId, count-1)
				}
			}
			if !fm.Success || fm.Task.Status == "F" {
				// Inform client loop to clear local active flag
				select {
				case dm.FailedQueue <- fm.Task:
				default:
					log.Printf("⚠️ FailedQueue full; could not enqueue task %d", fm.TaskId)
				}
				delete(dm.EnqueuedTasks, fm.TaskId)
			}
		}
	case ResourceFile, DockerImage:
		{
			// On success, publish metadata before waking waiting tasks to avoid a race
			if fm.Success {
				dm.ResourceMemory[fm.SourcePath] = *fileMeta
				dm.saveResourceMemory()
			}

			if tasks, exists := dm.ResourceDownloads[fm.SourcePath]; exists {
				for _, task := range tasks {
					if count, ok := dm.TaskDownloads[task.TaskId]; ok {
						if !fm.Success {
							log.Printf("❌ Task %d failed due to download error for resource %s", task.TaskId, fm.SourcePath)
							dm.reporter.UpdateTask(task.TaskId, "F", fmt.Sprintf("Download failed for resource %s", fm.SourcePath))
							task.Status = "F"
							// Notify the client loop so it can clear activeTasks and avoid waiting on a never-executing task
							select {
							case dm.FailedQueue <- task:
							default:
								log.Printf("⚠️ FailedQueue full; could not enqueue task %d", task.TaskId)
							}
							delete(dm.EnqueuedTasks, task.TaskId)
						}

						// Decrement remaining count for this task
						dm.TaskDownloads[task.TaskId] = count - 1

						// If this was the last pending download for the task, and all succeeded, queue for execution
						if count <= 1 {
							delete(dm.TaskDownloads, task.TaskId)
							delete(dm.EnqueuedTasks, task.TaskId)
							if task.Status != "F" && fm.Success {
								log.Printf("🚀 Task %d ready for execution", task.TaskId)
								dm.ExecQueue <- task
							} else {
								log.Printf("❌ Task %d not ready for execution (status=%s, resource success=%v)", task.TaskId, task.Status, fm.Success)
							}
						} else {
							log.Printf("📝 Task %d still waiting for %d files", task.TaskId, count-1)
						}
					}
				}
				delete(dm.ResourceDownloads, fm.SourcePath)
			}
		}
	default:
		log.Printf("Unknown file type for %s", fm.SourcePath)
	}

}

// resourceLink creates hard links for resources in the task's resource folder.
func (dm *DownloadManager) resourceLink(resourcePath, taskResourceFolder string) error {
	fileMeta := dm.ResourceMemory[resourcePath]

	// Ensure destination root exists
	if err := os.MkdirAll(taskResourceFolder, 0o755); err != nil {
		message := fmt.Sprintf("Error creating destination directory %s: %v", taskResourceFolder, err)
		log.Printf("❌ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"resource_path": fileMeta.FilePath,
			"task_id":       fileMeta.TaskId,
			"error":         err.Error(),
		})
		if fileMeta.Task != nil {
			fileMeta.Task.Status = "F"
			dm.reporter.UpdateTask(fileMeta.TaskId, "F", message)
		}
		return fmt.Errorf("error creating destination directory %s: %w", taskResourceFolder, err)
	}

	// Recursive function to mirror directory structure and hard-link files
	var linkTree func(src, dst string) error
	linkTree = func(src, dst string) error {
		entries, err := os.ReadDir(src)
		if err != nil {
			return fmt.Errorf("read dir %s: %w", src, err)
		}
		for _, entry := range entries {
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())

			// Directories: create and recurse
			if entry.IsDir() {
				if err := os.MkdirAll(dstPath, 0o755); err != nil {
					return fmt.Errorf("mkdir %s: %w", dstPath, err)
				}
				if err := linkTree(srcPath, dstPath); err != nil {
					return err
				}
				continue
			}

			// Non-directories: attempt hard link
			// Note: we ignore symlinks specially; if present, we link the symlink inode itself
			if err := os.Link(srcPath, dstPath); err != nil {
				return fmt.Errorf("link %s -> %s: %w", srcPath, dstPath, err)
			}
		}
		return nil
	}

	if err := linkTree(fileMeta.FilePath, taskResourceFolder); err != nil {
		message := fmt.Sprintf("Error linking resource tree %s -> %s: %v", fileMeta.FilePath, taskResourceFolder, err)
		log.Printf("❌ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"resource_path": fileMeta.FilePath,
			"task_id":       fileMeta.TaskId,
			"error":         err.Error(),
		})
		if fileMeta.Task != nil {
			fileMeta.Task.Status = "F"
			dm.reporter.UpdateTask(fileMeta.TaskId, "F", message)
		}
		return fmt.Errorf("error linking resource tree %s -> %s: %w", fileMeta.FilePath, taskResourceFolder, err)
	}

	log.Printf("Linked resource tree %s -> %s", resourcePath, taskResourceFolder)
	return nil
}

// extractFilename extracts filename from a path.
func extractFilename(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// run downloader
func RunDownloader(store string, reporter *event.Reporter) *DownloadManager {
	dm := NewDownloadManager(store, reporter)
	go func() { dm.StartDownloadWorkers() }()
	go func() {
		dm.loadResourceMemory()
		dm.ProcessDownloads()
	}()
	return dm
}
