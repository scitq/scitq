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

// WorkerConfig holds worker settings from CLI args.
type WorkerConfig struct {
	ServerAddr  string
	Concurrency int
	Name        string
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
func registerWorker(client pb.TaskQueueClient, name string, concurrency int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.RegisterWorker(ctx, &pb.WorkerInfo{Name: name, Concurrency: int32(concurrency)})
	if err != nil {
		log.Fatalf("Failed to register worker: %v", err)
	}
	log.Printf("✅ Worker %s registered with concurrency %d", name, concurrency)
}

// acknowledgeTask marks the task as "Accepted" (`C` status) before execution.
func acknowledgeTask(client pb.TaskQueueClient, taskID int32) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
		TaskId:    taskID,
		NewStatus: "C",
	})
	if err != nil {
		log.Printf("⚠️ Failed to acknowledge task %d: %v", taskID, err)
	} else {
		log.Printf("📌 Task %d acknowledged (Accepted)", taskID)
	}
}

// updateTaskStatus marks task as `S` (Success) or `F` (Failed) after execution.
func updateTaskStatus(client pb.TaskQueueClient, taskID int32, status string) {
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

	// **ACKNOWLEDGE THE TASK ("C" - Accepted)**
	acknowledgeTask(client, task.TaskId)

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
func fetchTasks(client pb.TaskQueueClient, name string) []*pb.Task {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.PingAndTakeNewTasks(ctx, &pb.WorkerInfo{Name: name})
	if err != nil {
		log.Printf("⚠️ Error fetching tasks: %v", err)
		return nil
	}
	return res.Tasks
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(client pb.TaskQueueClient, config WorkerConfig) {
	sem := make(chan struct{}, config.Concurrency) // Concurrency control
	for {
		tasks := fetchTasks(client, config.Name)
		if len(tasks) == 0 {
			time.Sleep(5 * time.Second) // No tasks, wait before retrying
			continue
		}

		var wg sync.WaitGroup

		for _, task := range tasks {
			wg.Add(1)
			sem <- struct{}{} // Block if max concurrency is reached
			go func(t *pb.Task) {
				executeTask(client, t, &wg)
				<-sem // Release semaphore slot after task completion
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
	registerWorker(client, config.Name, config.Concurrency)

	// Start processing tasks
	workerLoop(client, config)
}
