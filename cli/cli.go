package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/internal/version"
	"github.com/scitq/scitq/lib"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"google.golang.org/protobuf/types/known/emptypb"
)

// CLI struct encapsulates task & worker commands
type Attr struct {
	Server  string `arg:"-s,--server,env:SCITQ_SERVER" default:"localhost:50051" help:"gRPC server address"`
	TimeOut int    `arg:"-t,--timeout" default:"5" help:"Timeout for server interaction (in seconds)"`

	// Task Commands (Sub-Subcommands)
	Task *struct {
		Create *struct {
			Name      *string  `arg:"--name" help:"Optional name of the task"`
			Container string   `arg:"--container,required" help:"Container to run"`
			Command   string   `arg:"--command,required" help:"Command to execute"`
			Shell     *string  `arg:"--shell" help:"Shell to use"`
			Input     []string `arg:"--input,separate" help:"Input values for the task (can be repeated)"`
			Resource  []string `arg:"--resource,separate" help:"Input values for the task (can be repeated)"`
			Output    string   `arg:"--output,separate" help:"Output folder where results are copied for the task"`
			StepId    *uint32  `arg:"--step-id" help:"Step ID if task is affected to a step"`
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
			StepId      uint32 `arg:"--step" help:"Worker step ID if worker is affected to a task"`
			Concurrency int    `arg:"--concurrency" default:"1" help:"Worker initial concurrency"`
			Prefetch    int    `arg:"--prefetch" default:"0" help:"Worker initial prefetch"`
		} `arg:"subcommand:deploy" help:"Create and deploy a new worker instance"`
		Delete *struct {
			WorkerId uint32 `arg:"--worker-id,required" help:"The ID of the worker to be deleted"`
		} `arg:"subcommand:delete" help:"Delete a worker instance"`
		Stats *struct {
			WorkerIds []uint32 `arg:"--worker-id,separate,required" help:"Worker IDs to get stats for"`
		} `arg:"subcommand:stats" help:"Fetch current stats for workers"`
	} `arg:"subcommand:worker" help:"Manage workers"`

	// Flavor commands
	Flavor *struct {
		List *struct {
			Limit  int    `arg:"--limit" help:"Limit the number of answers" default:"10"`
			Filter string `arg:"--filter" help:"Filter flavor by a filter string like 'cpu>=12:mem>=30'"`
		} `arg:"subcommand:list" help:"List flavors"`
	} `arg:"subcommand:flavor" help:"Find flavors"`

	// User commands
	User *struct {
		List   *struct{} `arg:"subcommand:list" help:"List all users"`
		Update *struct {
			UserId   uint32  `arg:"--id,required" help:"User ID to update"`
			Username *string `arg:"--username" help:"New username"`
			Email    *string `arg:"--email" help:"New email"`
			Admin    bool    `arg:"--admin" help:"Set admin status"`
			NoAdmin  bool    `arg:"--no-admin" help:"Remove admin rights"`
		} `arg:"subcommand:update" help:"Update a user"`
		Create *struct {
			Username string `arg:"--username,required" help:"New username"`
			Email    string `arg:"--email,required" help:"New email"`
			Password string `arg:"--password,required" help:"New password"`
			Admin    bool   `arg:"--admin" help:"Set admin status"`
		} `arg:"subcommand:create" help:"Create a new user"`
		Delete *struct {
			UserId uint32 `arg:"--id,required" help:"User ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a user"`
		ChangePassword *struct {
			Username string `arg:"--username,required" help:"Username for which password will be changed"`
		} `arg:"subcommand:change-password" help:"Change your password"`
	} `arg:"subcommand:user" help:"User management"`

	Recruiter *struct {
		List *struct {
			StepId uint32 `arg:"--step-id" help:"Step ID to filter"`
		} `arg:"subcommand:list" help:"List all recruiters"`
		Create *struct {
			StepId      uint32  `arg:"--step-id,required" help:"Step ID"`
			Rank        uint32  `arg:"--rank" default:"1" help:"Recruiter rank"`
			Protofilter string  `arg:"--protofilter,required" help:"A protofilter like 'cpu>=12:mem>=30' or 'flavor~Standard_D2s_%:region is default'"`
			Concurrency uint32  `arg:"--concurrency" default:"1" help:"Worker initial concurrency"`
			Prefetch    uint32  `arg:"--prefetch" default:"0" help:"Worker initial prefetch"`
			MaxWorkers  *uint32 `arg:"--max-workers" help:"Maximum number of workers"`
			Rounds      int     `arg:"--rounds" help:"Number of rounds"`
			Timeout     int     `arg:"--timeout" default:"10" help:"Timeout in seconds"`
		} `arg:"subcommand:create" help:"Create a new recruiter"`
		Delete *struct {
			StepId uint32 `arg:"--step-id,required" help:"Step ID to delete"`
			Rank   int    `arg:"--rank,required" help:"Recruiter rank to delete"`
		} `arg:"subcommand:delete" help:"Delete a recruiter"`
	} `arg:"subcommand:recruiter" help:"Recruiter management"`

	// Workflow commands
	Workflow *struct {
		List *struct {
			NameLike string `arg:"--name-like" help:"Filter workflows by name"`
		} `arg:"subcommand:list" help:"List workflows"`
		Create *struct {
			Name           string  `arg:"--name,required" help:"Workflow name"`
			RunStrategy    string  `arg:"--run-strategy" help:"Run strategy (one letter B/T/D or Z, defaulting to B): \n\t(B)atch wise, e.g. workers do all tasks of a certain step (default)\n\t(T)hread wise, e.g. workers focus on going as far as possible in the workflow for each entry point\n\t(D)ebug\n\t(Z)suspended"`
			MaximumWorkers *uint32 `arg:"--maximum-workers" help:"Maximum number of workers"`
		} `arg:"subcommand:create" help:"Create a new workflow"`
		Delete *struct {
			WorkflowId uint32 `arg:"--id,required" help:"Workflow ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a workflow"`
	} `arg:"subcommand:workflow" help:"Manage workflows"`

	// Step commands
	Step *struct {
		List *struct {
			WorkflowId uint32 `arg:"--workflow-id,required" help:"Workflow ID to list steps for"`
		} `arg:"subcommand:list" help:"List steps for a workflow"`
		Create *struct {
			WorkflowId   uint32 `arg:"--workflow-id" help:"Workflow ID"`
			WorkflowName string `arg:"--workflow-name" help:"Workflow name (alternative to ID)"`
			Name         string `arg:"--name,required" help:"Step name"`
		} `arg:"subcommand:create" help:"Create a step"`
		Delete *struct {
			StepId uint32 `arg:"--id,required" help:"Step ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a step"`
	} `arg:"subcommand:step" help:"Manage steps"`

	// File commands
	File *struct {
		List *struct {
			URI     string `arg:"positional,required" help:"URI to list files from (supports glob patterns)"`
			Timeout int    `arg:"--timeout" default:"30" help:"Timeout for listing files (in seconds)"`
		} `arg:"subcommand:list" help:"List remote files"`
	} `arg:"subcommand:file" help:"Remote file listing"`

	// (Workflow) Template commands
	Template *struct {
		Upload *struct {
			Path  string `arg:"--path,required" help:"Path to the Python template script"`
			Force bool   `arg:"--force" help:"Overwrite existing template with same name/version"`
		} `arg:"subcommand:upload" help:"Upload a new workflow template"`

		Run *struct {
			TemplateId uint32  `arg:"--id,required" help:"ID of the template to run"`
			ParamPairs *string `arg:"--param" help:"Comma-separated key=value pairs (e.g. a=1,b=2)"`
		} `arg:"subcommand:run" help:"Run a workflow template"`

		List *struct {
			Name    *string `arg:"--name" help:"Template name (can include wildcards like 'meta%')"`
			Version *string `arg:"--version" help:"Template version (can be 'latest' or a wildcard)"`
			Latest  bool    `arg:"--latest" help:"Show only latest version(s) for each template name"`
		} `arg:"subcommand:list" help:"List all uploaded workflow templates"`

		Detail *struct {
			TemplateId uint32 `arg:"--id,required" help:"Show detailed information for this template ID"`
		} `arg:"subcommand:detail" help:"Show a template's param JSON and metadata"`
	} `arg:"subcommand:template" help:"Manage workflow templates"`

	// (Workflow) Template run commands
	Run *struct {
		List *struct {
			TemplateId *uint32 `arg:"--template-id" help:"Filter runs by template ID"`
		} `arg:"subcommand:list" help:"List template runs"`

		Delete *struct {
			RunId uint32 `arg:"--id,required" help:"ID of the template run to delete"`
		} `arg:"subcommand:delete" help:"Delete a template run"`
	} `arg:"subcommand:run" help:"Manage template runs"`

	// Login commands
	Login *struct {
		User     *string `arg:"--user" help:"Username to log in"`
		Password *string `arg:"--password" help:"Password to log in"`
	} `arg:"subcommand:login" help:"Login and provide a token, use with export SCITQ_TOKENs=$(scitq login)"`

	// HashPassword command
	HashPassword *struct {
		Password string `arg:"positional,required" help:"Password to hash"`
	} `arg:"subcommand:hashpassword" help:"Hash a password using bcrypt (useful for config files)"`
}

// Version enables go-arg's built-in --version handling.
// When users run `scitq --version`, go-arg will print this string and exit.
func (Attr) Version() string {
	return "scitq " + version.Full()
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
		Shell:     c.Attr.Task.Create.Shell,
		Input:     c.Attr.Task.Create.Input,
		Resource:  c.Attr.Task.Create.Resource,
		Output:    &c.Attr.Task.Create.Output,
		StepId:    c.Attr.Task.Create.StepId,
		TaskName:  c.Attr.Task.Create.Name,
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
		var name string
		if task.TaskName != nil {
			name = " | Name: " + *task.TaskName
		} else {
			name = ""
		}
		fmt.Printf("🆔 ID: %d%s | Command: %s | Container: %s | Status: %s\n",
			task.TaskId, name, task.Command, task.Container, task.Status)
	}
	return nil
}

// fetchTaskLogs fetches and prints logs of a task by ID.
func (c *CLI) TaskOutput() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.TaskId{TaskId: c.Attr.Task.Output.ID}
	stream, err := c.QC.Client.StreamTaskLogsOutput(ctx, req)
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

func (c *CLI) WorkerStats() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.GetWorkerStatsRequest{
		WorkerIds: c.Attr.Worker.Stats.WorkerIds,
	}
	res, err := c.QC.Client.GetWorkerStats(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching worker stats: %w", err)
	}

	fmt.Println("📈 Worker Stats:")
	for workerID, stats := range res.WorkerStats {
		fmt.Printf("🆔 Worker ID: %d\n", workerID)
		fmt.Printf("  🖥️  CPU Usage:  %.2f%%\n", stats.CpuUsagePercent)
		fmt.Printf("  🧠 Memory Usage: %.2f%%\n", stats.MemUsagePercent)
		fmt.Printf("  📈 Load (1 min): %.2f\n", stats.Load_1Min)
		fmt.Printf("  ⏳ IO Wait: %.2f%%\n", stats.IowaitPercent)

		fmt.Println("  💽 Disks:")
		for _, d := range stats.Disks {
			fmt.Printf("    📦 %s: %.2f%% used\n", d.DeviceName, d.UsagePercent)
		}

		fmt.Println("  📀 Disk IO:")
		fmt.Printf("    📥 Read:  %.2f B/s (total %d bytes)\n", stats.DiskIo.ReadBytesRate, stats.DiskIo.ReadBytesTotal)
		fmt.Printf("    📤 Write: %.2f B/s (total %d bytes)\n", stats.DiskIo.WriteBytesRate, stats.DiskIo.WriteBytesTotal)

		fmt.Println("  🌐 Network IO:")
		fmt.Printf("    📥 Receive: %.2f B/s (total %d bytes)\n", stats.NetIo.RecvBytesRate, stats.NetIo.RecvBytesTotal)
		fmt.Printf("    📤 Send:    %.2f B/s (total %d bytes)\n", stats.NetIo.SentBytesRate, stats.NetIo.SentBytesTotal)
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
		StepId:      &c.Attr.Worker.Deploy.StepId,
		Concurrency: uint32(c.Attr.Worker.Deploy.Concurrency),
		Prefetch:    uint32(c.Attr.Worker.Deploy.Prefetch),
	}

	// Call the gRPC DeployWorker RPC.
	res2, err := c.QC.Client.CreateWorker(ctx, req2)
	if err != nil {
		return fmt.Errorf("error deploying worker: %w", err)
	}

	for _, w := range res2.WorkersDetails {
		fmt.Printf("✅ Worker deployed with ID: %d, Name: %s\n", w.WorkerId, w.WorkerName)
	}
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

	if res.JobId != 0 {
		fmt.Printf("✅ Worker %d is being deleted\n", c.Attr.Worker.Delete.WorkerId)
		return nil
	} else {
		return fmt.Errorf("deletion order is in an unknown status for worker %d", c.Attr.Worker.Delete.WorkerId)
	}

}

func ListUsers(client pb.TaskQueueClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.ListUsers(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to list users: %w", err)
	}

	fmt.Printf("%-10s %-20s %-30s %-8s\n", "User ID", "Username", "Email", "Admin")
	for _, user := range resp.Users {
		fmt.Printf("%-10d %-20s %-30s %-8t\n", user.UserId, user.GetUsername(), user.GetEmail(), user.GetIsAdmin())
	}
	return nil
}

func DeleteUser(client pb.TaskQueueClient, userId uint32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.DeleteUser(ctx, &pb.UserId{UserId: userId})
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	fmt.Println("✅ User deleted successfully")
	return nil
}

func UpdateUser(client pb.TaskQueueClient, user *pb.User) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.UpdateUser(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	fmt.Println("✅ User updated successfully")
	return nil
}

func ChangePassword(serverAddr, username string) error {
	qc, err := lib.CreateLoginClient(serverAddr)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer qc.Close()
	client := qc.Client

	// Prompt old password
	fmt.Print("🔐 Current password: ")
	oldPwBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("failed to read old password: %w", err)
	}

	// Prompt new password (twice)
	fmt.Print("🔐 New password: ")
	newPwBytes1, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("failed to read new password: %w", err)
	}

	fmt.Print("🔁 Confirm new password: ")
	newPwBytes2, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("failed to confirm new password: %w", err)
	}

	if string(newPwBytes1) != string(newPwBytes2) {
		return fmt.Errorf("❌ Passwords do not match")
	}

	_, err = client.ChangePassword(context.Background(), &pb.ChangePasswordRequest{
		Username:    username,
		OldPassword: string(oldPwBytes),
		NewPassword: string(newPwBytes1),
	})
	if err != nil {
		return fmt.Errorf("failed to change password: %w", err)
	}

	fmt.Println("✅ Password changed successfully")
	return nil
}

