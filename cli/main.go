package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// createClientConnection establishes a gRPC connection to the server.
func createClientConnection(serverAddr string) (pb.TaskQueueClient, *grpc.ClientConn) {
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})

	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	return pb.NewTaskQueueClient(conn), conn
}

// createTask sends a task creation request.
func createTask(cmd *cobra.Command, args []string) {
	server, _ := cmd.Flags().GetString("server")
	container, _ := cmd.Flags().GetString("container")
	command, _ := cmd.Flags().GetString("command")

	if container == "" || command == "" {
		log.Fatal("Error: --container and --command are required")
	}

	client, conn := createClientConnection(server)
	defer conn.Close()

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
func listTasks(cmd *cobra.Command, args []string) {
	server, _ := cmd.Flags().GetString("server")
	statusFilter, _ := cmd.Flags().GetString("status")

	client, conn := createClientConnection(server)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.ListTasksRequest{}
	if statusFilter != "" {
		req.StatusFilter = &statusFilter
	}

	res, err := client.ListTasks(ctx, req)
	if err != nil {
		log.Fatalf("Error fetching tasks: %v", err)
	}

	fmt.Println("📋 Task List:")
	if len(res.Tasks) == 0 {
		fmt.Println("No tasks available.")
		return
	}
	for _, task := range res.Tasks {
		fmt.Printf("🆔 ID: %d | 🖥️  Command: %s | 📦 Container: %s | 📌 Status: %s\n",
			task.TaskId, task.Command, task.Container, task.Status)
	}
}

// fetchTaskLogs fetches and prints logs of a task by ID.
func fetchTaskLogs(cmd *cobra.Command, args []string) {
	server, _ := cmd.Flags().GetString("server")
	taskID, _ := cmd.Flags().GetInt32("id")

	if taskID == 0 {
		log.Fatal("Error: --id is required")
	}

	client, conn := createClientConnection(server)
	defer conn.Close()

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
	var rootCmd = &cobra.Command{Use: "cli"}

	// Global flags
	rootCmd.PersistentFlags().String("server", "localhost:50051", "gRPC server address")

	// Task Command
	var taskCmd = &cobra.Command{Use: "task", Short: "Manage tasks"}
	rootCmd.AddCommand(taskCmd)

	// task create
	var createCmd = &cobra.Command{
		Use:   "create",
		Short: "Create a new task",
		Run:   createTask,
	}
	createCmd.Flags().String("container", "", "Container to run (required)")
	createCmd.Flags().String("command", "", "Command to execute (required)")
	taskCmd.AddCommand(createCmd)

	// task list
	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "List all tasks",
		Run:   listTasks,
	}
	listCmd.Flags().String("status", "", "Filter tasks by status")
	taskCmd.AddCommand(listCmd)

	// task output
	var outputCmd = &cobra.Command{
		Use:   "output",
		Short: "Fetch task output logs",
		Run:   fetchTaskLogs,
	}
	outputCmd.Flags().Int32("id", 0, "Task ID (required)")
	taskCmd.AddCommand(outputCmd)

	// Run CLI
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
