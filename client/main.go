package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"flag"
	"io"
	"log"
	"os/exec"
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
	// Load TLS credentials
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})

	// Connect to the gRPC server
	// Create new gRPC client connection
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(creds))
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
	log.Printf("Worker %s registered with concurrency %d", name, concurrency)
}

// fetchTasks requests new tasks from the server.
func fetchTasks(client pb.TaskQueueClient, name string) []*pb.Task {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.PingAndTakeNewTasks(ctx, &pb.WorkerInfo{Name: name})
	if err != nil {
		log.Printf("Error fetching tasks: %v", err)
		return nil
	}
	return res.Tasks
}

// executeTask runs the Docker command for a given task and streams logs.
func executeTask(client pb.TaskQueueClient, task *pb.Task) {
	log.Printf("Executing task %d: %s", task.TaskId, task.Command)

	cmd := exec.Command("docker", "run", "--rm", task.Container, "sh", "-c", task.Command)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start task %d: %v", task.TaskId, err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.SendTaskLogs(ctx)
	if err != nil {
		log.Printf("Failed to open log stream: %v", err)
		return
	}

	// Function to read and send logs
	sendLogs := func(reader io.Reader, stream pb.TaskQueue_SendTaskLogsClient, isError bool) {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			logType := "stdout"
			if isError {
				logType = "stderr"
			}
			stream.Send(&pb.TaskLog{TaskId: task.TaskId, LogType: logType, LogText: line})
		}
	}

	// Stream stdout and stderr logs
	go sendLogs(stdout, stream, false)
	go sendLogs(stderr, stream, true)

	cmd.Wait() // Wait for task to complete
	stream.CloseSend()
	log.Printf("Task %d completed", task.TaskId)
}

// workerLoop continuously fetches and executes tasks.
func workerLoop(client pb.TaskQueueClient, config WorkerConfig) {
	for {
		tasks := fetchTasks(client, config.Name)
		if len(tasks) == 0 {
			time.Sleep(5 * time.Second) // No tasks, wait before retrying
			continue
		}

		// Process tasks concurrently
		sem := make(chan struct{}, config.Concurrency)
		for _, task := range tasks {
			sem <- struct{}{} // Limit concurrency
			go func(t *pb.Task) {
				executeTask(client, t)
				<-sem
			}(task)
		}
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
