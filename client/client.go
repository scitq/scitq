package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gmtsciencedev/scitq2/client/install"
	"github.com/gmtsciencedev/scitq2/fetch"
	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/gmtsciencedev/scitq2/lib"
	"github.com/gmtsciencedev/scitq2/utils"

	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// the client is divided in several loops to accomplish its tasks:
// - the main loop (essentially this file)
//
//

// send simple log message
func logMessage(msg string, client pb.TaskQueueClient, taskID uint32) {
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

// WorkerConfig holds worker settings from CLI args.
type WorkerConfig struct {
	WorkerId    uint32
	ServerAddr  string
	Concurrency uint32
	Name        string
	Store       string
	Token       string
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
	log.Printf("✅ Worker %s registered with concurrency %d", w.Name, w.Concurrency)
}

// acknowledgeTask marks the task as "Running" (`R` status) before execution.
func acknowledgeTask(client pb.TaskQueueClient, taskID uint32) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: taskID, NewStatus: "R"})

	if err != nil || !res.Success {
		log.Printf("❌ Failed to acknowledge task %d: %v", taskID, err)
		return false
	}
	log.Printf("📌 Task %d running", taskID)
	return true
}

// updateTaskStatus marks task as `S` (Success) or `F` (Failed) after execution.
func updateTaskStatus(client pb.TaskQueueClient, taskID uint32, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    taskID,
		NewStatus: status,
	})
	if err != nil {
		log.Printf("⚠️ Failed to update task %d status to %s: %v", taskID, status, err)
	} else {
		log.Printf("✅ Task %d updated to status: %s", taskID, status)
	}
}

// executeTask runs the Docker command and streams logs.
func executeTask(client pb.TaskQueueClient, task *pb.Task, wg *sync.WaitGroup, store string, dm *DownloadManager) {
	defer wg.Done()
	log.Printf("🚀 Executing task %d: %s", task.TaskId, task.Command)

	// 🛑 Only acknowledge if the task is in "C" or "D"
	if task.Status != "C" && task.Status != "D" {
		log.Printf("⚠️ Task %d is not accepted (C) or downloading (D) but %s, skipping acknowledgment.", task.TaskId, task.Status)
		return
	}
	if !acknowledgeTask(client, task.TaskId) {
		log.Printf("⚠️ Task %d could not be acknowledged, giving up execution.", task.TaskId)
		return // ❌ Do not execute if acknowledgment failed.
	}
	task.Status = "R"

	//TODO: linking resource
	for _, r := range task.Resource {
		dm.resourceLink(r, store+"/tasks/"+fmt.Sprint(task.TaskId)+"/resource")
	}

	command := []string{"run", "--rm"}
	for _, folder := range []string{"input", "output", "tmp", "resource"} {
		command = append(command, "-v", store+"/tasks/"+fmt.Sprint(task.TaskId)+"/"+folder+":/"+folder)
	}

	if task.ContainerOptions != nil {
		options := strings.Fields(*task.ContainerOptions)
		command = append(command, options...)
	}
	if task.Shell != nil {
		command = append(command, task.Container, *task.Shell, "-c", task.Command)
	} else {
		command = append(command, task.Container, task.Command)
	}

	log.Printf("🛠️  Running command: docker %s", strings.Join(command, " "))
	cmd := exec.Command("docker", command...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		log.Printf("❌ Failed to start task %d: %v", task.TaskId, err)
		task.Status = "F"                          // Mark as failed
		updateTaskStatus(client, task.TaskId, "V") // Mark as failed
		return
	}

	// Open log stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.SendTaskLogs(ctx)
	if err != nil {
		log.Printf("⚠️ Failed to open log stream for task %d: %v", task.TaskId, err)
		cmd.Wait()
		task.Status = "F"                          // Mark as failed
		updateTaskStatus(client, task.TaskId, "V") // Mark as failed
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
		log.Printf("❌ Task %d failed: %v", task.TaskId, err)
		task.Status = "F"                          // Mark as failed
		updateTaskStatus(client, task.TaskId, "V") // Mark as failed
	} else {
		log.Printf("✅ Task %d completed successfully", task.TaskId)
		task.Status = "S"                          // Mark as success
		updateTaskStatus(client, task.TaskId, "U") // Mark as success
	}
}

