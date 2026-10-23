package client

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

func (sc *SyncCounter) Delete(taskID int32) {
	sc.m.Delete(taskID)
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
	Size       int64 // local size when the output directory was listed
}

// outputManifest records what a task left in its output directory and how
// much of it reached the upload targets.
type outputManifest struct {
	files         int32
	bytes         int64
	targets       int32
	uploadedPairs atomic.Int32 // verified (file, target) uploads
	failed        atomic.Bool  // the task was handed to the failure path
}

type UploadManager struct {
	UploadQueue    chan *UploadFile
	Completion     chan *pb.Task
	FailedQueue    chan *UploadFailure
	Store          string
	Client         pb.TaskQueueClient
	reporter       *event.Reporter
	pendingUploads SyncCounter // taskID → remaining files
	manifests      sync.Map    // taskID → *outputManifest
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
	type outputFile struct {
		path string
		size int64
	}
	var files []outputFile
	err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, outputFile{path: path, size: info.Size()})
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			// The worker creates the output directory when it accepts the
			// task, so a missing one means the task directory was lost while
			// the task ran: whatever the program wrote is gone. That is a
			// worker-side fault, never a program outcome.
			log.Printf("⚠️ Output directory does not exist for task %d", task.TaskId)
			if task.Status == "S" {
				um.reporter.Event("E", "upload", "output directory missing at upload; task failed", map[string]any{
					"task_id":    task.TaskId,
					"output_dir": outputDir,
				})
				task.Status = "F"
				fc := "other"
				task.FailureClass = &fc
			}
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

	m := &outputManifest{files: int32(len(files)), targets: int32(len(uploadTargets))}
	for _, f := range files {
		m.bytes += f.size
	}
	um.manifests.Store(task.TaskId, m)
	if len(files) == 0 {
		log.Printf("ℹ️ Task %d left no files in its output directory", task.TaskId)
		um.Completion <- task
		return
	}
	// Set counters/timers BEFORE enqueuing to avoid races. With publish_mode=copy
	// each file is enqueued to *both* targets, so the pending counter is
	// scaled by the number of targets — that's what watchCompletions decrements
	// once per upload.
	um.uploadStart.Store(task.TaskId, time.Now())
	um.pendingUploads.Set(task.TaskId, len(files)*len(uploadTargets))

	for _, f := range files {
		relPath, err := filepath.Rel(outputDir, f.path)
		if err != nil {
			um.failTask(task, fmt.Sprintf("cannot compute relative path of %s: %v", f.path, err))
			return
		}
		for _, ut := range uploadTargets {
			target := fetch.Join(*ut, relPath)
			um.UploadQueue <- &UploadFile{Task: task, SourcePath: f.path, TargetPath: target, Size: f.size}
		}
	}
}

// uploadFile copies one file to one target, then checks the target holds an
// object of the same size. A copy error or a size mismatch counts as a failed
// attempt; after uploadRetry failed attempts the whole task fails.
func (um *UploadManager) uploadFile(file *UploadFile) error {
	var err error
	for attempt := 1; attempt <= uploadRetry; attempt++ {
		m := um.manifest(file.Task.TaskId)
		if m == nil || m.failed.Load() {
			// The task already ended (another file failed): stop here.
			return nil
		}
		err = fetch.Copy(um.RcloneRemotes, file.SourcePath, file.TargetPath)
		if err == nil {
			err = um.verifyUpload(file)
		}
		if err == nil {
			log.Printf("✅ Uploaded: %s → %s", file.SourcePath, file.TargetPath)
			m.uploadedPairs.Add(1)
			um.Completion <- file.Task
			return nil
		}
		log.Printf("⚠️ Upload attempt %d failed for %s: %v", attempt, file.SourcePath, err)
		if attempt < uploadRetry {
			time.Sleep(uploadInterval)
		}
	}
	message := fmt.Sprintf("Failed to upload %s after %d attempts: %v", file.SourcePath, uploadRetry, err)
	log.Printf("❌ %s", message)
	um.reporter.Event("E", "upload", message, map[string]any{
		"source":   file.SourcePath,
		"target":   file.TargetPath,
		"task_id":  file.Task.TaskId,
		"attempts": uploadRetry,
		"error":    err.Error(),
	})
	um.failTask(file.Task, message)
	return err
}

// verifyUpload checks the destination holds an object of the uploaded size.
// A negative size means the backend does not report sizes; existence is then
// the check.
func (um *UploadManager) verifyUpload(file *UploadFile) error {
	info, err := fetch.Info(um.RcloneRemotes, file.TargetPath)
	if err != nil {
		return fmt.Errorf("uploaded object not found at %s: %w", file.TargetPath, err)
	}
	if got := info.Size(); got >= 0 && got != file.Size {
		return fmt.Errorf("uploaded object %s has size %d, expected %d", file.TargetPath, got, file.Size)
	}
	return nil
}

// failTask hands the task to the centralized failure path, once per task
// whatever the number of failing files.
func (um *UploadManager) failTask(task *pb.Task, message string) {
	m := um.manifest(task.TaskId)
	if m == nil || !m.failed.CompareAndSwap(false, true) {
		return // the task already ended, or its failure is already reported
	}
	select {
	case um.FailedQueue <- &UploadFailure{Task: task, Message: message}:
	default:
		log.Printf("⚠️ FailedQueue full; could not enqueue upload failure for task %d", task.TaskId)
	}
}

func (um *UploadManager) manifest(taskID int32) *outputManifest {
	if v, ok := um.manifests.Load(taskID); ok {
		return v.(*outputManifest)
	}
	return nil
}

// FinishTask releases the uploader's state for a task and returns the upload
// duration and the terminal metrics to send with its final status.
func (um *UploadManager) FinishTask(task *pb.Task) (int32, event.Terminal) {
	taskID := task.TaskId
	var secs int32
	if v, ok := um.uploadStart.LoadAndDelete(taskID); ok {
		secs = int32(time.Since(v.(time.Time)).Seconds())
	}
	um.pendingUploads.Delete(taskID)
	t := event.Terminal{}
	if task.PeakMemMb != nil {
		t.PeakMemMB = *task.PeakMemMb
	}
	if v, ok := um.manifests.LoadAndDelete(taskID); ok {
		m := v.(*outputManifest)
		files, bytes := m.files, m.bytes
		var uploaded int32
		if m.targets > 0 {
			uploaded = m.uploadedPairs.Load() / m.targets
		}
		t.ProducedFiles, t.ProducedBytes, t.UploadedFiles = &files, &bytes, &uploaded
	}
	return secs, t
}

func (um *UploadManager) watchCompletions(activeTasks *sync.Map) {
	for task := range um.Completion {
		taskID := task.TaskId
		if remaining, done := um.pendingUploads.Decrement(taskID); done {
			log.Printf("📤 Upload completed for task %d, marking as %s", taskID, task.Status)
			secs, terminal := um.FinishTask(task)
			// Send the terminal status through the reporter's per-task
			// serialized queue (NOT a direct/out-of-band send): updates for a
			// task must stay ordered behind any earlier R/V update, otherwise
			// the terminal status can land first and a late-arriving 'V' pins
			// the task as active on the server.
			// Carry the failure_class set earlier (by executeTask's
			// classifyExecFailure, or by the uploader itself on its own
			// upload failures) through the terminal F status. Empty on
			// success / non-failure transitions.
			fc := ""
			if task.Status == "F" && task.FailureClass != nil {
				fc = *task.FailureClass
			}
			um.reporter.UpdateTaskAsyncTerminal(taskID, task.Status, "", &secs, fc, terminal)
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
