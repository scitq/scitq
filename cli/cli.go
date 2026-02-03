package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/scitq/scitq/fetch"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/internal/version"
	"github.com/scitq/scitq/lib"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"google.golang.org/protobuf/types/known/emptypb"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v3"
)

// CLI struct encapsulates task & worker commands
type Attr struct {
	// Config commands
	Config *struct {
		ImportRclone *struct {
			Path string `arg:"positional" default:"~/.config/rclone/rclone.conf" help:"Path to existing rclone.conf to import"`
		} `arg:"subcommand:import-rclone" help:"Import an existing rclone.conf and print YAML fragment"`
	} `arg:"subcommand:config" help:"Configuration tools"`

	Server  string `arg:"-s,--server,env:SCITQ_SERVER" default:"localhost:50051" help:"gRPC server address"`
	TimeOut int    `arg:"-t,--timeout" default:"300" help:"Timeout for server interaction (in seconds)"`
	Token   string `arg:"-T,--token" default:"" help:"authentication token (used for tests)"`

	// Task Commands (Sub-Subcommands)
	Task *struct {
		Create *struct {
			Name         *string  `arg:"--name" help:"Optional name of the task"`
			Container    string   `arg:"--container,required" help:"Container to run"`
			Command      string   `arg:"--command,required" help:"Command to execute"`
			Shell        *string  `arg:"--shell" help:"Shell to use"`
			Input        []string `arg:"--input,separate" help:"Input values for the task (can be repeated)"`
			Resource     []string `arg:"--resource,separate" help:"Input values for the task (can be repeated)"`
			Output       string   `arg:"--output,separate" help:"Output folder where results are copied for the task"`
			StepId       *int32   `arg:"--step-id" help:"Step ID if task is affected to a step"`
			Dependencies []int32  `arg:"--dependency,separate" help:"IDs of tasks that this task depends on (can be repeated)"`
		} `arg:"subcommand:create" help:"Create a new task"`

		List *struct {
			Status     string `arg:"--status" help:"Filter tasks by status"`
			ShowHidden bool   `arg:"--show-hidden" help:"Include tasks marked as hidden (e.g., previous failed attempts)"`
			WorkerId   int32  `arg:"--worker-id" help:"Filter tasks by worker ID"`
			WorkflowId int32  `arg:"--workflow-id" help:"Filter tasks by workflow ID"`
			StepId     int32  `arg:"--step-id" help:"Filter tasks by step ID"`
			Command    string `arg:"--command" help:"Filter tasks by command substring"`
			Limit      int32  `arg:"--limit" default:"20" help:"Limit the number of tasks returned"`
			Offset     int32  `arg:"--offset" help:"Offset for pagination of tasks"`
		} `arg:"subcommand:list" help:"List all tasks"`

		Output *struct {
			ID int32 `arg:"--id,required" help:"Task ID"`
		} `arg:"subcommand:output" help:"Fetch task output logs"`

		Retry *struct {
			ID    int32  `arg:"--id,required" help:"Task ID to retry"`
			Retry *int32 `arg:"--retry" help:"Number of retries left (optional)"`
		} `arg:"subcommand:retry" help:"Retry a failed task"`
	} `arg:"subcommand:task" help:"Manage tasks"`

	// Worker Commands (Sub-Subcommands)
	Worker *struct {
		List   *struct{} `arg:"subcommand:list" help:"List all workers"`
		Deploy *struct {
			Flavor      string `arg:"--flavor,required" help:"Worker flavor"`
			Provider    string `arg:"--provider,required" help:"Worker provider in the form providerName.configName like azure.primary"`
			Region      string `arg:"--region" help:"Worker region, default to provider default region"`
			Count       int    `arg:"--count" help:"How many worker to create" default:"1"`
			StepId      int32  `arg:"--step" help:"Worker step ID if worker is affected to a task"`
			Concurrency int    `arg:"--concurrency" default:"1" help:"Worker initial concurrency"`
			Prefetch    int    `arg:"--prefetch" default:"0" help:"Worker initial prefetch"`
		} `arg:"subcommand:deploy" help:"Create and deploy a new worker instance"`
		Delete *struct {
			WorkerId int32 `arg:"--worker-id,required" help:"The ID of the worker to be deleted"`
		} `arg:"subcommand:delete" help:"Delete a worker instance"`
		Update *struct {
			WorkerId        int32   `arg:"--worker-id,required" help:"The ID of the worker to update"`
			StepId          *int32  `arg:"--step-id" help:"Step ID to assign the worker to"`
			WorkflowName    *string `arg:"--workflow-name" help:"Workflow name to resolve step (lowest step_id if --step-name omitted)"`
			StepName        *string `arg:"--step-name" help:"Step name to resolve step (requires --workflow-name)"`
			Concurrency     *int32  `arg:"--concurrency" help:"Update worker concurrency"`
			Prefetch        *int32  `arg:"--prefetch" help:"Update worker prefetch"`
			Permanent       bool    `arg:"--permanent" help:"Set worker as permanent"`
			NoPermanent     bool    `arg:"--no-permanent" help:"Unset permanent status"`
			RecyclableScope *string `arg:"--recyclable-scope" help:"Update recyclable scope"`
		} `arg:"subcommand:update" help:"Update worker settings"`
		Stats *struct {
			WorkerIds []int32 `arg:"--worker-id,separate,required" help:"Worker IDs to get stats for"`
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
			UserId   int32   `arg:"--id,required" help:"User ID to update"`
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
			UserId int32 `arg:"--id,required" help:"User ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a user"`
		ChangePassword *struct {
			Username string `arg:"--username,required" help:"Username for which password will be changed"`
		} `arg:"subcommand:change-password" help:"Change your password"`
	} `arg:"subcommand:user" help:"User management"`

	Recruiter *struct {
		List *struct {
			StepId int32 `arg:"--step-id" help:"Step ID to filter"`
		} `arg:"subcommand:list" help:"List all recruiters"`
		Create *struct {
			StepId          int32    `arg:"--step-id,required" help:"Step ID"`
			Rank            int32    `arg:"--rank" default:"1" help:"Recruiter rank"`
			Protofilter     string   `arg:"--filter,required" help:"A filter like 'cpu>=12:mem>=30' or 'flavor~Standard_D2s_%:region is default'"`
			Concurrency     *int32   `arg:"--concurrency" help:"Worker initial concurrency"`
			Prefetch        *int32   `arg:"--prefetch" help:"Worker initial prefetch"`
			PrefetchPercent *int32   `arg:"--prefetch-percent" help:"Worker initial prefetc (expressed as a %% of concurrency)"`
			CpuPerTask      *int32   `arg:"--cpu-per-task" help:"Adaptative concurrency: CPU required for 1 task"`
			MemoryPerTask   *float32 `arg:"--memory-per-task" help:"Adaptative concurrency: Memory (Gb) required for 1 task"`
			DiskPerTask     *float32 `arg:"--disk-per-task" help:"Adaptative concurrency: Disk space (Gb) required for 1 task"`
			ConcurrencyMax  *int32   `arg:"--concurrency" help:"Adaptative concurrency: Maximum concurrency per worker"`
			ConcurrencyMin  *int32   `arg:"--concurrency" help:"Adaptative concurrency: Minimal concurrency per worker"`
			MaxWorkers      *int32   `arg:"--max-workers" help:"Maximum number of workers"`
			Rounds          int      `arg:"--rounds" help:"Number of rounds"`
			Timeout         int      `arg:"--timeout" default:"10" help:"Timeout in seconds"`
		} `arg:"subcommand:create" help:"Create a new recruiter"`
		Delete *struct {
			StepId int32 `arg:"--step-id,required" help:"Step ID to delete"`
			Rank   int   `arg:"--rank,required" help:"Recruiter rank to delete"`
		} `arg:"subcommand:delete" help:"Delete a recruiter"`
	} `arg:"subcommand:recruiter" help:"Recruiter management"`

	// Workflow commands
	Workflow *struct {
		List *struct {
			NameLike string `arg:"--name-like" help:"Filter workflows by name"`
		} `arg:"subcommand:list" help:"List workflows"`
		Create *struct {
			Name           string `arg:"--name,required" help:"Workflow name"`
			RunStrategy    string `arg:"--run-strategy" help:"Run strategy (one letter B/T/D or Z, defaulting to B): \n\t(B)atch wise, e.g. workers do all tasks of a certain step (default)\n\t(T)hread wise, e.g. workers focus on going as far as possible in the workflow for each entry point\n\t(D)ebug\n\t(Z)suspended"`
			MaximumWorkers *int32 `arg:"--maximum-workers" help:"Maximum number of workers"`
		} `arg:"subcommand:create" help:"Create a new workflow"`
		Delete *struct {
			WorkflowId int32 `arg:"--id,required" help:"Workflow ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a workflow"`
	} `arg:"subcommand:workflow" help:"Manage workflows"`

	// Step commands
	Step *struct {
		List *struct {
			WorkflowId int32 `arg:"--workflow-id,required" help:"Workflow ID to list steps for"`
		} `arg:"subcommand:list" help:"List steps for a workflow"`
		Create *struct {
			WorkflowId   int32  `arg:"--workflow-id" help:"Workflow ID"`
			WorkflowName string `arg:"--workflow-name" help:"Workflow name (alternative to ID)"`
			Name         string `arg:"--name,required" help:"Step name"`
		} `arg:"subcommand:create" help:"Create a step"`
		Delete *struct {
			StepId int32 `arg:"--id,required" help:"Step ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a step"`
		Stats *struct {
			WorkflowId   *int32  `arg:"--workflow-id" help:"Workflow ID to get step stats for"`
			WorkflowName string  `arg:"--workflow-name" help:"Workflow name (alternative to ID, used to resolve ID)"`
			StepIds      []int32 `arg:"--step-id,separate" help:"Step IDs to get stats for (repeatable)"`
			TaskName     string  `arg:"--task-name" help:"Filter relevant step IDs by tasks whose name matches substring (case-insensitive, client-side)"`
			Totals       bool    `arg:"--totals" help:"Add totals line at the end of the stats"`
		} `arg:"subcommand:stats" help:"Show step statistics"`
	} `arg:"subcommand:step" help:"Manage steps"`

	// File commands
	File *struct {
		RemoteList *struct {
			URI     string `arg:"positional,required" help:"URI to list files from (supports glob patterns)"`
			Timeout int    `arg:"--timeout" default:"30" help:"Timeout for listing files (in seconds)"`
		} `arg:"subcommand:remote-list" help:"List remote files"`
		Copy *struct {
			Src string `arg:"positional,required" help:"Source URI"`
			Dst string `arg:"positional,required" help:"Destination URI"`
		} `arg:"subcommand:copy" help:"Copy remote files"`

		List *struct {
			Src string `arg:"positional,required" help:"Source URI"`
		} `arg:"subcommand:list" help:"List remote files"`
	} `arg:"subcommand:file" help:"Remote file operations"`

	// (Workflow) Template commands
	Template *struct {
		Upload *struct {
			Path  string `arg:"--path,required" help:"Path to the Python template script"`
			Force bool   `arg:"--force" help:"Overwrite existing template with same name/version"`
		} `arg:"subcommand:upload" help:"Upload a new workflow template"`

		Run *struct {
			TemplateId *int32  `arg:"--id" help:"ID of the template to run (either name or id is required)"`
			Name       *string `arg:"--name" help:"Name of the template to run (either name or id is required)"`
			Version    *string `arg:"--version" help:"Optional version (default to latest if omitted)"`
			ParamPairs *string `arg:"--param" help:"Comma-separated key=value pairs (e.g. a=1,b=2)"`
		} `arg:"subcommand:run" help:"Run a workflow template"`

		List *struct {
			Name    *string `arg:"--name" help:"Template name (can include wildcards like 'meta%')"`
			Version *string `arg:"--version" help:"Template version (can be 'latest' or a wildcard)"`
			Latest  bool    `arg:"--latest" help:"Show only latest version(s) for each template name"`
		} `arg:"subcommand:list" help:"List all uploaded workflow templates"`

		Detail *struct {
			TemplateId *int32  `arg:"--id" help:"Show detailed information for this template ID (either name or id is required)"`
			Name       *string `arg:"--name" help:"Name of the template to show (either name or id is required)"`
			Version    *string `arg:"--version" help:"Optional version (default to latest if omitted)"`
		} `arg:"subcommand:detail" help:"Show a template's param JSON and metadata"`
	} `arg:"subcommand:template" help:"Manage workflow templates"`

	// (Workflow) Template run commands
	Run *struct {
		List *struct {
			TemplateId *int32 `arg:"--template-id" help:"Filter runs by template ID"`
		} `arg:"subcommand:list" help:"List template runs"`

		Delete *struct {
			RunId int32 `arg:"--id,required" help:"ID of the template run to delete"`
		} `arg:"subcommand:delete" help:"Delete a template run"`
	} `arg:"subcommand:run" help:"Manage template runs"`

	// Login commands
	Login *struct {
		User     *string `arg:"--user" help:"Username to log in"`
		Password *string `arg:"--password" help:"Password to log in"`
	} `arg:"subcommand:login" help:"Login and provide a token, use with export SCITQ_TOKENs=$(scitq login)"`

	// Certificate command
	Cert *struct{} `arg:"subcommand:cert" help:"Print server TLS certificate (PEM)"`

	// HashPassword command
	HashPassword *struct {
		Password string `arg:"positional,required" help:"Password to hash"`
	} `arg:"subcommand:hashpassword" help:"Hash a password using bcrypt (useful for config files)"`

	// Worker event commands
	WorkerEvent *struct {
		List *struct {
			Limit    int    `arg:"--limit" default:"20" help:"Max number of events to show"`
			Level    string `arg:"--level" help:"Filter by level D/I/W/E as D:Debug/I:Info/W:Warning/E:Error"`
			Class    string `arg:"--class" help:"Filter by event_class"`
			WorkerId int32  `arg:"--worker-id" help:"Filter by worker id"`
		} `arg:"subcommand:list" help:"List worker events"`
		Delete *struct {
			Id int32 `arg:"--id,required" help:"Worker event ID to delete"`
		} `arg:"subcommand:delete" help:"Delete a worker event by ID"`
		Prune *struct {
			OlderThan string `arg:"--older-than" help:"Age to prune, e.g. 7d, 12h, 30m (required unless --dry-run)"`
			Level     string `arg:"--level" help:"Optional level filter D/I/W/E as D:Debug/I:Info/W:Warning/E:Error"`
			Class     string `arg:"--class" help:"Optional event_class filter"`
			WorkerId  int32  `arg:"--worker-id" help:"Optional worker id filter"`
			DryRun    bool   `arg:"--dry-run" help:"Only count matched rows; do not delete"`
		} `arg:"subcommand:prune" help:"Prune worker events by age and optional filters"`
	} `arg:"subcommand:worker-event" help:"Worker event logs"`
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
		Command:    c.Attr.Task.Create.Command,
		Container:  c.Attr.Task.Create.Container,
		Shell:      c.Attr.Task.Create.Shell,
		Input:      c.Attr.Task.Create.Input,
		Resource:   c.Attr.Task.Create.Resource,
		Output:     &c.Attr.Task.Create.Output,
		StepId:     c.Attr.Task.Create.StepId,
		TaskName:   c.Attr.Task.Create.Name,
		Dependency: c.Attr.Task.Create.Dependencies,
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
	// Status filter
	if c.Attr.Task.List.Status != "" {
		req.StatusFilter = &c.Attr.Task.List.Status
	}
	// ShowHidden filter
	if c.Attr.Task.List.ShowHidden {
		v := true
		req.ShowHidden = &v
	}
	// WorkerId filter
	if c.Attr.Task.List.WorkerId != 0 {
		req.WorkerIdFilter = &c.Attr.Task.List.WorkerId
	}
	// WorkflowId filter
	if c.Attr.Task.List.WorkflowId != 0 {
		req.WorkflowIdFilter = &c.Attr.Task.List.WorkflowId
	}
	// StepId filter
	if c.Attr.Task.List.StepId != 0 {
		req.StepIdFilter = &c.Attr.Task.List.StepId
	}
	// Command filter
	if c.Attr.Task.List.Command != "" {
		req.CommandFilter = &c.Attr.Task.List.Command
	}
	// Limit filter
	if c.Attr.Task.List.Limit > 0 {
		req.Limit = &c.Attr.Task.List.Limit
	}
	// Offset filter
	if c.Attr.Task.List.Offset > 0 {
		req.Offset = &c.Attr.Task.List.Offset
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
		retries := ""
		if task.RetryCount > 0 {
			retries = fmt.Sprintf(" | Retries: %d", task.RetryCount)
		}
		hidden := ""
		if c.Attr.Task.List.ShowHidden {
			hidden = fmt.Sprintf(" | Hidden: %t", task.Hidden)
		}
		fmt.Printf("🆔 ID: %d%s | Command: %s | Container: %s | Status: %s%s%s\n",
			task.TaskId, name, task.Command, task.Container, task.Status, retries, hidden)
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

// TaskRetry retries a failed task.
func (c *CLI) TaskRetry() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.RetryTaskRequest{
		TaskId: c.Attr.Task.Retry.ID,
	}
	if c.Attr.Task.Retry.Retry != nil {
		req.Retry = c.Attr.Task.Retry.Retry
	}

	res, err := c.QC.Client.RetryTask(ctx, req)
	if err != nil {
		return fmt.Errorf("error retrying task: %w", err)
	}

	fmt.Printf("🔁 Retried task %d → new task ID %d\n", c.Attr.Task.Retry.ID, res.TaskId)
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
func (c *CLI) FlavorList(limit int32, filter string) error {
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

	// Try to parse flavor as ID first, then fall back to name
	flavorID, err := strconv.Atoi(c.Attr.Worker.Deploy.Flavor)
	var filter string

	if err == nil {
		// Input is a valid integer, search by flavor ID
		filter = fmt.Sprintf("provider=%s:flavor_id=%d:%s", c.Attr.Worker.Deploy.Provider, flavorID, regionFilter)
	} else {
		// Input is not a valid integer, search by flavor name
		filter = fmt.Sprintf("provider=%s:flavor_name=%s:%s", c.Attr.Worker.Deploy.Provider, c.Attr.Worker.Deploy.Flavor, regionFilter)
	}

	req := &pb.ListFlavorsRequest{
		Limit:  1,
		Filter: filter,
	}
	res, err := c.QC.Client.ListFlavors(ctx, req)
	if err != nil {
		return fmt.Errorf("error fetching flavor: %w", err)
	}
	if len(res.Flavors) != 1 {
		// Provide diagnostic help when no flavors are found
		if len(res.Flavors) == 0 {
			// Try to find flavors without provider/region constraints to help diagnose the issue
			diagReq := &pb.ListFlavorsRequest{
				Limit:  10,
				Filter: fmt.Sprintf("flavor_name=%s", c.Attr.Worker.Deploy.Flavor),
			}
			diagRes, diagErr := c.QC.Client.ListFlavors(ctx, diagReq)
			if diagErr == nil && len(diagRes.Flavors) > 0 {
				// Flavor exists but not with the specified provider/region
				var availableProviders []string
				for _, f := range diagRes.Flavors {
					availableProviders = append(availableProviders, f.Provider)
				}
				return fmt.Errorf("flavor '%s' not found for provider '%s' and region '%s'. Available providers for this flavor: %v. Use 'scitq flavor list --filter flavor_name=%s' to see details",
					c.Attr.Worker.Deploy.Flavor, c.Attr.Worker.Deploy.Provider, c.Attr.Worker.Deploy.Region, availableProviders, c.Attr.Worker.Deploy.Flavor)
			}
			return fmt.Errorf("no flavors found matching provider='%s', flavor='%s', region='%s'. Please check your provider, flavor name, and region. Use 'scitq flavor list' to see available flavors",
				c.Attr.Worker.Deploy.Provider, c.Attr.Worker.Deploy.Flavor, c.Attr.Worker.Deploy.Region)
		}
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
		Number:      int32(c.Attr.Worker.Deploy.Count),
		StepId:      &c.Attr.Worker.Deploy.StepId,
		Concurrency: int32(c.Attr.Worker.Deploy.Concurrency),
		Prefetch:    int32(c.Attr.Worker.Deploy.Prefetch),
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
	res, err := c.QC.Client.DeleteWorker(ctx, &pb.WorkerDeletion{WorkerId: int32(c.Attr.Worker.Delete.WorkerId)})
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

// WorkerUpdate updates worker settings (user-scoped).
func (c *CLI) WorkerUpdate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.WorkerUpdateRequest{
		WorkerId: c.Attr.Worker.Update.WorkerId,
	}

	if c.Attr.Worker.Update.StepId != nil && (c.Attr.Worker.Update.WorkflowName != nil || c.Attr.Worker.Update.StepName != nil) {
		return fmt.Errorf("provide either --step-id or --workflow-name/--step-name, not both")
	}
	if c.Attr.Worker.Update.StepName != nil && c.Attr.Worker.Update.WorkflowName == nil {
		return fmt.Errorf("--workflow-name is required when --step-name is provided")
	}
	if c.Attr.Worker.Update.Permanent && c.Attr.Worker.Update.NoPermanent {
		return fmt.Errorf("provide only one of --permanent or --no-permanent")
	}

	if c.Attr.Worker.Update.StepId != nil {
		req.StepId = c.Attr.Worker.Update.StepId
	}
	if c.Attr.Worker.Update.WorkflowName != nil {
		req.WorkflowName = c.Attr.Worker.Update.WorkflowName
	}
	if c.Attr.Worker.Update.StepName != nil {
		req.StepName = c.Attr.Worker.Update.StepName
	}
	if c.Attr.Worker.Update.Concurrency != nil {
		req.Concurrency = c.Attr.Worker.Update.Concurrency
	}
	if c.Attr.Worker.Update.Prefetch != nil {
		req.Prefetch = c.Attr.Worker.Update.Prefetch
	}
	if c.Attr.Worker.Update.Permanent {
		val := true
		req.IsPermanent = &val
	} else if c.Attr.Worker.Update.NoPermanent {
		val := false
		req.IsPermanent = &val
	}
	if c.Attr.Worker.Update.RecyclableScope != nil {
		req.RecyclableScope = c.Attr.Worker.Update.RecyclableScope
	}

	_, err := c.QC.Client.UserUpdateWorker(ctx, req)
	if err != nil {
		return fmt.Errorf("error updating worker: %w", err)
	}

	fmt.Printf("✅ Worker %d updated\n", c.Attr.Worker.Update.WorkerId)
	return nil
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

func DeleteUser(client pb.TaskQueueClient, userId int32) error {
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
		var descLine string
		if r.Concurrency != nil {
			descLine = fmt.Sprintf("Step %d | Rank %d | Filter %s | Concurrency=%d",
				r.StepId, r.Rank, r.Protofilter, *r.Concurrency)
		} else {
			descLine = fmt.Sprintf("Step %d | Rank %d | Filter %s | Adaptative concurrency",
				r.StepId, r.Rank, r.Protofilter)
			if r.CpuPerTask != nil {
				descLine += fmt.Sprintf(" CPU:%d/task", *r.CpuPerTask)
			}
			if r.MemoryPerTask != nil {
				descLine += fmt.Sprintf(" Mem:%.1fGb/task", *r.MemoryPerTask)
			}
			if r.DiskPerTask != nil {
				descLine += fmt.Sprintf(" Disk:%.1fGb/task", *r.DiskPerTask)
			}
			if r.ConcurrencyMax != nil && r.ConcurrencyMin == nil {
				descLine += fmt.Sprintf(" (Max:%d)", *r.ConcurrencyMax)
			}
			if r.ConcurrencyMax == nil && r.ConcurrencyMin != nil {
				descLine += fmt.Sprintf(" (Min:%d)", *r.ConcurrencyMin)
			}
			if r.ConcurrencyMax != nil && r.ConcurrencyMin != nil {
				descLine += fmt.Sprintf(" (Min-Max:%d-%d)", *&r.ConcurrencyMin, *r.ConcurrencyMax)
			}
		}

		var prefetch string
		if r.Prefetch != nil {
			prefetch = fmt.Sprintf("%d", *r.Prefetch)
		} else {
			if r.PrefetchPercent != nil {
				prefetch = fmt.Sprintf("%d%%", *r.PrefetchPercent)
			}
		}

		descLine += fmt.Sprintf(" Prefetch=%s Rounds=%d Timeout=%d",
			prefetch, r.Rounds, r.Timeout)

		if r.MaxWorkers != nil {
			descLine += fmt.Sprintf(" Maximum Workers=%d\n", *r.MaxWorkers)
		} else {
			descLine += fmt.Sprintf(" Maximum Workers unlimited\n")
		}
		fmt.Print(descLine)
	}
	return nil
}

func (c *CLI) RecruiterCreate() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.Recruiter{
		StepId:          c.Attr.Recruiter.Create.StepId,
		Rank:            c.Attr.Recruiter.Create.Rank,
		Protofilter:     c.Attr.Recruiter.Create.Protofilter,
		Concurrency:     c.Attr.Recruiter.Create.Concurrency,
		Prefetch:        c.Attr.Recruiter.Create.Prefetch,
		PrefetchPercent: c.Attr.Recruiter.Create.PrefetchPercent,
		CpuPerTask:      c.Attr.Recruiter.Create.CpuPerTask,
		MemoryPerTask:   c.Attr.Recruiter.Create.MemoryPerTask,
		DiskPerTask:     c.Attr.Recruiter.Create.DiskPerTask,
		ConcurrencyMin:  c.Attr.Recruiter.Create.ConcurrencyMin,
		ConcurrencyMax:  c.Attr.Recruiter.Create.ConcurrencyMax,
		MaxWorkers:      c.Attr.Recruiter.Create.MaxWorkers,
		Rounds:          int32(c.Attr.Recruiter.Create.Rounds),
		Timeout:         int32(c.Attr.Recruiter.Create.Timeout),
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
		Rank:   int32(c.Attr.Recruiter.Delete.Rank),
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Attr.File.RemoteList.Timeout)*time.Second)
	defer cancel()

	req := &pb.FetchListRequest{
		Uri: c.Attr.File.RemoteList.URI,
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

	// If --id is not provided, but --name is, resolve the template ID by name/version.
	if c.Attr.Template.Detail.TemplateId == nil && c.Attr.Template.Detail.Name != nil {
		filter := &pb.TemplateFilter{Name: c.Attr.Template.Detail.Name}
		if c.Attr.Template.Detail.Version != nil {
			filter.Version = c.Attr.Template.Detail.Version
		} else {
			latest := "latest"
			filter.Version = &latest
		}
		res, err := c.QC.Client.ListTemplates(ctx, filter)
		if err != nil {
			return fmt.Errorf("failed to resolve template by name/version: %w", err)
		}
		if len(res.Templates) == 0 {
			return fmt.Errorf("no template found with name %q and version %q", *filter.Name, *filter.Version)
		}
		c.Attr.Template.Detail.TemplateId = &res.Templates[0].WorkflowTemplateId
	}
	if c.Attr.Template.Detail.TemplateId == nil {
		return fmt.Errorf("template ID or --name is required")
	}

	req := &pb.TemplateFilter{WorkflowTemplateId: c.Attr.Template.Detail.TemplateId}
	res, err := c.QC.Client.ListTemplates(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to query template: %w", err)
	}
	if len(res.Templates) == 0 {
		return fmt.Errorf("no template found with ID %d", *c.Attr.Template.Detail.TemplateId)
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
	// Long-running DSL scripts may take many minutes; use a large static timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Resolve TemplateId by Name/Version if --name is provided
	if c.Attr.Template.Run.Name != nil {
		filter := &pb.TemplateFilter{Name: c.Attr.Template.Run.Name}
		if c.Attr.Template.Run.Version != nil {
			filter.Version = c.Attr.Template.Run.Version
		} else {
			latest := "latest"
			filter.Version = &latest
		}
		res, err := c.QC.Client.ListTemplates(ctx, filter)
		if err != nil {
			return fmt.Errorf("failed to resolve template by name/version: %w", err)
		}
		if len(res.Templates) == 0 {
			return fmt.Errorf("no template found with name %q and version %q", *filter.Name, *filter.Version)
		}
		c.Attr.Template.Run.TemplateId = &res.Templates[0].WorkflowTemplateId
	}
	if c.Attr.Template.Run.TemplateId == nil {
		return fmt.Errorf("template ID or --name is required")
	}
	var paramJSON string
	var err error

	if c.Attr.Template.Run.ParamPairs != nil {
		paramJSON, err = parseCommaSeparatedParams(*c.Attr.Template.Run.ParamPairs)
		if err != nil {
			return err
		}
	}

	req := &pb.RunTemplateRequest{
		WorkflowTemplateId: *c.Attr.Template.Run.TemplateId,
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
	if res.ErrorMessage != nil {
		errMsg := *res.ErrorMessage
		fmt.Printf("⚠️  Warning: %s\n", errMsg)
	}
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

func (c *CLI) WorkerEventList() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	f := &pb.WorkerEventFilter{}
	if c.Attr.WorkerEvent.List.WorkerId != 0 {
		f.WorkerId = &c.Attr.WorkerEvent.List.WorkerId
	}
	if c.Attr.WorkerEvent.List.Level != "" {
		f.Level = &c.Attr.WorkerEvent.List.Level
	}
	if c.Attr.WorkerEvent.List.Class != "" {
		f.Class = &c.Attr.WorkerEvent.List.Class
	}
	if c.Attr.WorkerEvent.List.Limit > 0 {
		l := int32(c.Attr.WorkerEvent.List.Limit)
		f.Limit = &l
	}

	res, err := c.QC.Client.ListWorkerEvents(ctx, f)
	if err != nil {
		return fmt.Errorf("error fetching worker events: %w", err)
	}

	if len(res.Events) == 0 {
		fmt.Println("⚠️ No worker events found.")
		return nil
	}

	fmt.Println("📋 Worker Events:")
	for _, e := range res.Events {
		wid := "-"
		if e.WorkerId != nil {
			wid = fmt.Sprintf("%d", *e.WorkerId)
		}
		fmt.Printf("🆔 %d | %s | WID:%s Name:%s | %s/%s | %s\n",
			e.EventId, e.CreatedAt, wid, e.WorkerName,
			e.Level, e.EventClass, e.Message)
		if e.DetailsJson != "" {
			fmt.Printf("   Details: %s\n", e.DetailsJson)
		}
	}
	return nil
}

func (c *CLI) WorkerEventDelete() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	_, err := c.QC.Client.DeleteWorkerEvent(ctx, &pb.WorkerEventId{EventId: c.Attr.WorkerEvent.Delete.Id})
	if err != nil {
		return fmt.Errorf("failed to delete worker event %d: %w", c.Attr.WorkerEvent.Delete.Id, err)
	}
	fmt.Printf("🗑️ Deleted worker event %d\n", c.Attr.WorkerEvent.Delete.Id)
	return nil
}

func parseAgeToRFC3339(older string) (string, error) {
	// supports Ns, Nm, Nh, Nd, Nw
	if older == "" {
		return "", fmt.Errorf("missing --older-than")
	}
	n := len(older)
	if n < 2 {
		return "", fmt.Errorf("invalid --older-than: %q", older)
	}
	unit := older[n-1]
	val := older[:n-1]
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || f <= 0 {
		return "", fmt.Errorf("invalid --older-than number: %q", val)
	}
	var dur time.Duration
	switch unit {
	case 's':
		dur = time.Duration(f * float64(time.Second))
	case 'm':
		dur = time.Duration(f * float64(time.Minute))
	case 'h':
		dur = time.Duration(f * float64(time.Hour))
	case 'd':
		dur = time.Duration(f * float64(24*time.Hour))
	case 'w':
		dur = time.Duration(f * float64(7*24*time.Hour))
	default:
		return "", fmt.Errorf("invalid --older-than unit (use s,m,h,d,w)")
	}
	cutoff := time.Now().UTC().Add(-dur)
	return cutoff.Format(time.RFC3339), nil
}

func (c *CLI) WorkerEventPrune() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	req := &pb.WorkerEventPruneFilter{}

	// older-than → RFC3339 'before'
	if c.Attr.WorkerEvent.Prune.OlderThan != "" {
		ts, err := parseAgeToRFC3339(c.Attr.WorkerEvent.Prune.OlderThan)
		if err != nil {
			return err
		}
		req.Before = &ts
	} else if !c.Attr.WorkerEvent.Prune.DryRun {
		return fmt.Errorf("--older-than is required unless --dry-run")
	}

	if v := strings.TrimSpace(c.Attr.WorkerEvent.Prune.Level); v != "" {
		req.Level = &v
	}
	if v := strings.TrimSpace(c.Attr.WorkerEvent.Prune.Class); v != "" {
		req.Class = &v
	}
	if id := c.Attr.WorkerEvent.Prune.WorkerId; id != 0 {
		req.WorkerId = &id
	}
	req.DryRun = c.Attr.WorkerEvent.Prune.DryRun

	res, err := c.QC.Client.PruneWorkerEvents(ctx, req)
	if err != nil {
		return fmt.Errorf("prune failed: %w", err)
	}
	if req.DryRun {
		fmt.Printf("🔎 Dry-run: %d events would be deleted.\n", res.Matched)
	} else {
		fmt.Printf("🧹 Pruned %d events.\n", res.Deleted)
	}
	return nil
}

// containsIgnoreCase returns true if needle is a substring of hay, case-insensitive.
func containsIgnoreCase(hay, needle string) bool {
	hay = strings.ToLower(hay)
	needle = strings.ToLower(needle)
	return strings.Contains(hay, needle)
}

// int32Set returns a map[int32]struct{} from a slice (for set operations).
func int32Set(slice []int32) map[int32]struct{} {
	s := make(map[int32]struct{}, len(slice))
	for _, v := range slice {
		s[v] = struct{}{}
	}
	return s
}

// int32SetToSlice returns a sorted slice from a set.
func int32SetToSlice(set map[int32]struct{}) []int32 {
	res := make([]int32, 0, len(set))
	for v := range set {
		res = append(res, v)
	}
	// Optionally sort for stable output
	if len(res) > 1 {
		// Use sort
		ints := make([]int, len(res))
		for i, v := range res {
			ints[i] = int(v)
		}
		// sort
		for i := 0; i < len(ints); i++ {
			for j := i + 1; j < len(ints); j++ {
				if ints[i] > ints[j] {
					ints[i], ints[j] = ints[j], ints[i]
				}
			}
		}
		for i := range ints {
			res[i] = int32(ints[i])
		}
	}
	return res
}

// int32SetIntersect returns intersection of two sets.
func int32SetIntersect(a, b map[int32]struct{}) map[int32]struct{} {
	out := make(map[int32]struct{})
	for v := range a {
		if _, ok := b[v]; ok {
			out[v] = struct{}{}
		}
	}
	return out
}

// int32SetUnion returns the union of two sets.
func int32SetUnion(a, b map[int32]struct{}) map[int32]struct{} {
	out := make(map[int32]struct{})
	for v := range a {
		out[v] = struct{}{}
	}
	for v := range b {
		out[v] = struct{}{}
	}
	return out
}

// StepStats implements the stats subcommand for steps.
func (c *CLI) StepStats() error {
	ctx, cancel := c.WithTimeout()
	defer cancel()

	attr := c.Attr.Step.Stats
	var workflowId *int32
	// Step 1: Resolve workflowId from WorkflowName if provided.
	if attr.WorkflowName != "" {
		// List workflows and find exact match
		req := &pb.WorkflowFilter{}
		res, err := c.QC.Client.ListWorkflows(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to list workflows: %w", err)
		}
		var found *pb.Workflow
		for _, w := range res.Workflows {
			if w.Name == attr.WorkflowName {
				found = w
				break
			}
		}
		if found == nil {
			return fmt.Errorf("no workflow found with exact name: %s", attr.WorkflowName)
		}
		workflowId = &found.WorkflowId
	} else if attr.WorkflowId != nil && *attr.WorkflowId != 0 {
		workflowId = attr.WorkflowId
	}

	// Step 2: Build stepIds set
	var stepIDsFromTaskName map[int32]struct{}
	if attr.TaskName != "" {
		// List tasks, filter by TaskName substring and workflowId if set
		v := true
		req := &pb.ListTasksRequest{ShowHidden: &v}
		res, err := c.QC.Client.ListTasks(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to list tasks: %w", err)
		}
		stepIDsFromTaskName = make(map[int32]struct{})
		for _, t := range res.Tasks {
			// Filter by workflowId if set
			if workflowId != nil && t.WorkflowId != nil && *t.WorkflowId != *workflowId {
				continue
			}
			if t.TaskName != nil && containsIgnoreCase(*t.TaskName, attr.TaskName) {
				if t.StepId != nil {
					stepIDsFromTaskName[*t.StepId] = struct{}{}
				}
			}
		}
	}
	userStepIDs := int32Set(attr.StepIds)
	var stepIDs map[int32]struct{}
	if attr.TaskName != "" && len(attr.StepIds) > 0 {
		// Intersection
		stepIDs = int32SetIntersect(stepIDsFromTaskName, userStepIDs)
	} else if attr.TaskName != "" {
		stepIDs = stepIDsFromTaskName
	} else if len(attr.StepIds) > 0 {
		stepIDs = userStepIDs
	} else {
		// No step IDs specified, fetch all steps for workflow if workflowId is set
		if workflowId != nil {
			stepsRes, err := c.QC.Client.ListSteps(ctx, &pb.StepFilter{WorkflowId: *workflowId})
			if err != nil {
				return fmt.Errorf("failed to list steps: %w", err)
			}
			stepIDs = make(map[int32]struct{})
			for _, s := range stepsRes.Steps {
				stepIDs[s.StepId] = struct{}{}
			}
		} else {
			return fmt.Errorf("must specify at least one of --workflow-id/--workflow-name, --step-id, or --task-name")
		}
	}
	finalStepIds := int32SetToSlice(stepIDs)
	if len(finalStepIds) == 0 {
		fmt.Println("No step IDs found for the given filters.")
		return nil
	}

	// Step 3: Build StepStatsRequest
	req := &pb.StepStatsRequest{
		StepIds: finalStepIds,
	}
	if workflowId != nil {
		req.WorkflowId = workflowId
	}

	// Step 4: Call GetStepStats and print human-friendly table
	res, err := c.QC.Client.GetStepStats(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get step stats: %w", err)
	}
	if len(res.Stats) == 0 {
		fmt.Println("No stats found for the selected steps.")
		return nil
	}
	fmt.Printf("%-8s %-24s %-8s %-8s %-8s %-8s %-8s %-8s %-8s %-8s | %-22s %-22s %-22s %-22s %-22s\n",
		"StepID", "Name", "Total", "Wait", "Pend", "Acc.", "Runn", "Upl.", "Succ", "Fail", "SuccRun", "FailRun", "CurrRun", "Download", "Upload")
	fmt.Println(strings.Repeat("-", 222))
	var totalSuccess, totalFailed, totalRunning, totalDownload, totalUpload float32
	var startTime, endTime *int64

	for _, stat := range res.Stats {
		name := stat.StepName
		if name == "" {
			name = fmt.Sprintf("Step id=%d", stat.StepId)
		}
		fmt.Printf("%-8d %-24s %-8d %-8d %-8d %-8d %-8d %-8d %-8d %-3d(%-3d) | %-22s %-22s %-22s %-22s %-22s\n",
			stat.StepId,
			name,
			stat.TotalTasks,
			stat.WaitingTasks,
			stat.PendingTasks,
			stat.AcceptedTasks,
			stat.RunningTasks,
			stat.UploadingTasks,
			stat.SuccessfulTasks,
			stat.ReallyFailedTasks,
			stat.FailedTasks,
			formatAccum(stat.SuccessRun),
			formatAccum(stat.FailedRun),
			formatAccum(stat.RunningRun),
			formatAccum(stat.Download),
			formatAccum(stat.Upload),
		)
		if attr.Totals {
			// Sum the accumulator sums
			if stat.SuccessRun != nil {
				totalSuccess += stat.SuccessRun.GetSum()
			}
			if stat.FailedRun != nil {
				totalFailed += stat.FailedRun.GetSum()
			}
			if stat.RunningRun != nil {
				totalRunning += stat.RunningRun.GetSum()
			}
			if stat.Download != nil {
				totalDownload += stat.Download.GetSum()
			}
			if stat.Upload != nil {
				totalUpload += stat.Upload.GetSum()
			}
			// handle start/end time (int32 pointers)
			if endTime == nil || (stat.EndTime != nil && *stat.EndTime > *endTime) {
				endTime = stat.EndTime
			}
			if stat.StartTime != nil && (startTime == nil || *stat.StartTime < *startTime) {
				startTime = stat.StartTime
			}
		}
	}
	if attr.Totals {
		fmt.Println(strings.Repeat("-", 222))
		elapsedTime := float32(0)
		if startTime != nil && endTime != nil && *endTime >= *startTime {
			elapsedTime = float32(*endTime - *startTime)
		}
		fmt.Printf("Elapsed time: %-10s %-66sCumulated times: %-22s %-22s %-22s %-22s %-22s\n", formatDuration(elapsedTime), "",
			formatDuration(totalSuccess),
			formatDuration(totalFailed),
			formatDuration(totalRunning),
			formatDuration(totalDownload),
			formatDuration(totalUpload),
		)
	}

	return nil
}

// formatDuration prints a duration in a human-friendly way:
// - "0" if durSeconds == 0
// - "<1s" if 0 < durSeconds < 1
// - "Xs" if under 60 seconds
// - "YmZs" if under 3600 seconds (minutes + leftover seconds)
// - "XhYm" if 3600 seconds or more (hours + leftover minutes, omit seconds)
func formatDuration(durSeconds float32) string {
	if durSeconds == 0 {
		return "0"
	}
	if durSeconds > 0 && durSeconds < 1 {
		return "<1s"
	}
	if durSeconds < 60 {
		// Xs
		return fmt.Sprintf("%ds", int(durSeconds+0.5))
	}
	if durSeconds < 3600 {
		// YmZs
		min := int(durSeconds) / 60
		sec := int(durSeconds) % 60
		return fmt.Sprintf("%dm%ds", min, sec)
	}
	// XhYm
	h := int(durSeconds) / 3600
	m := (int(durSeconds) % 3600) / 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// formatAccum renders avg [min-max] from an *pb.Accum.
func formatAccum(a *pb.Accum) string {
	if a == nil || a.Count == 0 {
		return "-"
	}
	avg := a.Sum / float32(a.Count)
	return fmt.Sprintf("%s [%s-%s]",
		formatDuration(avg),
		formatDuration(a.Min),
		formatDuration(a.Max),
	)
}

// ConfigImportRclone loads an rclone.conf and prints it as YAML fragment.
func (c *CLI) ConfigImportRclone() error {
	path := c.Attr.Config.ImportRclone.Path
	// Expand ~ if present
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home + path[1:]
		}
	}
	cfg, err := ini.Load(path)
	if err != nil {
		return fmt.Errorf("failed to load rclone.conf: %w", err)
	}
	remotes := make(map[string]map[string]string)
	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == ini.DefaultSection {
			continue
		}
		keys := section.KeysHash()
		remotes[name] = keys
	}
	yamlObj := map[string]interface{}{
		"rclone": remotes,
	}
	out, err := yaml.Marshal(yamlObj)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	fmt.Println("# Paste the following under the 'rclone:' section in /etc/scitq.yaml")
	fmt.Print(string(out))
	return nil
}

func (c *CLI) Copy() error {
	// 1. Get rclone config from server via gRPC
	ctx, cancel := c.WithTimeout()
	defer cancel()

	remotes, err := c.QC.Client.GetRcloneConfig(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to fetch rclone config: %w", err)
	}

	// 3. Use fetch.NewOperation for copy
	op, err := fetch.NewOperation(remotes, c.Attr.File.Copy.Src, c.Attr.File.Copy.Dst)
	if err != nil {
		return fmt.Errorf("failed to create fetch operation: %w", err)
	}
	defer fetch.CleanOperation(op)

	err = op.Copy()
	if err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	fmt.Printf("Copy successful: %s → %s\n", c.Attr.File.Copy.Src, c.Attr.File.Copy.Dst)
	return nil
}

func (c *CLI) List() error {
	// 1. Get rclone config from server via gRPC
	ctx, cancel := c.WithTimeout()
	defer cancel()

	remotes, err := c.QC.Client.GetRcloneConfig(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("failed to fetch rclone config: %w", err)
	}

	// 3. Use fetch.NewOperation for copy
	op, err := fetch.NewOperation(remotes, c.Attr.File.List.Src, "")
	if err != nil {
		return fmt.Errorf("failed to create fetch operation: %w", err)
	}
	defer fetch.CleanOperation(op)

	files, err := op.List()
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}
	for _, file := range files {
		fileName := op.SrcBase() + file.String()
		if fetch.IsDir(file) {
			fileName += "/"
		}
		fmt.Println(fileName)
	}
	return nil
}

func Run(c CLI) error {
	arg.MustParse(&c.Attr)

	switch {
	case c.Attr.Login != nil:
		fmt.Print(createToken(c.Attr.Server, c.Attr.Login.User, c.Attr.Login.Password))
		return nil
	case c.Attr.Cert != nil:
		fmt.Print(fetchCertificate(c.Attr.Server))
		return nil
	case c.Attr.HashPassword != nil:
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(c.Attr.HashPassword.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("Failed to hash password: %v", err)
		}
		fmt.Printf("Hashed password: %s\n", string(hashedPassword))
		return nil
	case c.Attr.Config != nil && c.Attr.Config.ImportRclone != nil:
		return c.ConfigImportRclone()
	}
	// Establish gRPC connection
	// Ensure token exists (interactive if needed)
	token := c.Attr.Token
	if token == "" {
		var err error
		token, err = getToken()

		if err != nil {
			log.Fatalf("Could not create client: %v", err)
		}
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
		case c.Attr.Task.Retry != nil:
			err = c.TaskRetry()
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
		case c.Attr.Worker.Update != nil:
			err = c.WorkerUpdate()
		case c.Attr.Worker.Stats != nil:
			err = c.WorkerStats()
		}
	case c.Attr.Flavor != nil:
		switch {
		case c.Attr.Flavor.List != nil:
			err = c.FlavorList(int32(c.Attr.Flavor.List.Limit), c.Attr.Flavor.List.Filter)
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
		case c.Attr.Step.Stats != nil:
			err = c.StepStats()
		}
	case c.Attr.File != nil:
		switch {
		case c.Attr.File.RemoteList != nil:
			err = c.FileList()
		case c.Attr.File.Copy != nil:
			err = c.Copy()
		case c.Attr.File.List != nil:
			err = c.List()
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
	case c.Attr.WorkerEvent != nil:
		switch {
		case c.Attr.WorkerEvent.List != nil:
			err = c.WorkerEventList()
		case c.Attr.WorkerEvent.Delete != nil:
			err = c.WorkerEventDelete()
		case c.Attr.WorkerEvent.Prune != nil:
			err = c.WorkerEventPrune()
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}

	return err
}
