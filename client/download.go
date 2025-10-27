package client

import (
	"context"
	"encoding/json"
	"errors"
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
	TaskId     int32
	Task       *pb.Task
	FileType   FileType
	SourcePath string
	TargetPath string
}

// DownloadFailure carries a unified failure notification from the downloader
// to a single status update point in the client loop.
type DownloadFailure struct {
	Task    *pb.Task
	Message string
}

// ResourceMetadata stores metadata for downloaded resources.
type FileMetadata struct {
	TaskId     int32
	Task       *pb.Task
	SourcePath string
	FilePath   string
	FileType   FileType
	Size       int64
	Date       time.Time
	MD5        string
	Success    bool // true if download succeeded
}

// DownloadManager manages task downloads.
type DownloadManager struct {
	ResourceDownloads map[string][]*pb.Task   // SourcePath → Tasks waiting
	ResourceMemory    map[string]FileMetadata // SourcePath → Metadata

	FileQueue       chan *FileTransfer    // Limited queue (maxDownloads=20)
	TaskQueue       chan *pb.Task         // Unlimited queue (tasks waiting for download)
	CompletionQueue chan *FileMetadata    // Completed downloads
	ExecQueue       chan *pb.Task         // Tasks ready for execution
	FailedQueue     chan *DownloadFailure // Tasks that failed during downloads (centralized reporting)
	Store           string
	reporter        *event.Reporter
	// Tracks tasks currently being scheduled for downloads to ensure idempotency at the boundary.
	EnqueuedTasks map[int32]bool
	// Per-task chrono map for download durations
	DownloadStart map[int32]time.Time
	// Per-task pending set for download items
	TaskPending   map[int32]map[string]bool // Task ID → set of pending item keys (I:input, R:resource, D:docker)
	RcloneRemotes *pb.RcloneRemotes
}

// NewDownloadManager initializes the download manager.
func NewDownloadManager(store string, reporter *event.Reporter, rcloneRemotes *pb.RcloneRemotes) *DownloadManager {
	return &DownloadManager{
		ResourceDownloads: make(map[string][]*pb.Task),
		ResourceMemory:    make(map[string]FileMetadata),
		FileQueue:         make(chan *FileTransfer, maxDownloads),
		TaskQueue:         make(chan *pb.Task, maxQueueSize),
		CompletionQueue:   make(chan *FileMetadata, maxQueueSize),
		ExecQueue:         make(chan *pb.Task, maxQueueSize),
		FailedQueue:       make(chan *DownloadFailure, maxQueueSize),
		Store:             store,
		reporter:          reporter,
		EnqueuedTasks:     make(map[int32]bool),
		DownloadStart:     make(map[int32]time.Time),
		TaskPending:       make(map[int32]map[string]bool),
		RcloneRemotes:     rcloneRemotes,
	}
}