func CreateUser(client pb.TaskQueueClient, user *pb.CreateUserRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.CreateUser(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	fmt.Println("✅ User created successfully")
	return nil
}

func (c *CLI) RecruiterList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.RecruiterFilter{}
	if c.Attr.Recruiter.List.StepId != 0 {
		req.StepId = &c.Attr.Recruiter.List.StepId
	}

	res, err := c.QC.Client.ListRecruiters(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching recruiters: %w", err)
	}

	fmt.Println("🏗️ Recruiter List:")
	for _, r := range res.Recruiters {
		if r.MaxWorkers != nil {
			fmt.Printf("Step %d | Rank %d | Filter %s | Concurrency=%d Prefetch=%d Max=%d Rounds=%d Timeout=%d Maximum Workers=%d\n",
				r.StepId, r.Rank, r.Protofilter, r.Concurrency, r.Prefetch, r.MaxWorkers, r.Rounds, r.Timeout, *r.MaxWorkers)
		} else {
			fmt.Printf("Step %d | Rank %d | Filter %s | Concurrency=%d Prefetch=%d Rounds=%d Timeout=%d Maximum Workers=unlimited\n",
				r.StepId, r.Rank, r.Protofilter, r.Concurrency, r.Prefetch, r.Rounds, r.Timeout)
		}
	}
	return nil
}

