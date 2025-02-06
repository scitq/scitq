package cli

import (
	"context"
	"fmt"
	"time"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/gmtsciencedev/scitq2/lib"
)

// CLI struct encapsulates task & worker commands
type Attr struct {
	Server  string `arg:"-s,--server,env:SCITQ_SERVER" default:"localhost:50051" help:"gRPC server address"`
	TimeOut int    `arg:"-t,--timeout" default:"5" help:"Timeout for server interaction (in seconds)"`

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

type CLI struct {
	QC   lib.QueueClient
	Attr Attr
}

// WithTimeout provides a context with a timeout
func (cli *CLI) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(cli.Attr.TimeOut)*time.Second)
}

// createTask sends a task creation request.
func (c *CLI) TaskCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.TaskRequest{
		Command:   c.Attr.Task.Create.Command,
		Container: c.Attr.Task.Create.Container,
	}
	res, err := c.QC.Client.SubmitTask(ctx, req)
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
	if c.Attr.Task.List.Status != "" {
		req.StatusFilter = &c.Attr.Task.List.Status
	}

	res, err := c.QC.Client.ListTasks(ctx, req)
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

	req := &pb.TaskId{TaskId: c.Attr.Task.Output.ID}
	stream, err := c.QC.Client.StreamTaskLogs(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching task logs: %v", err)
	}

	fmt.Printf("📜 Logs for Task %d:\n", c.Attr.Task.Output.ID)
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
	res, err := c.QC.Client.ListWorkers(ctx, req)
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