// addWaitingTask appends task to dm.ResourceDownloads[key] only if not already present.
// Returns true if the task was added (i.e., this task should increment its pending count by 1).
func (dm *DownloadManager) addWaitingTask(key string, task *pb.Task) bool {
	list, exists := dm.ResourceDownloads[key]
	if !exists {
		log.Printf("Scheduling new download for resource %s for task %d", key, task.TaskId)
		dm.ResourceDownloads[key] = []*pb.Task{task}
		return true
	}
	for _, t := range list {
		if t.TaskId == task.TaskId {
			log.Printf("Duplicate of resource in %s for task %d", key, task.TaskId)
			// Already waiting for this resource; don't add a duplicate.
			return false
		}
	}
	log.Printf("Adding task %d to wait list for resource %s launched by task %d", task.TaskId, key, dm.ResourceDownloads[key][0].TaskId)
	dm.ResourceDownloads[key] = append(list, task)
	return true
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
			err = fetch.Copy(dm.RcloneRemotes, file.SourcePath, file.TargetPath)

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
	var srcModTime time.Time
	info, err := fetch.Info(dm.RcloneRemotes, file.SourcePath)
	if err != nil {
		log.Printf("Error fetching file info for %s: %v", file.SourcePath, err)
	} else {
		size = info.Size()
		md5Str = fetch.GetMD5(info)
		// Record the SOURCE modtime, not now(), so change detection is stable
		srcModTime = info.ModTime(context.Background())
	}

	metadata := FileMetadata{
		TaskId:     file.TaskId,
		Task:       file.Task,
		SourcePath: file.SourcePath,
		FilePath:   file.TargetPath,
		FileType:   file.FileType,
		Size:       size,
		Date:       srcModTime, // use source's ModTime for proper change detection
		MD5:        md5Str,
		Success:    success,
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
	// Start chrono for download duration
	if _, seen := dm.DownloadStart[task.TaskId]; !seen {
		dm.DownloadStart[task.TaskId] = time.Now()
	}
	// Use TaskPending set for tracking
	if _, ok := dm.TaskPending[task.TaskId]; !ok {
		dm.TaskPending[task.TaskId] = make(map[string]bool)
	}
	pending := dm.TaskPending[task.TaskId]

	// Queue input files
	for _, input := range task.Input {
		key := "I:" + input
		if pending[key] {
			log.Printf("🔁 Duplicate input ignored for task %d: %s", task.TaskId, input)
		} else {
			pending[key] = true
			ft := &FileTransfer{
				TaskId:     task.TaskId,
				Task:       task,
				FileType:   InputFile,
				SourcePath: input,
				TargetPath: fmt.Sprintf("%s/tasks/%d/input/", dm.Store, task.TaskId),
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		}
	}

	// Queue resources (if not already downloaded or outdated)
	for _, resource := range task.Resource {
		log.Printf("🔎 Checking ResourceMemory for key=%q", resource)
		meta, exists := dm.ResourceMemory[resource]

		if exists {
			log.Printf("Checking resource %v", meta)
			fileInfo, err := fetch.Info(dm.RcloneRemotes, meta.SourcePath)
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
						dm.reporter.Event("I", "resource_change", fmt.Sprintf("Resource %s MD5 changed, redownloading", resource), map[string]any{
							"resource_path": resource,
							"task_id":       task.TaskId,
						})
						delete(dm.ResourceMemory, resource)
						dm.saveResourceMemory()
						return false
					}
				}

				if !meta.Date.IsZero() {
					ctx := context.Background()
					srcTime := fileInfo.ModTime(ctx)
					// Normalize both sides to second precision & UTC to be safe
					memoryDate := meta.Date.UTC().Truncate(time.Second)
					srcDate := srcTime.UTC().Truncate(time.Second)
					if !memoryDate.Equal(srcDate) {
						log.Printf("Resource %s seems to have changed (date differs), redownloading", resource)
						dm.reporter.Event("I", "resource_change", fmt.Sprintf("Resource %s date changed, redownloading", resource), map[string]any{
							"resource_path": resource,
							"task_id":       task.TaskId,
						})
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
						dm.reporter.Event("I", "resource_change", fmt.Sprintf("Resource %s size changed, redownloading", resource), map[string]any{
							"resource_path": resource,
							"task_id":       task.TaskId,
						})
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
		added := dm.addWaitingTask(resource, task)
		rkey := "R:" + resource
		if added {
			if pending[rkey] {
				log.Printf("🔁 Duplicate resource association ignored for task %d: %s", task.TaskId, resource)
			} else {
				pending[rkey] = true
			}
		}
		if list, ok := dm.ResourceDownloads[resource]; ok {
			log.Printf("📥 waitlist key=%q added=%v waiters=%d first=%v", resource, added, len(list), len(list) == 1)
		} else {
			log.Printf("📥 waitlist key=%q not present right after add (added=%v)", resource, added)
		}
		if added && len(dm.ResourceDownloads[resource]) == 1 {
			log.Printf("📦 ENQ resource download key=%q by task=%d", resource, task.TaskId)
			ft := &FileTransfer{
				TaskId:     task.TaskId,
				Task:       task,
				FileType:   ResourceFile,
				SourcePath: resource,
				TargetPath: fmt.Sprintf("%s/resources/%s/", dm.Store, uuid.New()),
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		} else if added {
			log.Printf("⏳ Not first waiter for key=%q; another task already enqueued the download", resource)
		}
	}

	container := "docker:" + task.Container
	if _, present := dm.ResourceMemory[container]; !present {
		added := dm.addWaitingTask(container, task)
		dkey := "D:" + container
		if added {
			if pending[dkey] {
				log.Printf("🔁 Duplicate docker association ignored for task %d: %s", task.TaskId, container)
			} else {
				pending[dkey] = true
			}
		}
		if list, ok := dm.ResourceDownloads[container]; ok {
			log.Printf("📥 waitlist key=%q added=%v waiters=%d first=%v", container, added, len(list), len(list) == 1)
		} else {
			log.Printf("📥 waitlist key=%q not present right after add (added=%v)", container, added)
		}
		if added && len(dm.ResourceDownloads[container]) == 1 {
			log.Printf("📦 ENQ docker pull key=%q by task=%d", container, task.TaskId)
			ft := &FileTransfer{
				TaskId:     task.TaskId,
				Task:       task,
				FileType:   DockerImage,
				SourcePath: container,
				TargetPath: task.Container,
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		} else if added {
			log.Printf("⏳ Not first waiter for docker key=%q; another task already enqueued the pull", container)
		}
	}

	if len(pending) == 0 {
		log.Printf("🚀 Task %d ready for execution (no downloads needed)", task.TaskId)
		delete(dm.EnqueuedTasks, task.TaskId)
		dm.ExecQueue <- task
	} else {
		log.Printf("📝 Task %d waiting for %d item(s)", task.TaskId, len(pending))
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

	switch fm.FileType {
	case InputFile:
		{
			key := "I:" + fm.SourcePath
			if set, ok := dm.TaskPending[fm.TaskId]; ok {
				if set[key] {
					delete(set, key)
				} else {
					log.Printf("⚠️ Completion for non-pending input key=%q task=%d", key, fm.TaskId)
					dm.reporter.Event("E", "download_completion", "completion for non-pending key", map[string]any{
						"key":     key,
						"task_id": fm.TaskId,
					})
				}
				if !fm.Success {
					log.Printf("❌ Download failed for %s", fm.SourcePath)
					fm.Task.Status = "F"
					select {
					case dm.FailedQueue <- &DownloadFailure{Task: fm.Task, Message: fmt.Sprintf("Download failed for %s", fm.SourcePath)}:
					default:
						log.Printf("⚠️ FailedQueue full; could not enqueue failure for task %d", fm.TaskId)
					}
				}
				if len(set) == 0 {
					delete(dm.TaskPending, fm.TaskId)
					delete(dm.EnqueuedTasks, fm.TaskId)
					if fm.Task.Status != "F" {
						if start, ok := dm.DownloadStart[fm.TaskId]; ok {
							secs := int32(time.Since(start).Seconds())
							dm.reporter.UpdateTaskAsync(fm.TaskId, "O", "", &secs)
							delete(dm.DownloadStart, fm.TaskId)
						}
					} else {
						// if failed, do not send O and ensure chrono is cleared
						delete(dm.DownloadStart, fm.TaskId)
					}
					if fm.Task.Status != "F" && fm.Success {
						log.Printf("🚀 Task %d ready for execution", fm.TaskId)
						dm.ExecQueue <- fm.Task
					} else if fm.Task.Status == "F" {
						log.Printf("❌ Task %d failed due to previous download errors", fm.TaskId)
					} else {
						log.Printf("❌ Task %d not ready for execution (status=%s, success=%v)", fm.TaskId, fm.Task.Status, fm.Success)
					}
				} else {
					log.Printf("📝 Task %d still waiting for %d item(s)", fm.TaskId, len(set))
				}
			}
			if !fm.Success || fm.Task.Status == "F" {
				// Inform client loop to clear local active flag and centralize status update
				select {
				case dm.FailedQueue <- &DownloadFailure{Task: fm.Task, Message: fmt.Sprintf("Download failed for %s", fm.SourcePath)}:
				default:
					log.Printf("⚠️ FailedQueue full; could not enqueue failure for task %d", fm.TaskId)
				}
				delete(dm.EnqueuedTasks, fm.TaskId)
				delete(dm.DownloadStart, fm.TaskId)
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
				log.Printf("📣 Notifying %d waiter(s) for key=%q (success=%v)", len(tasks), fm.SourcePath, fm.Success)
				for _, task := range tasks {
					var keyPrefix string
					if fm.FileType == ResourceFile {
						keyPrefix = "R:"
					} else {
						keyPrefix = "D:"
					}
					tset := dm.TaskPending[task.TaskId]
					key := keyPrefix + fm.SourcePath
					if tset != nil {
						if tset[key] {
							delete(tset, key)
						} else {
							log.Printf("⚠️ Completion for non-pending %s key=%q task=%d", keyPrefix, key, task.TaskId)
							dm.reporter.Event("E", "download_completion", "completion for non-pending key", map[string]any{
								"key":     key,
								"task_id": task.TaskId,
							})
						}
					}
					if !fm.Success {
						log.Printf("❌ Task %d failed due to download error for resource %s", task.TaskId, fm.SourcePath)
						task.Status = "F"
						select {
						case dm.FailedQueue <- &DownloadFailure{Task: task, Message: fmt.Sprintf("Download failed for resource %s", fm.SourcePath)}:
						default:
							log.Printf("⚠️ FailedQueue full; could not enqueue failure for task %d", task.TaskId)
						}
						delete(dm.EnqueuedTasks, task.TaskId)
						delete(dm.DownloadStart, task.TaskId)
					}
					if tset != nil {
						if len(tset) == 0 {
							delete(dm.TaskPending, task.TaskId)
							delete(dm.EnqueuedTasks, task.TaskId)
							if task.Status != "F" {
								if start, ok := dm.DownloadStart[task.TaskId]; ok {
									secs := int32(time.Since(start).Seconds())
									dm.reporter.UpdateTaskAsync(task.TaskId, "O", "", &secs)
									delete(dm.DownloadStart, task.TaskId)
								}
							} else {
								delete(dm.DownloadStart, task.TaskId)
							}
							if task.Status != "F" && fm.Success {
								log.Printf("🚀 Task %d ready for execution", task.TaskId)
								dm.ExecQueue <- task
							} else if task.Status == "F" {
								log.Printf("❌ Task %d failed due to previous download errors", task.TaskId)
							} else {
								log.Printf("❌ Task %d not ready for execution (status=%s, resource success=%v)", task.TaskId, task.Status, fm.Success)
							}
						} else {
							log.Printf("📝 Task %d still waiting for %d item(s)", task.TaskId, len(tset))
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
	log.Printf("🔗 resourceLink arg=%q", resourcePath)
	fileMeta, ok := dm.ResourceMemory[resourcePath]
	log.Printf("🔍 ResourceMemory hit=%v for key=%q", ok, resourcePath)

	waitForResource := func(timeout time.Duration) (FileMetadata, bool) {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			fm, found := dm.ResourceMemory[resourcePath]
			if found {
				return fm, true
			}
			time.Sleep(500 * time.Millisecond)
		}
		return FileMetadata{}, false
	}

	if resourcePath == "" {
		message := "resourceLink called with empty key"
		log.Printf("⚠️ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"lookup_key": resourcePath,
		})
		return errors.New(message)
	}

	if !ok {
		log.Printf("⏱ resourceLink: key %q not in memory — assuming upgrade in progress; waiting...", resourcePath)
		if fm, ready := waitForResource(10 * time.Minute); ready {
			fileMeta = fm
			ok = true
			log.Printf("✅ resourceLink: key %q appeared after wait -> path=%q", resourcePath, fileMeta.FilePath)
			dm.reporter.Event("I", "resource_link", fmt.Sprintf("resource key appeared in memory after wait: %q", resourcePath), map[string]any{
				"lookup_key": resourcePath,
			})
		} else {
			message := fmt.Sprintf("resource key not found in memory after wait: %q", resourcePath)
			log.Printf("⚠️ %s", message)
			dm.reporter.Event("E", "resource_link", message, map[string]any{
				"lookup_key": resourcePath,
			})
			return errors.New(message)
		}
	}

	if fileMeta.FilePath == "" {
		message := fmt.Sprintf("resource metadata has empty FilePath for key %q", resourcePath)
		log.Printf("⚠️ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"lookup_key":    resourcePath,
			"resource_path": fileMeta.FilePath,
			"task_id":       fileMeta.TaskId,
		})
		return errors.New(message)
	}

	log.Printf("✅ resourceLink lookup hit for key=%q -> path=%q", resourcePath, fileMeta.FilePath)

	// Sanity check: ensure the resolved resource path exists
	if _, err := os.Stat(fileMeta.FilePath); err != nil {
		message := fmt.Sprintf("resource path missing for key %q: %s", resourcePath, err)
		log.Printf("❌ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"lookup_key":    resourcePath,
			"resource_path": fileMeta.FilePath,
			"task_id":       fileMeta.TaskId,
			"error":         err.Error(),
		})
		return errors.New(message)
	}

	// Ensure destination root exists
	if err := os.MkdirAll(taskResourceFolder, 0o755); err != nil {
		message := fmt.Sprintf("Error creating destination directory %s: %v", taskResourceFolder, err)
		log.Printf("❌ %s", message)
		dm.reporter.Event("E", "resource_link", message, map[string]any{
			"lookup_key":    resourcePath,
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
				if err := os.MkdirAll(dstPath, 0o777); err != nil {
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
			"lookup_key":    resourcePath,
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
func RunDownloader(store string, reporter *event.Reporter, rcloneRemotes *pb.RcloneRemotes) *DownloadManager {
	dm := NewDownloadManager(store, reporter, rcloneRemotes)
	go func() { dm.StartDownloadWorkers() }()
	go func() {
		dm.loadResourceMemory()
		dm.ProcessDownloads()
	}()
	return dm
}