// fetchTasks requests new tasks from the server.
func (w *WorkerConfig) fetchTasks(
	client pb.TaskQueueClient,
	id uint32,
	sem *utils.ResizableSemaphore,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
) []*pb.Task {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.PingAndTakeNewTasks(ctx, &pb.WorkerId{WorkerId: id})
	if err != nil {
		log.Printf("⚠️ Error fetching tasks: %v", err)
		return nil
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
	serverActiveTasks := make(map[uint32]struct{})
	for _, tid := range res.ActiveTasks {
		serverActiveTasks[tid] = struct{}{}
		_, known := activeTasks.Load(tid)
		if !known {
			log.Printf("⚠️ Server believes task %d is active, but client lost track. Reporting failure.", tid)
			go func(tid uint32) {
				logMessage("Client lost track of task", client, tid)
				updateTaskStatus(client, tid, "F")
			}(tid)
		}
	}
	activeTasks.Range(func(key, _ any) bool {
		taskID := key.(uint32)
		if _, known := serverActiveTasks[taskID]; !known {
			log.Printf("⚠️ Client believes task %d is active but server did not mention it. Will wait.", taskID)
		}
		return true
	})

	return res.Tasks
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(client pb.TaskQueueClient, config WorkerConfig, sem *utils.ResizableSemaphore, dm *DownloadManager, taskWeights *sync.Map, activeTasks *sync.Map) {
	store := dm.Store

	for {
		tasks := config.fetchTasks(client, config.WorkerId, sem, taskWeights, activeTasks)
		if len(tasks) == 0 {
			log.Printf("No tasks available, retrying in 5 seconds...")
			time.Sleep(5 * time.Second) // No tasks, wait before retrying
			continue
		}

		for _, task := range tasks {
			task.Status = "C" // Mark as "C" (Accepted)
			activeTasks.Store(task.TaskId, struct{}{})
			updateTaskStatus(client, task.TaskId, task.Status)
			log.Printf("📝 Task %d accepted", task.TaskId)
			for _, folder := range []string{"input", "output", "tmp", "resource"} {
				if err := os.MkdirAll(store+"/tasks/"+fmt.Sprint(task.TaskId)+"/"+folder, 0777); err != nil {
					log.Printf("⚠️ Failed to create directory %s for task %d: %v", folder, task.TaskId, err)
				}
			}
			dm.TaskQueue <- task
		}

	}
}

func excuterThread(
	exexQueue chan *pb.Task,
	client pb.TaskQueueClient,
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
				log.Printf("❌ Failed to acquire semaphore for task %d: %v", t.TaskId, err)
				wg.Done()
				return
			}

			executeTask(client, t, &wg, store, dm)

			sem.ReleaseTask(t.TaskId) // always release same weight
			um.EnqueueTaskOutput(t)

			// Clean up memory if task is done
			taskWeights.Delete(t.TaskId)

		}(task, weight)
	}
}

// / client launcher
func Run(serverAddr string, concurrency uint32, name, store, token string) error {

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
		rCloneConfig, err := qclient.Client.GetRcloneConfig(context.Background(), &emptypb.Empty{})
		if err != nil {
			return fmt.Errorf("could not get Rclone config: %v", err)
		}
		install.InstallRcloneConfig(rCloneConfig.Config, fetch.DefaultRcloneConfig)
	}

	config.registerWorker(qclient.Client)
	sem := utils.NewResizableSemaphore(float64(config.Concurrency))

	//TODO: we need to add somehow an error state (or we could update the task status in the task with the error notably in case download fails)

	// Launching download Manager
	dm := RunDownloader(store)

	// Launching upload Manager
	um := RunUploader(store, qclient.Client, activeTasks)

	// Launching execution thread
	go excuterThread(dm.ExecQueue, qclient.Client, sem, store, dm, um, taskWeights, activeTasks)

	// Start processing tasks
	go workerLoop(qclient.Client, config, sem, dm, taskWeights, activeTasks)
	select {} // Block forever
}
