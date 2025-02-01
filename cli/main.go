package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// createClientConnection establishes a gRPC connection to the server.
func createClientConnection(serverAddr string) (pb.TaskQueueClient, *grpc.ClientConn) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	// Create new gRPC client connection
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	return pb.NewTaskQueueClient(conn), conn
}

// createTask sends a task creation request.
func createTask(client pb.TaskQueueClient, container string, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskReq := &pb.TaskRequest{Command: command, Container: container}
	res, err := client.SubmitTask(ctx, taskReq)
	if err != nil {
		log.Fatalf("Error creating task: %v", err)
	}
	fmt.Printf("✅ Task created with ID: %d\n", res.TaskId)
}

// listTasks fetches and displays all tasks.
func listTasks(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.PingAndTakeNewTasks(ctx, &pb.WorkerInfo{Name: "cli"})
	if err != nil {
		log.Fatalf("Error fetching tasks: %v", err)
	}

	fmt.Println("📋 Task List:")
	if len(res.Tasks) == 0 {
		fmt.Println("No tasks available.")
		return
	}
	for _, task := range res.Tasks {
		fmt.Printf("🆔 ID: %d | 🖥️ Command: %s | 📦 Container: %s | 📌 Status: %s\n",
			task.TaskId, task.Command, task.Container, task.Status)
	}
}

// fetchTaskLogs fetches and prints logs of a task by ID.
func fetchTaskLogs(client pb.TaskQueueClient, taskID int32) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.TaskId{TaskId: taskID}
	stream, err := client.StreamTaskLogs(ctx, req)
	if err != nil {
		log.Fatalf("Error fetching task logs: %v", err)
	}

	fmt.Printf("📜 Logs for Task %d:\n", taskID)
	for {
		msg, err := stream.Recv()
		if err != nil {
			break // Stream finished
		}
		fmt.Printf("[%s] %s\n", msg.LogType, msg.LogText)
	}
}

func main() {
	// Define CLI arguments
	serverAddr := flag.String("server", "localhost:50051", "gRPC server address")
	container := flag.String("container", "", "Container to run (required for 'task create')")
	command := flag.String("command", "", "Command to execute (required for 'task create')")
	taskID := flag.Int("id", 0, "Task ID (required for 'task output')")
	flag.Parse()

	// Parse arguments
	if len(flag.Args()) < 2 {
		fmt.Println("Usage: go run cli/main.go <object> <action> [flags]")
		os.Exit(1)
	}

	object, action := flag.Args()[0], flag.Args()[1]

	// Create gRPC connection
	client, conn := createClientConnection(*serverAddr)
	defer conn.Close()

	// Handle task-related actions
	if object == "task" {
		switch action {
		case "create":
			if *container == "" || *command == "" {
				log.Fatal("Error: --container and --command are required for 'task create'")
			}
			createTask(client, *container, *command)

		case "list":
			listTasks(client)

		case "output":
			if *taskID == 0 {
				log.Fatal("Error: --id is required for 'task output'")
			}
			fetchTaskLogs(client, int32(*taskID))

		default:
			log.Fatalf("Unknown action: %s", action)
		}
	} else {
		log.Fatalf("Unknown object: %s", object)
	}
}