func (c *CLI) RecruiterCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.Recruiter{
		StepId:      c.Attr.Recruiter.Create.StepId,
		Rank:        c.Attr.Recruiter.Create.Rank,
		Protofilter: c.Attr.Recruiter.Create.Protofilter,
		Concurrency: c.Attr.Recruiter.Create.Concurrency,
		Prefetch:    c.Attr.Recruiter.Create.Prefetch,
		MaxWorkers:  c.Attr.Recruiter.Create.MaxWorkers,
		Rounds:      uint32(c.Attr.Recruiter.Create.Rounds),
		Timeout:     uint32(c.Attr.Recruiter.Create.Timeout),
	}

	_, err := c.QC.Client.CreateRecruiter(ctx, req)
	if err != nil {
		return fmt.Errorf("error creating recruiter: %w", err)
	}
	fmt.Println("✅ Recruiter created successfully")
	return nil
}

func (c *CLI) RecruiterDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.RecruiterId{
		StepId: c.Attr.Recruiter.Delete.StepId,
		Rank:   uint32(c.Attr.Recruiter.Delete.Rank),
	}

	_, err := c.QC.Client.DeleteRecruiter(ctx, req)
	if err != nil {
		return fmt.Errorf("error deleting recruiter: %w", err)
	}

	fmt.Printf("✅ Recruiter step_id=%d rank=%d deleted\n", req.StepId, req.Rank)
	return nil
}

