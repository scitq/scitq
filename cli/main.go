package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/alexflint/go-arg"
	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/gmtsciencedev/scitq2/lib"
)

// CLI struct encapsulates task & worker commands
type CLI struct {
	Server  string `arg:"-s,--server,env:SCITQ_SERVER" default:"localhost:50051" help:"gRPC server address"`
	TimeOut int    `arg:"-t,--timeout" default:"5" help:"Timeout for server interaction (in seconds)"`
	qc      lib.QueueClient

	// Task Commands (Sub-Subcommands)
	Task *struct {
		Create *struct {
			Container string `arg:"--container,required" help:"Container to run"`
			Command   string `arg:"--command,required" help:"Command to execute"`
		} `arg:"subcommand:create" help:"Create a new task"`

		List *struct {
			Status string `arg:"--status" help:"Filter tasks by status"`
		} `arg:"subcommand:list" help:"List all tasks"`

		Output *struct {
			ID uint32 `arg:"--id,required" help:"Task ID"`
		} `arg:"subcommand:output" help:"Fetch task output logs"`
	} `arg:"subcommand:task" help:"Manage tasks"`

	// Worker Commands (Sub-Subcommands)
	Worker *struct {
		List *struct{} `arg:"subcommand:list" help:"List all workers"`
	} `arg:"subcommand:worker" help:"Manage workers"`
}

// WithTimeout provides a context with a timeout
func (cli *CLI) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(cli.TimeOut)*time.Second)
}

// createTask sends a task creation request.
func (c *CLI) TaskCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.TaskRequest{
		Command:   c.Task.Create.Command,
		Container: c.Task.Create.Container,
	}
	res, err := c.qc.Client.SubmitTask(ctx, req)
	if err != nil {
		return fmt.Errorf("error creating task: %w", err)
	}
	fmt.Printf("✅ Task created with ID: %d\n", res.TaskId)
	return nil
}

// listTasks fetches and displays all tasks.
func (c *CLI) TaskList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.ListTasksRequest{}
	if c.Task.List.Status != "" {
		req.StatusFilter = &c.Task.List.Status
	}

	res, err := c.qc.Client.ListTasks(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching tasks: %w", err)
	}

	fmt.Println("📋 Task List:")
	for _, task := range res.Tasks {
		fmt.Printf("🆔 ID: %d | Command: %s | Container: %s | Status: %s\n",
			task.TaskId, task.Command, task.Container, task.Status)
	}
	return nil
}

// fetchTaskLogs fetches and prints logs of a task by ID.
func (c *CLI) TaskOutput() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.TaskId{TaskId: c.Task.Output.ID}
	stream, err := c.qc.Client.StreamTaskLogs(ctx, req)
	if err != nil {
		return fmt.Errorf("Error fetching task logs: %v", err)
	}

	fmt.Printf("📜 Logs for Task %d:\n", c.Task.Output.ID)
	for {
		msg, err := stream.Recv()
		if err != nil {
			break // Stream finished
		}
		fmt.Printf("[%s] %s\n", msg.LogType, msg.LogText)
	}
	return nil
}

// ListWorkers handles listing workers
func (c *CLI) WorkerList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.ListWorkersRequest{}
	res, err := c.qc.Client.ListWorkers(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching workers: %w", err)
	}

	fmt.Println("👷 Worker List:")
	for _, worker := range res.Workers {
		fmt.Printf("🔹 ID: %d | Name: %s | Concurrency: %d\n",
			worker.WorkerId, worker.Name, worker.Concurrency)
	}
	return nil
}

func main() {
	var args CLI
	arg.MustParse(&args)

	// Establish gRPC connection
	qc, err := lib.CreateClient(args.Server)
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	args.qc = qc
	defer args.qc.Close()

	// Handle commands properly
	switch {
	// Task commands
	case args.Task != nil:
		switch {
		case args.Task.Create != nil:
			args.TaskCreate()
		case args.Task.List != nil:
			args.TaskList()
		case args.Task.Output != nil:
			args.TaskOutput()
		}
	// Worker commands
	case args.Worker != nil:
		switch {
		case args.Worker.List != nil:
			args.WorkerList()
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}
}
