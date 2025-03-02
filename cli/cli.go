package cli

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
		List   *struct{} `arg:"subcommand:list" help:"List all workers"`
		Deploy *struct {
			Flavor      string `arg:"--flavor,required" help:"Worker flavor"`
			Provider    string `arg:"--provider,required" help:"Worker provider in the form providerName.configName like azure.primary"`
			Region      string `arg:"--region" help:"Worker region, default to provider default region"`
			Count       int    `arg:"--count" help:"How many worker to create" default:"1"`
			StepId      int    `arg:"--step" help:"Worker step ID if worker is affected to a task"`
			Concurrency int    `arg:"--concurrency" default:"1" help:"Worker initial concurrency"`
			Prefetch    int    `arg:"--prefetch" default:"0" help:"Worker initial prefetch"`
		} `arg:"subcommand:deploy" help:"Create and deploy a new worker instance"`
		Delete *struct {
			WorkerId int `arg:"--worker-id,required" help:"The ID of the worker to be deleted"`
		} `arg:"subcommand:delete" help:"Delete a worker instance"`
	} `arg:"subcommand:worker" help:"Manage workers"`

	// Flavor commands
	Flavor *struct {
		List *struct {
			Limit  int    `arg:"--limit" help:"Limit the number of answers" default:"10"`
			Filter string `arg:"--filter" help:"Filter flavor by a filter string like 'cpu>=12:mem>=30'"`
		} `arg:"subcommand:list" help:"List flavors"`
	} `arg:"subcommand:flavor" help:"Find flavors"`
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

	req := &pb.Task{
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
		fmt.Printf("🔹 ID: %d | Name: %s | Concurrency: %d | Prefetch: %d | Status: %s | IPv4: %s | IPv6: %s | Flavor: %s | Provider: %s | Region: %s\n",
			worker.WorkerId,
			worker.Name,
			worker.Concurrency,
			worker.Prefetch,
			worker.Status,
			worker.Ipv4,
			worker.Ipv6,
			worker.Flavor,
			worker.Provider,
			worker.Region)
	}
	return nil
}

// ListFlavors handles listing flavors.
func (c *CLI) FlavorList(limit uint32, filter string) error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.ListFlavorsRequest{Limit: limit, Filter: filter}
	res, err := c.QC.Client.ListFlavors(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching flavors: %w", err)
	}

	fmt.Println("👷 Flavor List:")
	for _, flavor := range res.Flavors {
		fmt.Printf("🔹 ID: %d | Provider: %s | Name: %s | CPU: %d | Mem: %.2f | Disk: %.2f | GPU: %s | Eviction: %.2f | Cost: %.2f\n",
			flavor.FlavorId, flavor.Provider, flavor.FlavorName, flavor.Cpu, flavor.Mem, flavor.Disk, flavor.Gpu, flavor.Eviction, flavor.Cost)
	}
	return nil
}

// WorkerDeploy deploys a new worker instance.
func (c *CLI) WorkerDeploy() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	var regionFilter string
	if c.Attr.Worker.Deploy.Region == "" {
		regionFilter = "region is default"
	} else {
		regionFilter = fmt.Sprintf("region=%s", c.Attr.Worker.Deploy.Region)
	}

	req := &pb.ListFlavorsRequest{
		Limit:  1,
		Filter: fmt.Sprintf("provider=%s:flavor_name=%s:%s", c.Attr.Worker.Deploy.Provider, c.Attr.Worker.Deploy.Flavor, regionFilter),
	}
	res, err := c.QC.Client.ListFlavors(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching flavor: %w", err)
	}
	if len(res.Flavors) != 1 {
		return fmt.Errorf("expected exactly one flavor, got %d", len(res.Flavors))
	}

	flavor := res.Flavors[0]
	// Now you can work with 'flavor'
	fmt.Printf("✅ Identified flavor: %s (ID: %d)\n", flavor.FlavorName, flavor.FlavorId)

	// Build the WorkerDeployRequest using parameters from CLI.
	req2 := &pb.WorkerRequest{
		ProviderId:  flavor.ProviderId,
		FlavorId:    flavor.FlavorId,
		RegionId:    flavor.RegionId,
		Number:      uint32(c.Attr.Worker.Deploy.Count),
		StepId:      uint32(c.Attr.Worker.Deploy.StepId),
		Concurrency: uint32(c.Attr.Worker.Deploy.Concurrency),
		Prefetch:    uint32(c.Attr.Worker.Deploy.Prefetch),
	}

	// Call the gRPC DeployWorker RPC.
	res2, err := c.QC.Client.CreateWorker(ctx, req2)
	if err != nil {
		return fmt.Errorf("error deploying worker: %w", err)
	}

	fmt.Printf("✅ Worker deployed with ID: %d\n", res2.WorkerIds)
	return nil
}

// WorkerDelete deletes a worker instance.
func (c *CLI) WorkerDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	// Call the gRPC DeleteWorker RPC.
	res, err := c.QC.Client.DeleteWorker(ctx, &pb.WorkerId{WorkerId: uint32(c.Attr.Worker.Delete.WorkerId)})
	if err != nil {
		return fmt.Errorf("error deleting worker: %w", err)
	}

	if res.Success {
		fmt.Printf("✅ Worker %d is being deleted\n", c.Attr.Worker.Delete.WorkerId)
		return nil
	} else {
		return fmt.Errorf("deletion order is in an unknown status for worker %d", c.Attr.Worker.Delete.WorkerId)
	}

}

func Run(c CLI) error {
	arg.MustParse(&c.Attr)

	// Establish gRPC connection
	qc, err := lib.CreateClient(c.Attr.Server)
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	c.QC = qc
	defer c.QC.Close()

	// Handle commands properly
	switch {
	// Task commands
	case c.Attr.Task != nil:
		switch {
		case c.Attr.Task.Create != nil:
			err = c.TaskCreate()
		case c.Attr.Task.List != nil:
			err = c.TaskList()
		case c.Attr.Task.Output != nil:
			err = c.TaskOutput()
		}
	// Worker commands
	case c.Attr.Worker != nil:
		switch {
		case c.Attr.Worker.List != nil:
			err = c.WorkerList()
		case c.Attr.Worker.Deploy != nil:
			err = c.WorkerDeploy()
		case c.Attr.Worker.Delete != nil:
			err = c.WorkerDelete()
		}
	case c.Attr.Flavor != nil:
		switch {
		case c.Attr.Flavor.List != nil:
			err = c.FlavorList(uint32(c.Attr.Flavor.List.Limit), c.Attr.Flavor.List.Filter)
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}

	return err
}