func (c *CLI) WorkflowList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.WorkflowFilter{NameLike: nil}
	if c.Attr.Workflow.List.NameLike != "" {
		req.NameLike = &c.Attr.Workflow.List.NameLike
	}

	res, err := c.QC.Client.ListWorkflows(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list workflows: %w", err)
	}

	fmt.Println("📘 Workflow List:")
	for _, w := range res.Workflows {
		if w.MaximumWorkers != nil {
			fmt.Printf("🔹 ID: %d | Name: %s | Strategy: %s | Max Workers: %d\n",
				w.WorkflowId, w.Name, w.RunStrategy, *w.MaximumWorkers)
		} else {
			fmt.Printf("🔹 ID: %d | Name: %s | Strategy: %s | Max Workers: unlimited\n",
				w.WorkflowId, w.Name, w.RunStrategy)
		}
	}
	return nil
}

func (c *CLI) WorkflowCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.WorkflowRequest{
		Name:           c.Attr.Workflow.Create.Name,
		RunStrategy:    &c.Attr.Workflow.Create.RunStrategy,
		MaximumWorkers: c.Attr.Workflow.Create.MaximumWorkers,
	}
	res, err := c.QC.Client.CreateWorkflow(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create workflow: %w", err)
	}
	fmt.Printf("✅ Created workflow with ID %d\n", res.WorkflowId)
	return nil
}

