package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/google/shlex"

	"github.com/scitq/scitq/client/event"
	"github.com/scitq/scitq/client/install"
	"github.com/scitq/scitq/client/workerstats"
	"github.com/scitq/scitq/fetch"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/utils"

	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

const fetchTaskErrorThreshold = 5

// the client is divided in several loops to accomplish its tasks:
// - the main loop (essentially this file)
//
//

// WorkerConfig holds worker settings from CLI args.
type WorkerConfig struct {
	WorkerId    uint32
	ServerAddr  string
	Concurrency uint32
	Name        string
	Store       string
	Token       string
}

var lostTrackSeen sync.Map // map[uint32]time.Time

// auto detect self specs
func (w *WorkerConfig) registerSpecs(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. CPU
	cpuCount := int32(runtime.NumCPU())

	// 2. Memory
	vmem, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("⚠️ Could not detect memory: %v", err)
		return
	}
	totalMem := float32(vmem.Total) / (1024 * 1024 * 1024)

	// 3. Disk under store path
	var stat syscall.Statfs_t
	if err := syscall.Statfs(w.Store, &stat); err != nil {
		log.Printf("⚠️ Could not statfs %s: %v", w.Store, err)
		return
	}
	freeDisk := float32(stat.Bavail) * float32(stat.Bsize) / (1024 * 1024 * 1024)

	// 4. Send
	req := &pb.ResourceSpec{
		WorkerId: fmt.Sprintf("%d", w.WorkerId),
		Cpu:      cpuCount,
		Mem:      totalMem,
		Disk:     freeDisk,
	}
	res, err := client.RegisterSpecifications(ctx, req)
	if err != nil {
		log.Printf("⚠️ Failed to register specs: %v", err)
		return
	}
	if res.Success {
		log.Printf("📦 Registered specs: CPU=%d MEM=%.1f GiB DISK=%.1f GiB",
			req.Cpu,
			req.Mem,
			req.Disk,
		)
	}
}

// registerWorker registers the client worker with the server.
func (w *WorkerConfig) registerWorker(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.RegisterWorker(ctx, &pb.WorkerInfo{Name: w.Name, Concurrency: &w.Concurrency})
	if err != nil {
		log.Fatalf("Failed to register worker: %v", err)
	} else {
		w.WorkerId = uint32(res.WorkerId)
	}
	w.registerSpecs(client)
	log.Printf("✅ Worker %s registered with concurrency %d", w.Name, w.Concurrency)
}

// updateTaskStatus marks task as `S` (Success) or `F` (Failed) after execution.
//func updateTaskStatus(client pb.TaskQueueClient, taskID uint32, status string) {
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	_, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
//		TaskId:    taskID,
//		NewStatus: status,
//	})
//	if err != nil {
//		log.Printf("⚠️ Failed to update task %d status to %s: %v", taskID, status, err)
//	} else {
//		log.Printf("✅ Task %d updated to status: %s", taskID, status)
//	}
//}

