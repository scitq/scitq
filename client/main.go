package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// optionalInt32 converts an int to a pointer (*int32).
func optionalInt32(v int) *uint32 {
	i := uint32(v)
	return &i
}

// WorkerConfig holds worker settings from CLI args.
type WorkerConfig struct {
	WorkerId    uint32
	ServerAddr  string
	Concurrency int
	Name        string
}

// ResizableSemaphore manages dynamic concurrency limits.
type ResizableSemaphore struct {
	tokens chan struct{}
	mu     sync.Mutex
	size   int
}

// NewResizableSemaphore initializes a semaphore with a given size.
func NewResizableSemaphore(size int) *ResizableSemaphore {
	return &ResizableSemaphore{
		tokens: make(chan struct{}, size),
		size:   size,
	}
}

// Acquire blocks until a token is available.
func (s *ResizableSemaphore) Acquire() {
	s.tokens <- struct{}{}
}

// Release returns a token to the semaphore.
func (s *ResizableSemaphore) Release() {
	<-s.tokens
}

// Resize adjusts the semaphore size dynamically.
func (s *ResizableSemaphore) Resize(newSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if newSize > s.size {
		// Expanding: Add extra capacity
		for i := 0; i < newSize-s.size; i++ {
			s.tokens <- struct{}{} // Add extra tokens
		}
	} else {
		// Shrinking: Block until running tasks complete
		for i := 0; i < s.size-newSize; i++ {
			<-s.tokens // Remove excess tokens
		}
	}

	s.size = newSize
}

// createClientConnection establishes a gRPC connection to the server.
func createClientConnection(serverAddr string) (*grpc.ClientConn, error) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})

	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	return conn, nil
}

// registerWorker registers the client worker with the server.
func (w *WorkerConfig) registerWorker(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.RegisterWorker(ctx, &pb.WorkerInfo{Name: w.Name, Concurrency: optionalInt32(w.Concurrency)})
	if err != nil {
		log.Fatalf("Failed to register worker: %v", err)
	} else {
		w.WorkerId = uint32(res.WorkerId)
	}
	log.Printf("✅ Worker %s registered with concurrency %d", w.Name, w.Concurrency)
}

// acknowledgeTask marks the task as "Accepted" (`C` status) before execution.
func acknowledgeTask(client pb.TaskQueueClient, taskID uint32) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{TaskId: taskID, NewStatus: "C"})
	if err != nil || !res.Success {
		log.Printf("❌ Failed to acknowledge task %d: %v", taskID, err)
		return false
	}
	log.Printf("📌 Task %d acknowledged (Accepted)", taskID)
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
func executeTask(client pb.TaskQueueClient, task *pb.Task, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Printf("🚀 Executing task %d: %s", task.TaskId, task.Command)

	// 🛑 Only acknowledge if the task is in "A"
	if task.Status != "A" {
		log.Printf("⚠️ Task %d is not assigned (A), skipping acknowledgment.", task.TaskId)
		return
	}
	if !acknowledgeTask(client, task.TaskId) {
		log.Printf("⚠️ Task %d could not be acknowledged, giving up execution.", task.TaskId)
		return // ❌ Do not execute if acknowledgment failed.
	}

	cmd := exec.Command("docker", "run", "--rm", task.Container, "sh", "-c", task.Command)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		log.Printf("❌ Failed to start task %d: %v", task.TaskId, err)
		updateTaskStatus(client, task.TaskId, "F") // Mark as failed
		return
	}

	// Open log stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.SendTaskLogs(ctx)
	if err != nil {
		log.Printf("⚠️ Failed to open log stream for task %d: %v", task.TaskId, err)
		cmd.Wait()
		updateTaskStatus(client, task.TaskId, "F") // Mark as failed
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
		updateTaskStatus(client, task.TaskId, "F")
	} else {
		log.Printf("✅ Task %d completed successfully", task.TaskId)
		updateTaskStatus(client, task.TaskId, "S")
	}
}

// fetchTasks requests new tasks from the server.
func fetchTasks(client pb.TaskQueueClient, id uint32, sem *ResizableSemaphore) []*pb.Task {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.PingAndTakeNewTasks(ctx, &pb.WorkerId{WorkerId: uint32(id)})
	if err != nil {
		log.Printf("⚠️ Error fetching tasks: %v", err)
		return nil
	}
	return res.Tasks
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(client pb.TaskQueueClient, config WorkerConfig, sem *ResizableSemaphore) {
	for {
		tasks := fetchTasks(client, config.WorkerId, sem)
		if len(tasks) == 0 {
			time.Sleep(5 * time.Second) // No tasks, wait before retrying
			continue
		}

		var wg sync.WaitGroup

		for _, task := range tasks {
			wg.Add(1)
			log.Printf("Received task %d with status: %v", task.TaskId, task.Status)
			sem.Acquire() // Block if max concurrency is reached
			go func(t *pb.Task) {
				executeTask(client, t, &wg)
				sem.Release() // Release semaphore slot after task completion
			}(task)
		}
		//wg.Wait() // Ensure all tasks finish before fetching new ones
		time.Sleep(1 * time.Second)
	}
}

// main initializes the client.
func main() {
	// Parse command-line arguments
	serverAddr := flag.String("server", "localhost:50051", "gRPC server address")
	concurrency := flag.Int("concurrency", 2, "Number of concurrent tasks")
	name := flag.String("name", "worker-1", "Worker name")
	flag.Parse()

	config := WorkerConfig{ServerAddr: *serverAddr, Concurrency: *concurrency, Name: *name}

	// Establish connection to the server
	conn, err := createClientConnection(config.ServerAddr)
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	defer conn.Close()

	client := pb.NewTaskQueueClient(conn)
	config.registerWorker(client)
	sem := NewResizableSemaphore(config.Concurrency)

	// Start processing tasks
	workerLoop(client, config, sem)
}