func (c *CLI) WorkflowDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	_, err := c.QC.Client.DeleteWorkflow(ctx, &pb.WorkflowId{WorkflowId: c.Attr.Workflow.Delete.WorkflowId})
	if err != nil {
		return fmt.Errorf("failed to delete workflow: %w", err)
	}
	fmt.Println("🗑️ Workflow deleted")
	return nil
}

func (c *CLI) StepList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	res, err := c.QC.Client.ListSteps(ctx, &pb.StepFilter{WorkflowId: c.Attr.Step.List.WorkflowId})
	if err != nil {
		return fmt.Errorf("failed to list steps: %w", err)
	}

	fmt.Printf("🪜 Steps in Workflow %d:\n", c.Attr.Step.List.WorkflowId)
	for _, s := range res.Steps {
		fmt.Printf("🔹 Step ID: %d | Name: %s\n", s.StepId, s.Name)
	}
	return nil
}

func (c *CLI) StepCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.StepRequest{
		Name:         c.Attr.Step.Create.Name,
		WorkflowName: &c.Attr.Step.Create.WorkflowName,
		WorkflowId:   &c.Attr.Step.Create.WorkflowId,
	}

	res, err := c.QC.Client.CreateStep(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create step: %w", err)
	}
	fmt.Printf("✅ Created step with ID %d\n", res.StepId)
	return nil
}

