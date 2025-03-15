package client

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/google/uuid"
)

const (
	maxDownloads = 20
	retryLimit   = 3
	retryWait    = 10 * time.Second
	resourceFile = "/scratch/resources.json"
	maxQueueSize = 200
)

// FileTransfer represents a download task.
type FileType int

const (
	InputFile FileType = iota
	ResourceFile
	DockerImage
)

type FileTransfer struct {
	TaskID     uint32
	FileType   FileType
	SourcePath string
	TargetPath string
}

// ResourceMetadata stores metadata for downloaded resources.
type FileMetadata struct {
	SourcePath string
	FilePath   string
	FileType   FileType
	Size       int64
	Date       time.Time
	MD5        string
}

// DownloadManager manages task downloads.
type DownloadManager struct {
	TaskDownloads     map[uint32]int          // Task ID → Remaining file count
	ResourceDownloads map[string][]uint32     // SourcePath → Tasks waiting
	ResourceMemory    map[string]FileMetadata // SourcePath → Metadata

	FileQueue       chan *FileTransfer // Limited queue (maxDownloads=20)
	TaskQueue       chan *pb.Task      // Unlimited queue (tasks waiting for download)
	CompletionQueue chan *FileMetadata // Completed downloads
}

// NewDownloadManager initializes the download manager.
func NewDownloadManager() *DownloadManager {
	return &DownloadManager{
		TaskDownloads:     make(map[uint32]int),
		ResourceDownloads: make(map[string][]uint32),
		ResourceMemory:    make(map[string]FileMetadata),
		FileQueue:         make(chan *FileTransfer, maxDownloads),
		TaskQueue:         make(chan *pb.Task, maxQueueSize),
		CompletionQueue:   make(chan *FileMetadata, maxQueueSize),
	}
}

// loadResourceMemory loads the resource memory from a file.
func (dm *DownloadManager) loadResourceMemory() {
	file, err := os.Open(resourceFile)
	if err == nil {
		defer file.Close()
		decoder := json.NewDecoder(file)
		decoder.Decode(&dm.ResourceMemory)
		log.Println("🔄 Loaded resource memory from disk")
	}
}

// saveResourceMemory persists the resource memory to a file.
func (dm *DownloadManager) saveResourceMemory() {
	file, err := os.Create(resourceFile)
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

// downloadFile simulates a file download and notifies completion.
func (dm *DownloadManager) downloadFile(file *FileTransfer) {
	log.Printf("📥 Downloading: %s → %s", file.SourcePath, file.TargetPath)
	time.Sleep(2 * time.Second) // Simulated download time

	// Simulate metadata extraction
	size := int64(1024) // Dummy size
	hash := md5.Sum([]byte(file.SourcePath))
	md5Str := hex.EncodeToString(hash[:])
	metadata := FileMetadata{SourcePath: file.SourcePath, FileType: file.FileType, Size: size, Date: time.Now(), MD5: md5Str}
	dm.CompletionQueue <- &metadata
	//dm.ResourceMemory[file.SourcePath] = metadata
}

// ProcessDownloads manages incoming tasks and processes downloads.
func (dm *DownloadManager) ProcessDownloads(execQueue chan *pb.Task) {
	for {
		select {
		case task := <-dm.TaskQueue:
			dm.handleNewTask(task)
		case completedFile := <-dm.CompletionQueue:
			dm.handleFileCompletion(completedFile, execQueue)
		}
	}
}

// handleNewTask enqueues necessary downloads.
func (dm *DownloadManager) handleNewTask(task *pb.Task) {
	log.Printf("📝 Processing new task %d for downloads", *task.TaskId)
	numFiles := 0

	// Queue input files
	for _, input := range task.Input {
		ft := &FileTransfer{
			TaskID:     *task.TaskId,
			FileType:   InputFile,
			SourcePath: input,
			TargetPath: fmt.Sprintf("/scratch/tasks/%d/input/", *task.TaskId),
		}
		go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		numFiles++
	}

	// Queue resources (if not already downloaded or outdated)
	for _, resource := range task.Resource {
		meta, exists := dm.ResourceMemory[resource]
		if exists && time.Since(meta.Date) < 24*time.Hour {
			//numFiles++
			//TODO change this test
			continue // Assume it's fresh if less than 24h old
		}
		rd, exists := dm.ResourceDownloads[resource]
		numFiles++
		if exists {
			dm.ResourceDownloads[resource] = append(rd, *task.TaskId)
		} else {
			dm.ResourceDownloads[resource] = []uint32{*task.TaskId}
			ft := &FileTransfer{
				TaskID:     *task.TaskId,
				FileType:   ResourceFile,
				SourcePath: resource,
				TargetPath: fmt.Sprintf("/scratch/resources/%s/", uuid.New()),
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
			dm.ResourceDownloads[container] = append(rd, *task.TaskId)
		} else {
			dm.ResourceDownloads[container] = []uint32{*task.TaskId}
			ft := &FileTransfer{
				TaskID:     *task.TaskId,
				FileType:   DockerImage,
				SourcePath: container,
			}
			go func(ft *FileTransfer) { dm.FileQueue <- ft }(ft)
		}
	}

	// Track total downloads needed
	dm.TaskDownloads[*task.TaskId] = numFiles
}

// handleFileCompletion updates the state when a download completes.
func (dm *DownloadManager) handleFileCompletion(fileMeta *FileMetadata, execQueue chan *pb.Task) {
	if fileMeta == nil {
		log.Printf("ERROR: empty FileMetadata")
		return
	}
	fm := *fileMeta
	log.Printf("✅ Download completed: %s", fm.SourcePath)
	//dm.ResourceMemory[filePath] = dm.ResourceMemory[filePath]

	// Check if it's a resource, notify all waiting tasks
	if fm.FileType == InputFile {

	}
	if fm.FileType == ResourceFile {
		if tasks, exists := dm.ResourceDownloads[fm.SourcePath]; exists {
			for _, taskID := range tasks {
				if count, ok := dm.TaskDownloads[taskID]; ok {
					dm.TaskDownloads[taskID] = count - 1
					if dm.TaskDownloads[taskID] == 0 {
						delete(dm.TaskDownloads, taskID)
						log.Printf("🚀 Task %d ready for execution", taskID)
					}
				}
			}
			delete(dm.ResourceDownloads, fm.SourcePath)
		}
	}

}

// extractFilename extracts filename from a path.
func extractFilename(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}