// executeTask runs the Docker command and streams logs.
func executeTask(client pb.TaskQueueClient, reporter *event.Reporter, task *pb.Task, wg *sync.WaitGroup, store string, dm *DownloadManager, cpu int32) {
	defer wg.Done()
	log.Printf("🚀 Executing task %d: %s", task.TaskId, task.Command)

	// 🛑 Only acknowledge if the task is in "C" or "D"
	if task.Status != "C" && task.Status != "D" {
		log.Printf("⚠️ Task %d is not accepted (C) or downloading (D) but %s, skipping acknowledgment.", task.TaskId, task.Status)
		reporter.Event("E", "task", fmt.Sprintf("task %d is not in accepted state", task.TaskId), map[string]any{
			"task_id": task.TaskId,
			"status":  task.Status,
			"command": task.Command,
		})
		return
	}
	task.Status = "R"
	reporter.UpdateTaskAsync(task.TaskId, "R", "")

	//TODO: linking resource
	for _, r := range task.Resource {
		dm.resourceLink(r, store+"/tasks/"+fmt.Sprint(task.TaskId)+"/resource")
	}

	if task.Status == "F" {
		return // ❌ Do not execute if task is marked as failed
	}

	command := []string{"run", "--rm", "-e", fmt.Sprintf("CPU=%d", cpu)}
	option := ""
	for _, folder := range []string{"input", "output", "tmp", "resource"} {
		if folder == "resource" {
			option = ":ro"
		}
		command = append(command, "-v", store+"/tasks/"+fmt.Sprint(task.TaskId)+"/"+folder+":/"+folder+option)
	}
	command = append(command, "-v", filepath.Join(store, helperFolder)+":/builtin:ro")

	if task.ContainerOptions != nil {
		options := strings.Fields(*task.ContainerOptions)
		command = append(command, options...)
	}
	if task.Shell != nil {
		command = append(command, task.Container, *task.Shell, "-c", task.Command)
	} else {
		args, err := shlex.Split(task.Command)
		if err != nil {
			message := fmt.Sprintf("Failed to analyze command %s: %v", task.Command, err)
			log.Printf("❌ %s", message)
			reporter.Event("E", "task", message, map[string]any{
				"task_id": task.TaskId,
				"command": task.Command,
			})
			task.Status = "F"                              // Mark as failed
			reporter.UpdateTask(task.TaskId, "V", message) // Mark as failed
			return
		}
		command = append(command, task.Container)
		command = append(command, args...)
	}

	log.Printf("🛠️  Running command: docker %s", strings.Join(command, " "))
	cmd := exec.Command("docker", command...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		message := fmt.Sprintf("Failed to start task %d: %v", task.TaskId, err)
		log.Printf("❌ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		task.Status = "F"                              // Mark as failed
		reporter.UpdateTask(task.TaskId, "V", message) // Mark as failed
		return
	}

	// Open log stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.SendTaskLogs(ctx)
	if err != nil {
		message := fmt.Sprintf("Failed to open log stream for task %d: %v", task.TaskId, err)
		log.Printf("⚠️ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		cmd.Wait()
		task.Status = "F"                              // Mark as failed
		reporter.UpdateTask(task.TaskId, "V", message) // Mark as failed
		log.Printf("⚠️ Failed to open log stream for task %d: %v", task.TaskId, err)
		return
	}

	// Function to send logs
	sendLogs := func(reader io.Reader, stream pb.TaskQueue_SendTaskLogsClient, logType string) {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			stream.Send(&pb.TaskLog{TaskId: task.TaskId, LogType: logType, LogText: line})
		}
		stream.CloseSend() // ✅ Ensure closure of log stream
	}

	// Stream logs concurrently
	go sendLogs(stdout, stream, "stdout")
	go sendLogs(stderr, stream, "stderr")

	// Wait for task completion
	err = cmd.Wait()
	stream.CloseSend()

	// **UPDATE TASK STATUS BASED ON SUCCESS/FAILURE**
	if err != nil {
		message := fmt.Sprintf("Task %d failed: %v", task.TaskId, err)
		log.Printf("❌ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		task.Status = "F"                              // Mark as failed
		reporter.UpdateTaskAsync(task.TaskId, "V", "") // Mark as failed
	} else {
		log.Printf("✅ Task %d completed successfully", task.TaskId)
		task.Status = "S" // Mark as success
		reporter.UpdateTaskAsync(task.TaskId, "U", "")
	}
}

// fetchTasks requests new tasks from the server.
func (w *WorkerConfig) fetchTasks(
	ctx context.Context,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	id uint32,
	sem *utils.ResizableSemaphore,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
) ([]*pb.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var query *pb.PingAndGetNewTasksRequest
	ws, err := workerstats.CollectWorkerStats()
	if err != nil {
		log.Printf("⚠️ Error could not collect stats: %v", err)
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id}
	} else {
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id, Stats: ws.ToProto()}
	}

	res, err := client.PingAndTakeNewTasks(ctx, query)
	if err != nil {
		log.Printf("⚠️ Error calling fetch tasks: %v", err)
		return nil, err
	}

	if res.Concurrency != uint32(w.Concurrency) {
		log.Printf("Resizing concurrency from %d to %d", w.Concurrency, res.Concurrency)
		sem.Resize(float64(res.Concurrency))
		w.Concurrency = res.Concurrency
	}

	// Apply task weights if any
	updates := make(map[uint32]float64)
	for taskID, update := range res.Updates.Updates {
		taskWeights.Store(taskID, update.Weight)
		updates[taskID] = update.Weight
	}
	sem.ResizeTasks(updates)

	// Check activeTasks map
	// 1) Build a set of server-active tasks
	serverActiveTasks := make(map[uint32]struct{})
	for _, tid := range res.ActiveTasks {
		serverActiveTasks[tid] = struct{}{}
	}

	// 2) For any server-active task we don't know locally, debounce before failing
	now := time.Now()
	for tid := range serverActiveTasks {
		if _, known := activeTasks.Load(tid); !known {
			// have we seen this "unknown" before?
			if v, ok := lostTrackSeen.Load(tid); ok {
				first := v.(time.Time)
				// if it has remained unknown for ≥ one fetch cycle (or e.g. ≥3s), warn once
				if now.Sub(first) >= 3*time.Second {
					// emit a single WARNING event, cllient will catch up later
					reporter.Event("W", "task", fmt.Sprintf("lost track of task %d", tid), map[string]any{
						"task_id":      tid,
						"component":    "fetchTasks",
						"active_count": func() int { n := 0; activeTasks.Range(func(_, _ any) bool { n++; return true }); return n }(),
					})
					lostTrackSeen.Delete(tid)
				}
			} else {
				// first time we see this mismatch: remember it and give the worker loop a chance to store
				lostTrackSeen.Store(tid, now)
				log.Printf("⏳ Server says task %d active but client doesn't know it yet; deferring decision.", tid)
			}
		} else {
			// if we do know it locally, clear any lingering first-seen marker
			lostTrackSeen.Delete(tid)
		}
	}

	activeTasks.Range(func(key, _ any) bool {
		taskID := key.(uint32)
		if _, known := serverActiveTasks[taskID]; !known {
			log.Printf("⚠️ Client believes task %d is active but server did not mention it. Will wait.", taskID)
		}
		return true
	})

	return res.Tasks, nil
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(ctx context.Context, client pb.TaskQueueClient, reporter *event.Reporter, config WorkerConfig, sem *utils.ResizableSemaphore, dm *DownloadManager, taskWeights *sync.Map, activeTasks *sync.Map) {
	store := dm.Store

	var consecErrors int
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Drain any failed-download notifications so we don't keep thinking those tasks are active
		for {
			select {
			case failedTask := <-dm.FailedQueue:
				if failedTask != nil {
					activeTasks.Delete(failedTask.TaskId)
					log.Printf("🧹 Cleared local active flag for failed task %d", failedTask.TaskId)
				}
				// keep draining until channel is empty
				continue
			default:
			}
			break
		}

		tasks, err := config.fetchTasks(ctx, client, reporter, config.WorkerId, sem, taskWeights, activeTasks)
		if err != nil {
			log.Printf("⚠️ Error fetching tasks: %v", err)
			consecErrors++
			if consecErrors >= fetchTaskErrorThreshold {
				reporter.Event("W", "runtime", fmt.Sprintf("consecutive fetch errors (%d)", consecErrors), map[string]any{
					"last_error": err.Error(),
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		consecErrors = 0 // Reset error count on success
		if len(tasks) == 0 {
			log.Printf("No tasks available, retrying in 5 seconds...")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, task := range tasks {
			// Drop duplicates from the server: if this task is already active or already being scheduled for downloads, ignore it.
			if _, alreadyActive := activeTasks.Load(task.TaskId); alreadyActive {
				log.Printf("🔁 Server returned duplicate task %d — already active; ignoring", task.TaskId)
				continue
			}
			if dm != nil && dm.EnqueuedTasks != nil && dm.EnqueuedTasks[task.TaskId] {
				log.Printf("🔁 Server returned duplicate task %d — already scheduled for downloads; ignoring", task.TaskId)
				continue
			}

			task.Status = "C" // Accepted
			activeTasks.Store(task.TaskId, struct{}{})
			reporter.UpdateTaskAsync(task.TaskId, task.Status, "")
			log.Printf("📝 Task %d accepted", task.TaskId)
			log.Printf("📌 Marked task %d active locally", task.TaskId)

			failed := false
			for _, folder := range []string{"input", "output", "tmp", "resource"} {
				path := filepath.Join(store, "tasks", fmt.Sprint(task.TaskId), folder)
				if err := os.MkdirAll(path, 0777); err != nil {
					log.Printf("⚠️ Failed to create directory %s for task %d: %v", folder, task.TaskId, err)

					// Report event
					reporter.Event("E", "task", fmt.Sprintf("failed to create directory %s for task %d", folder, task.TaskId), map[string]any{
						"task_id":    task.TaskId,
						"folder":     folder,
						"store_path": path,
						"error":      err.Error(),
					})

					// Mark as failed
					reporter.UpdateTask(task.TaskId, "F", fmt.Sprintf("failed to create directory %s for task %d: %v", folder, task.TaskId, err))
					failed = true
					break
				}
			}

			if failed {
				// Clean up bookkeeping
				activeTasks.Delete(task.TaskId)
				continue // do not enqueue into TaskQueue
			}

			// Only enqueue if all folders were created successfully
			dm.TaskQueue <- task
		}

	}
}

func excuterThread(
	exexQueue chan *pb.Task,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	sem *utils.ResizableSemaphore,
	store string,
	dm *DownloadManager,
	um *UploadManager,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
) {
	var wg sync.WaitGroup

	for task := range exexQueue {
		wg.Add(1)
		log.Printf("Received task %d with status: %v", task.TaskId, task.Status)

		// Get task weight or default to 1.0
		weight := 1.0
		if w, ok := taskWeights.Load(task.TaskId); ok {
			weight = w.(float64)
		}

		go func(t *pb.Task, w float64) {
			if err := sem.AcquireWithWeight(context.Background(), w, t.TaskId); err != nil {
				message := fmt.Sprintf("Failed to acquire semaphore for task %d: %v", t.TaskId, err)
				log.Printf("❌ %s", message)
				reporter.Event("E", "task", message, map[string]any{
					"task_id": t.TaskId,
					"status":  t.Status,
					"command": t.Command,
				})
				t.Status = "F" // Mark as failed
				reporter.UpdateTask(t.TaskId, "F", message)
				wg.Done()
				return
			}

			cpu := max(int32(float64(runtime.NumCPU())/sem.Size()), 1)
			log.Printf("Available CPU threads estimated to %d", cpu)
			executeTask(client, reporter, t, &wg, store, dm, cpu)

			sem.ReleaseTask(t.TaskId) // always release same weight
			um.EnqueueTaskOutput(t)

			// Mark task as no longer active on the client side
			activeTasks.Delete(t.TaskId)
			log.Printf("🧹 Cleared local active flag for task %d", t.TaskId)

			// Clean up memory if task is done
			taskWeights.Delete(t.TaskId)

		}(task, weight)
	}
}

// / client launcher
func Run(ctx context.Context, serverAddr string, concurrency uint32, name, store, token string) error {

	config := WorkerConfig{ServerAddr: serverAddr, Concurrency: concurrency, Name: name, Store: store, Token: token}
	taskWeights := &sync.Map{}
	activeTasks := &sync.Map{}

	// Establish connection to the server
	qclient, err := lib.CreateClient(config.ServerAddr, config.Token)
	if err != nil {
		return fmt.Errorf("could not connect to server: %v", err)
	}
	defer qclient.Close()

	if _, err := os.Stat(fetch.DefaultRcloneConfig); os.IsNotExist(err) {
		log.Printf("⚠️ Rclone config file not found, creating a new one.")
		rCloneConfig, err := qclient.Client.GetRcloneConfig(ctx, &emptypb.Empty{})
		if err != nil {
			event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "rclone", "Failed to get Rclone config", map[string]any{"error": err.Error()})
			return fmt.Errorf("could not get Rclone config: %v", err)
		}
		install.InstallRcloneConfig(rCloneConfig.Config, fetch.DefaultRcloneConfig)
	}

	// Ensure docker credentials are present (write once)
	if _, err := os.Stat(install.DockerCredentialFile); os.IsNotExist(err) {
		log.Printf("⚠️ Docker credentials file not found, creating a new one.")
		creds, err := qclient.Client.GetDockerCredentials(ctx, &emptypb.Empty{})
		if err != nil {
			event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "docker", "Failed to get Docker credentials", map[string]any{"error": err.Error()})
			return fmt.Errorf("could not get Docker credentials: %v", err)
		}
		if err := install.InstallDockerCredentials(creds); err != nil {
			event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "docker", "Failed to install Docker credentials", map[string]any{"error": err.Error()})
			return fmt.Errorf("could not install Docker credentials: %v", err)
		}
	}

	config.registerWorker(qclient.Client)
	createHelpers(config.Store)
	sem := utils.NewResizableSemaphore(float64(config.Concurrency))

	reporter := event.NewReporter(qclient.Client, config.WorkerId, config.Name, 5*time.Second)
	defer reporter.StopOutbox()

	//TODO: we need to add somehow an error state (or we could update the task status in the task with the error notably in case download fails)

	// Launching download Manager
	dm := RunDownloader(store, reporter)

	// Launching upload Manager
	um := RunUploader(store, qclient.Client, activeTasks, reporter)

	// Launching execution thread
	go excuterThread(dm.ExecQueue, qclient.Client, reporter, sem, store, dm, um, taskWeights, activeTasks)

	// Start processing tasks
	go workerLoop(ctx, qclient.Client, reporter, config, sem, dm, taskWeights, activeTasks)
	// Block until context is canceled
	<-ctx.Done()
	return ctx.Err()
}