func (c *CLI) StepDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	_, err := c.QC.Client.DeleteStep(ctx, &pb.StepId{StepId: c.Attr.Step.Delete.StepId})
	if err != nil {
		return fmt.Errorf("failed to delete step: %w", err)
	}
	fmt.Println("🗑️ Step deleted")
	return nil
}

func (c *CLI) FileList() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Attr.File.List.Timeout)*time.Second)
	defer cancel()

	req := &pb.FetchListRequest{
		Uri: c.Attr.File.List.URI,
	}

	resp, err := c.QC.Client.FetchList(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list files via gRPC: %w", err)
	}

	for _, filename := range resp.Files {
		fmt.Println(filename)
	}
	return nil
}

func (c *CLI) TemplateUpload() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	path := c.Attr.Template.Upload.Path
	force := c.Attr.Template.Upload.Force

	// Read script file
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("❌ Failed to read script file: %w", err)
	}

	// Call UploadTemplate RPC
	resp, err := c.QC.Client.UploadTemplate(ctx, &pb.UploadTemplateRequest{
		Script: data,
		Force:  force,
	})
	if err != nil {
		return fmt.Errorf("❌ UploadTemplate RPC failed: %w", err)
	}

	// Report result
	if !resp.Success {
		return fmt.Errorf("❌ Upload failed: %s", resp.Message)
	}

	fmt.Println("✅ Upload successful:")
	fmt.Printf("  ID:        %d\n", resp.GetWorkflowTemplateId())
	fmt.Printf("  Name:      %s\n", resp.GetName())
	fmt.Printf("  Version:   %s\n", resp.GetVersion())
	fmt.Printf("  Desc:      %s\n", resp.GetDescription())

	if msg := strings.TrimSpace(resp.Message); msg != "" {
		fmt.Println("⚠️  Warnings:")
		fmt.Println(msg)
	}

	return nil
}

func (c *CLI) TemplateList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	filter := &pb.TemplateFilter{}
	if c.Attr.Template.List.Name != nil {
		filter.Name = c.Attr.Template.List.Name
	}
	if c.Attr.Template.List.Version != nil {
		filter.Version = c.Attr.Template.List.Version
	}
	if c.Attr.Template.List.Latest {
		latest := "latest"
		filter.Version = &latest
	}

	res, err := c.QC.Client.ListTemplates(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to list templates: %w", err)
	}

	if len(res.Templates) == 0 {
		fmt.Println("⚠️ No templates found.")
		return nil
	}

	fmt.Println("📦 Workflow Templates:")
	for _, t := range res.Templates {
		uploadedBy := "-"
		if t.UploadedBy != nil {
			uploadedBy = fmt.Sprintf("%d", *t.UploadedBy)
		}
		fmt.Printf("🔹 ID: %d | Name: %s | Version: %s | Description: %s | UploadedAt: %s | UploadedBy: %s\n",
			t.WorkflowTemplateId, t.Name, t.Version, t.Description, t.UploadedAt, uploadedBy)
	}
	return nil
}

func (c *CLI) TemplateDetail() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.TemplateFilter{WorkflowTemplateId: &c.Attr.Template.Detail.TemplateId}
	res, err := c.QC.Client.ListTemplates(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to query template: %w", err)
	}
	if len(res.Templates) == 0 {
		return fmt.Errorf("no template found with ID %d", c.Attr.Template.Detail.TemplateId)
	}
	t := res.Templates[0]

	fmt.Printf("📦 Template %s (v%s) — %s\n", t.Name, t.Version, t.Description)
	fmt.Printf("🕒 Uploaded: %s\n", t.UploadedAt)
	if t.UploadedBy != nil {
		fmt.Printf("👤 Uploaded by user ID: %d\n", *t.UploadedBy)
	}

	// Parse param JSON
	var params []struct {
		Name     string      `json:"name"`
		Type     string      `json:"type"`
		Required bool        `json:"required"`
		Default  interface{} `json:"default"`
		Choices  []string    `json:"choices"`
		Help     string      `json:"help"`
	}
	if err := json.Unmarshal([]byte(t.ParamJson), &params); err != nil {
		return fmt.Errorf("invalid param_json in template: %v\n%s", err, t.ParamJson)
	}

	if len(params) == 0 {
		fmt.Println("⚠️  No parameters defined.")
		return nil
	}

	// Pretty print
	fmt.Println("\n📋 Parameters:")
	fmt.Printf("%-15s %-10s %-8s %-15s %-20s %s\n", "Name", "Type", "Required", "Default", "Choices", "Help")
	fmt.Println(strings.Repeat("-", 90))
	for _, p := range params {
		typeFriendly := map[string]string{
			"str":   "string",
			"int":   "integer",
			"bool":  "boolean",
			"float": "float",
		}
		typ := typeFriendly[p.Type]
		if typ == "" {
			typ = p.Type
		}

		defVal := fmt.Sprintf("%v", p.Default)
		if defVal == "<nil>" {
			defVal = "-"
		}
		choices := "-"
		if len(p.Choices) > 0 {
			choices = strings.Join(p.Choices, ",")
		}
		help := p.Help
		if help == "" {
			help = "-"
		}

		fmt.Printf("%-15s %-10s %-8t %-15s %-20s %s\n", p.Name, typ, p.Required, defVal, choices, help)
	}
	return nil
}

func parseCommaSeparatedParams(input string) (string, error) {
	paramMap := make(map[string]string)
	pairs := strings.Split(input, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid param: %q (expected key=value)", pair)
		}
		paramMap[parts[0]] = parts[1]
	}
	jsonBytes, err := json.Marshal(paramMap)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func (c *CLI) TemplateRun() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	var paramJSON string
	var err error

	if c.Attr.Template.Run.ParamPairs != nil {
		paramJSON, err = parseCommaSeparatedParams(*c.Attr.Template.Run.ParamPairs)
		if err != nil {
			return err
		}
	}

	req := &pb.RunTemplateRequest{
		WorkflowTemplateId: c.Attr.Template.Run.TemplateId,
		ParamValuesJson:    paramJSON,
	}
	res, err := c.QC.Client.RunTemplate(ctx, req)
	if err != nil {
		return err
	}

	if res.Status != "S" {
		errMsg := "<unknown error>"
		if res.ErrorMessage != nil {
			errMsg = *res.ErrorMessage
		}
		return fmt.Errorf("template run failed: %s", errMsg)
	}

	fmt.Printf("🚀 Template run created with ID %d\n", res.TemplateRunId)
	return nil
}

func (c *CLI) RunList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	filter := &pb.TemplateRunFilter{WorkflowTemplateId: c.Attr.Run.List.TemplateId}

	res, err := c.QC.Client.ListTemplateRuns(ctx, filter)
	if err != nil {
		return fmt.Errorf("failed to list template runs: %w", err)
	}

	if len(res.Runs) == 0 {
		fmt.Println("⚠️ No template runs found.")
		return nil
	}

	fmt.Println("🏃 Template Runs:")
	for _, r := range res.Runs {
		username := "-"
		if r.RunByUsername != nil {
			username = *r.RunByUsername
		}

		templateName := "-"
		if r.TemplateName != nil {
			templateName = *r.TemplateName
		}
		templateVersion := "-"
		if r.TemplateVersion != nil {
			templateVersion = *r.TemplateVersion
		}
		workflowName := "-"
		if r.WorkflowName != nil {
			workflowName = *r.WorkflowName
		}

		fmt.Printf("🔸 ID: %d | Template: %s v%s | Workflow: %s | Created: %s | Status: %s | User: %s\n",
			r.TemplateRunId,
			templateName,
			templateVersion,
			workflowName,
			r.CreatedAt,
			r.Status,
			username,
		)
	}
	return nil
}

func (c *CLI) RunDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.DeleteTemplateRunRequest{
		TemplateRunId: c.Attr.Run.Delete.RunId,
	}

	_, err := c.QC.Client.DeleteTemplateRun(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete template run %d: %w", c.Attr.Run.Delete.RunId, err)
	}

	fmt.Printf("🗑️ Deleted template run with ID %d\n", c.Attr.Run.Delete.RunId)
	return nil
}

func Run(c CLI) error {
	arg.MustParse(&c.Attr)

	switch {
	case c.Attr.Login != nil:
		fmt.Print(createToken(c.Attr.Server, c.Attr.Login.User, c.Attr.Login.Password))
		return nil
	case c.Attr.HashPassword != nil:
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(c.Attr.HashPassword.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("Failed to hash password: %v", err)
		}
		fmt.Printf("Hashed password: %s\n", string(hashedPassword))
		return nil
	}
	// Establish gRPC connection
	// Ensure token exists (interactive if needed)
	token, err := getToken()

	if err != nil {
		log.Fatalf("Could not create client: %v", err)
	}
	qc, err := lib.CreateClient(c.Attr.Server, token)
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
		case c.Attr.Worker.Stats != nil:
			err = c.WorkerStats()
		}
	case c.Attr.Flavor != nil:
		switch {
		case c.Attr.Flavor.List != nil:
			err = c.FlavorList(uint32(c.Attr.Flavor.List.Limit), c.Attr.Flavor.List.Filter)
		}
	// In Run(), after handling Flavor, add:
	case c.Attr.User != nil:
		switch {
		case c.Attr.User.List != nil:
			err = ListUsers(c.QC.Client)
		case c.Attr.User.Update != nil:
			if c.Attr.User.Update.Admin && c.Attr.User.Update.NoAdmin {
				return fmt.Errorf("cannot use both --admin and --no-admin")
			}
			var IsAdmin *bool
			if c.Attr.User.Update.Admin {
				val := true
				IsAdmin = &val
			} else if c.Attr.User.Update.NoAdmin {
				val := false
				IsAdmin = &val
			}
			user := &pb.User{
				UserId:   c.Attr.User.Update.UserId,
				Username: c.Attr.User.Update.Username,
				Email:    c.Attr.User.Update.Email,
				IsAdmin:  IsAdmin,
			}
			err = UpdateUser(c.QC.Client, user)
		case c.Attr.User.Delete != nil:
			err = DeleteUser(c.QC.Client, c.Attr.User.Delete.UserId)
		case c.Attr.User.Create != nil:
			user := &pb.CreateUserRequest{
				Username: c.Attr.User.Create.Username,
				Email:    c.Attr.User.Create.Email,
				Password: c.Attr.User.Create.Password,
				IsAdmin:  c.Attr.User.Create.Admin,
			}
			err = CreateUser(c.QC.Client, user)
		case c.Attr.User.ChangePassword != nil:
			err = ChangePassword(c.Attr.Server, c.Attr.User.ChangePassword.Username)
		}
	case c.Attr.Recruiter != nil:
		switch {
		case c.Attr.Recruiter.List != nil:
			return c.RecruiterList()
		case c.Attr.Recruiter.Create != nil:
			return c.RecruiterCreate()
		case c.Attr.Recruiter.Delete != nil:
			return c.RecruiterDelete()
		default:
			return fmt.Errorf("no recruiter subcommand specified")
		}
	case c.Attr.Workflow != nil:
		switch {
		case c.Attr.Workflow.List != nil:
			err = c.WorkflowList()
		case c.Attr.Workflow.Create != nil:
			err = c.WorkflowCreate()
		case c.Attr.Workflow.Delete != nil:
			err = c.WorkflowDelete()
		}
	case c.Attr.Step != nil:
		switch {
		case c.Attr.Step.List != nil:
			err = c.StepList()
		case c.Attr.Step.Create != nil:
			err = c.StepCreate()
		case c.Attr.Step.Delete != nil:
			err = c.StepDelete()
		}
	case c.Attr.File != nil:
		switch {
		case c.Attr.File.List != nil:
			err = c.FileList()
		}
	case c.Attr.Template != nil:
		switch {
		case c.Attr.Template.Upload != nil:
			err = c.TemplateUpload()
		case c.Attr.Template.List != nil:
			err = c.TemplateList()
		case c.Attr.Template.Detail != nil:
			err = c.TemplateDetail()
		case c.Attr.Template.Run != nil:
			err = c.TemplateRun()
		}
	case c.Attr.Run != nil:
		switch {
		case c.Attr.Run.List != nil:
			err = c.RunList()
		case c.Attr.Run.Delete != nil:
			err = c.RunDelete()
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}

	return err
}
