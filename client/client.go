package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/google/shlex"

	"github.com/scitq/scitq/client/event"
	"github.com/scitq/scitq/client/install"
	"github.com/scitq/scitq/client/workerstats"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/internal/version"
	"github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/utils"

	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

const fetchTaskErrorThreshold = 5

// the client is divided in several loops to accomplish its tasks:
// - the main loop (essentially this file)
//
//

// LiveCaps holds the worker's resource caps in a form that can be
// read by per-task goroutines (excuterThread) and updated from the
// fetchTasks loop on every ping. Atomic so neither side needs to take
// a lock on the hot path.
//
// Zero (the default) means "autodetect": fall back to runtime.NumCPU()
// / host memory at read time, matching the static-flag behaviour.
type LiveCaps struct {
	cpu atomic.Int32
	mem atomic.Uint32 // float32 stored as bits, since atomic.Float32 doesn't exist
}

func NewLiveCaps(cpu int32, memGiB float32) *LiveCaps {
	lc := &LiveCaps{}
	lc.SetCPU(cpu)
	lc.SetMem(memGiB)
	return lc
}

func (l *LiveCaps) CPU() int32      { return l.cpu.Load() }
func (l *LiveCaps) Mem() float32    { return math.Float32frombits(l.mem.Load()) }
func (l *LiveCaps) SetCPU(v int32)  { l.cpu.Store(v) }
func (l *LiveCaps) SetMem(v float32) { l.mem.Store(math.Float32bits(v)) }

// WorkerConfig holds worker settings from CLI args.
type WorkerConfig struct {
	WorkerId    int32
	ServerAddr  string
	Concurrency int32
	Name        string
	Store       string
	Token       string
	IsPermanent bool
	Provider    *string
	Region      *string
	NoBare      bool // reject bare tasks
	// MaxCPU / MaxMem cap what this worker advertises to scitq, for
	// shared nodes that should only contribute part of their hardware.
	// Zero means "autodetect" (runtime.NumCPU / total memory). Validated
	// against the real machine in registerSpecs — a value larger than
	// the hardware is rejected, since the server uses these numbers
	// directly for flavor matching and $CPU/$MEM injection.
	MaxCPU int32
	MaxMem float32 // GiB
}

var lostTrackSeen sync.Map   // map[int32]time.Time
var orphanSeen sync.Map      // map[int32]time.Time — tasks client thinks active but server has dropped
var bareProcesses sync.Map   // map[int32]*os.Process — for signaling bare tasks

// taskHooks lets the signal handler and the per-task watchdog reach into a
// running executeTask invocation to forcibly unstick it. Both the watchdog
// (container died, executeTask still spinning) and the kill-failure recovery
// (operator sent K, docker kill returned "No such container") funnel into
// the same recoverTask() helper, which cancels the gRPC log-stream context
// (unblocking sendLogs goroutines stuck in stream.Send) and kills the
// docker run client process (unblocking cmd.Wait when the docker daemon
// itself is unresponsive). After both unblock, the normal executeTask
// post-Wait path runs and reports the task as F to the server.
type taskHooks struct {
	cancel        context.CancelFunc // cancels the log-stream context
	cmd           *exec.Cmd          // the docker run / bare process command
	containerName string             // empty for bare tasks
	isBare        bool
	recovered     atomic.Bool // true once recovery has been triggered, to keep it idempotent

	// capturedExit lets recoverTask preserve the workload's *actual* exit
	// code when phase-2 SIGKILL has to fire. Set to -1 by registerHooks;
	// recoverTask phase 2 stores the container's State.ExitCode here if
	// `docker inspect` returns it cleanly before the kill. The post-Wait
	// path in executeTask reads this and, if non-negative, uses it instead
	// of cmd.Wait()'s "signal: killed" — so a workload that actually
	// succeeded but got stuck in our log pipeline is reported as S, not F.
	capturedExit atomic.Int32
}

var taskHooksMap sync.Map // map[int32]*taskHooks

// pendingDirectives carries signals that arrived before the worker had a
// container to deliver them to (typically while the task is still in C/D/O).
// The launch checkpoint in executeTask reads this map right before the
// docker run / bare exec and either cancels the launch or re-reads the
// task fields from the server, depending on the directive. Keyed by
// task_id; value is "cancel" (K/T arrived) or "reread" (B arrived).
//
// We do this instead of trying to interrupt the in-flight download:
// rclone cancellation requires context propagation through the fetch
// layer that doesn't exist today, and forcibly killing the goroutine is
// not a thing in Go. Phase-boundary handling is the natural fit.
var pendingDirectives sync.Map // map[int32]string ("cancel" | "reread")

// Track how long tasks have been locally active and whether they are executing
var activeSince sync.Map    // map[int32]time.Time
var executingTasks sync.Map // map[int32]struct{}

// auto detect self specs
func (w *WorkerConfig) registerSpecs(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. CPU — honor MaxCPU cap if set, but never advertise more than the
	//    machine actually has.
	hostCPU := int32(runtime.NumCPU())
	cpuCount := hostCPU
	if w.MaxCPU > 0 {
		if w.MaxCPU > hostCPU {
			log.Fatalf("--max-cpu=%d exceeds host capacity (%d CPUs)", w.MaxCPU, hostCPU)
		}
		cpuCount = w.MaxCPU
	}

	// 2. Memory
	vmem, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("⚠️ Could not detect memory: %v", err)
		return
	}
	hostMem := float32(vmem.Total) / (1024 * 1024 * 1024)
	totalMem := hostMem
	if w.MaxMem > 0 {
		if w.MaxMem > hostMem {
			log.Fatalf("--max-mem=%.2f GiB exceeds host capacity (%.2f GiB)", w.MaxMem, hostMem)
		}
		totalMem = w.MaxMem
	}

	// 3. Disk under store path
	var stat syscall.Statfs_t
	if err := syscall.Statfs(w.Store, &stat); err != nil {
		log.Printf("⚠️ Could not statfs %s: %v", w.Store, err)
		return
	}
	freeDisk := float32(stat.Bavail) * float32(stat.Bsize) / (1024 * 1024 * 1024)

	// 4. Send
	req := &pb.ResourceSpec{
		WorkerId: fmt.Sprintf("%d", w.WorkerId),
		Cpu:      cpuCount,
		Mem:      totalMem,
		Disk:     freeDisk,
	}
	res, err := client.RegisterSpecifications(ctx, req)
	if err != nil {
		log.Printf("⚠️ Failed to register specs: %v", err)
		return
	}
	if res.Success {
		log.Printf("📦 Registered specs: CPU=%d MEM=%.1f GiB DISK=%.1f GiB",
			req.Cpu,
			req.Mem,
			req.Disk,
		)
	}
}

// registerWorker registers the client worker with the server.
func (w *WorkerConfig) registerWorker(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build identity — used by the server to flag stale workers in
	// `scitq worker list`. See specs/worker_autoupgrade.md.
	workerVersion := version.Version
	workerCommit := ""
	if vcs, ok := version.ReadVCS(); ok && vcs.Revision != "" {
		workerCommit = vcs.Revision
	} else if version.Commit != "" && version.Commit != "none" {
		workerCommit = version.Commit
	}
	buildArch := runtime.GOOS + "/" + runtime.GOARCH

	res, err := client.RegisterWorker(ctx,
		&pb.WorkerInfo{
			Name:        w.Name,
			Concurrency: &w.Concurrency,
			IsPermanent: &w.IsPermanent,
			Provider:    w.Provider,
			Region:      w.Region,
			Version:     &workerVersion,
			Commit:      &workerCommit,
			BuildArch:   &buildArch,
		})
	if err != nil {
		log.Fatalf("Failed to register worker: %v", err)
	} else {
		w.WorkerId = res.WorkerId
	}
	w.registerSpecs(client)
	log.Printf("✅ Worker %s registered with concurrency %d (version %s, commit %s, arch %s)",
		w.Name, w.Concurrency, workerVersion, shortSha(workerCommit), buildArch)
}

// shortSha returns the first 7 chars of a git SHA, or the original if shorter.
func shortSha(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

// updateTaskStatus marks task as `S` (Success) or `F` (Failed) after execution.
//func updateTaskStatus(client pb.TaskQueueClient, taskID int32, status string) {
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	_, err := client.UpdateTaskStatus(ctx, &pb.TaskStatusUpdate{
//		TaskId:    taskID,
//		NewStatus: status,
//	})
//	if err != nil {
//		log.Printf("⚠️ Failed to update task %d status to %s: %v", taskID, status, err)
//	} else {
//		log.Printf("✅ Task %d updated to status: %s", taskID, status)
//	}
//}

// getScriptExtension determines the appropriate file extension based on the shell/interpreter
func getScriptExtension(shell string) string {
	// Extract the base name from the shell path (handles /usr/bin/python3, /opt/venv/bin/python, etc.)
	baseShell := filepath.Base(shell)
	
	// Check for shell-like interpreters (all end with 'sh')
	if strings.HasSuffix(baseShell, "sh") {
		return ".sh"
	}
	
	// Check for Python interpreters (python, python3, python3.12, etc.)
	if strings.HasPrefix(baseShell, "python") {
		return ".py"
	}
	
	// Check for R interpreter
	if baseShell == "R" || baseShell == "Rscript" {
		return ".R"
	}
	
	// Check for other common interpreters
	switch baseShell {
	case "perl":
		return ".pl"
	case "ruby":
		return ".rb"
	case "lua":
		return ".lua"
	case "node", "nodejs":
		return ".js"
	case "php":
		return ".php"
	}
	
	// Default extension for unknown interpreters
	return ".cmd"
}

// executeTask runs the Docker command and streams logs.
func executeTask(client pb.TaskQueueClient, reporter *event.Reporter, task *pb.Task, wg *sync.WaitGroup, store string, dm *DownloadManager, cpu int32, memGB int32, workerName string, noBare bool, serverAddr string, workerToken string, numaAlloc Allocation, numaBound bool) {
	defer wg.Done()
	log.Printf("🚀 Executing task %d: %s", task.TaskId, task.Command)

	// 🛑 Only proceed if the task is Accepted (C), Downloading (D), or On hold (O)
	if task.Status != "C" && task.Status != "D" && task.Status != "O" {
		log.Printf("⚠️ Task %d is not in C/D/O but %s, skipping.", task.TaskId, task.Status)
		reporter.Event("E", "task", fmt.Sprintf("task %d is not in accepted state", task.TaskId), map[string]any{
			"task_id": task.TaskId,
			"status":  task.Status,
			"command": task.Command,
		})
		return
	}
	// Launch checkpoint: signals that arrived while the task was in C/D/O
	// (no container, no bare process to deliver to) were parked here as
	// directives. Apply them now, BEFORE we commit to R and exec:
	//   - "cancel": operator sent K/T while the task was still pre-running.
	//               Mark as F with failure_class=cancelled and bail. We
	//               don't try to interrupt the download — it already ran
	//               or is running; the bandwidth is sunk cost — but the
	//               CPU-bound run is skipped.
	//   - "reread": operator edited the task fields (command, container,
	//               container_options, shell) and sent signal B. Pull the
	//               fresh values from the server before we exec.
	if d, ok := pendingDirectives.LoadAndDelete(task.TaskId); ok {
		directive := d.(string)
		switch directive {
		case "cancel":
			reporter.Event("I", "phase", "cancelled before launch (pre-launch signal)", map[string]any{
				"task_id": task.TaskId,
			})
			task.Status = "F"
			reporter.UpdateTask(task.TaskId, "F", "task cancelled before launch by operator signal", "cancelled")
			return
		case "reread":
			// Re-fetch task fields from the server. Only the fields the
			// operator can edit in place: command, container,
			// container_options, shell. Inputs/output/publish are
			// deliberately not re-read — those are workflow-shape changes
			// that need a fresh task_id, not an in-place patch.
			ctxRR, cancelRR := context.WithTimeout(context.Background(), 5*time.Second)
			fresh, rrErr := client.GetTask(ctxRR, &pb.TaskId{TaskId: task.TaskId})
			cancelRR()
			if rrErr != nil {
				log.Printf("⚠️ pre-launch reread failed for task %d: %v (running with stale command)", task.TaskId, rrErr)
			} else {
				task.Command = fresh.Command
				task.Container = fresh.Container
				task.ContainerOptions = fresh.ContainerOptions
				task.Shell = fresh.Shell
				reporter.Event("I", "phase", "pre-launch reread applied", map[string]any{
					"task_id": task.TaskId,
				})
			}
		}
	}

	// Promote to Running locally and on the server
	reporter.Event("I", "phase", "promoting to R (start exec)", map[string]any{
		"task_id": task.TaskId,
	})
	task.Status = "R"
	reporter.UpdateTaskAsync(task.TaskId, "R", "", nil, "")

	runStart := time.Now()

	//TODO: linking resource
	for _, r := range task.Resource {
		dm.resourceLink(r, store+"/tasks/"+fmt.Sprint(task.TaskId)+"/resource")
	}

	if task.Status == "F" {
		return // ❌ Do not execute if task is marked as failed
	}

	taskDir := store + "/tasks/" + fmt.Sprint(task.TaskId)
	isBare := task.Container == "bare"

	// Reject bare tasks if -no-bare flag is set
	if isBare && noBare {
		message := fmt.Sprintf("Task %d rejected: bare execution disabled on this worker", task.TaskId)
		log.Printf("❌ %s", message)
		reporter.Event("E", "task", message, map[string]any{"task_id": task.TaskId})
		task.Status = "F"
		reporter.UpdateTask(task.TaskId, "V", message, "")
		return
	}

	var cmd *exec.Cmd
	// Container name is computed up-front (deterministic from worker + task)
	// so the watchdog and signal handler can both reference it without
	// reaching into the per-branch local. Empty when isBare.
	var containerName string
	if !isBare {
		containerName = fmt.Sprintf("scitq-%s-task-%d", workerName, task.TaskId)
	}

	// scitq_auth: per-task opt-in. When true the task gets SCITQ_SERVER and
	// SCITQ_TOKEN in its env (so `scitq file copy` etc. can authenticate as
	// the worker), and docker tasks additionally bind-mount the worker's
	// scitq CLI binary at /usr/local/bin/scitq:ro so the binary is available
	// regardless of what's baked into the container image. The worker's own
	// auth token is reused — same identity as the worker's other RPCs, no
	// new credential surface.
	scitqAuth := task.ScitqAuth != nil && *task.ScitqAuth

	if isBare {
		// Bare execution: run command directly without Docker
		shell := "sh"
		if task.Shell != nil {
			shell = *task.Shell
		}
		cmd = exec.Command(shell, "-c", task.Command)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own process group for clean signal delivery
		cmd.Dir = taskDir + "/tmp"
		cmd.Env = append(os.Environ(),
			"INPUT="+taskDir+"/input",
			"OUTPUT="+taskDir+"/output",
			"RESOURCE="+taskDir+"/resource",
			"TEMP="+taskDir+"/tmp",
			"SCRIPTS="+taskDir+"/scripts",
			"BUILTIN="+filepath.Join(store, helperFolder),
			fmt.Sprintf("CPU=%d", cpu),
			fmt.Sprintf("THREADS=%d", cpu),
			fmt.Sprintf("MEM=%d", memGB),
		)
		if scitqAuth {
			cmd.Env = append(cmd.Env, "SCITQ_SERVER="+serverAddr, "SCITQ_TOKEN="+workerToken)
		}
		log.Printf("🛠️  Running bare command: %s -c %s", shell, task.Command)
	} else {
		// Docker execution: existing logic (containerName hoisted above).
		// `--rm` was removed deliberately: keeping the container in stopped
		// state until executeTask explicitly removes it lets recoverTask
		// query State.ExitCode via `docker inspect` if it ever needs to
		// SIGKILL the cmd. The defer below is responsible for cleanup so
		// the daemon doesn't accumulate stopped containers.
		command := []string{"run", "--name", containerName, "-e", fmt.Sprintf("CPU=%d", cpu), "-e", fmt.Sprintf("THREADS=%d", cpu), "-e", fmt.Sprintf("MEM=%d", memGB)}
		// NUMA binding: when the allocator handed us a slot, pin both
		// CPUs and memory through docker's cpuset interface. Both flags
		// matter — `--cpuset-cpus` alone pins threads but leaves
		// memory allocations cross-die, defeating the point on
		// bandwidth-bound workloads like de Bruijn assemblers.
		if numaBound {
			command = append(command,
				"--cpuset-cpus", numaAlloc.Cpuset,
				"--cpuset-mems", numaAlloc.Memset,
			)
		}
		if scitqAuth {
			// Token + server reach the container via -e so the task can
			// authenticate as the worker. Binary is bind-mounted in
			// case the container image doesn't include it (most don't).
			// Assumes /usr/local/bin/scitq on the worker is the static
			// build (CGO_ENABLED=0) — see Makefile / install-cli.
			command = append(command,
				"-e", "SCITQ_SERVER="+serverAddr,
				"-e", "SCITQ_TOKEN="+workerToken,
				"-v", "/usr/local/bin/scitq:/usr/local/bin/scitq:ro",
			)
		}
		option := ""
		for _, folder := range []string{"input", "output", "tmp", "resource", "scripts"} {
			if folder == "resource" || folder == "scripts" {
				option = ":ro"
			} else {
				option = ""
			}
			command = append(command, "-v", taskDir+"/"+folder+":/"+folder+option)
		}
		command = append(command, "-v", filepath.Join(store, helperFolder)+":/builtin:ro")

		if task.ContainerOptions != nil {
			options := strings.Fields(*task.ContainerOptions)
			command = append(command, options...)
		}

		// Check if command is too long and needs script file
		useScriptFile := len(task.Command) > utils.MaxCommandLength && task.Shell != nil

		if useScriptFile {
			scriptContent := task.Command
			scriptExtension := getScriptExtension(*task.Shell)
			scriptPath := filepath.Join(taskDir, "scripts", fmt.Sprintf("task_%d%s", task.TaskId, scriptExtension))
			if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
				message := fmt.Sprintf("Failed to create scripts directory for task %d: %v", task.TaskId, err)
				log.Printf("❌ %s", message)
				reporter.Event("E", "task", message, map[string]any{"task_id": task.TaskId, "command": task.Command})
				task.Status = "F"
				reporter.UpdateTask(task.TaskId, "V", message, "")
				return
			}
			if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
				message := fmt.Sprintf("Failed to write script file for task %d: %v", task.TaskId, err)
				log.Printf("❌ %s", message)
				reporter.Event("E", "task", message, map[string]any{"task_id": task.TaskId, "command": task.Command})
				task.Status = "F"
				reporter.UpdateTask(task.TaskId, "V", message, "")
				return
			}
			command = append(command, task.Container, *task.Shell, "/scripts/task_"+fmt.Sprint(task.TaskId)+scriptExtension)
			log.Printf("📝 Using script file for long command (length: %d) with shell: %s (extension: %s)", len(task.Command), *task.Shell, scriptExtension)
		} else {
			if task.Shell != nil {
				command = append(command, task.Container, *task.Shell, "-c", task.Command)
			} else {
				args, err := shlex.Split(task.Command)
				if err != nil {
					message := fmt.Sprintf("Failed to analyze command %s: %v", task.Command, err)
					log.Printf("❌ %s", message)
					reporter.Event("E", "task", message, map[string]any{"task_id": task.TaskId, "command": task.Command})
					task.Status = "F"
					reporter.UpdateTask(task.TaskId, "V", message, "")
					return
				}
				command = append(command, task.Container)
				command = append(command, args...)
			}
		}

		log.Printf("🛠️  Running command: docker %s", strings.Join(command, " "))
		cmd = exec.Command("docker", command...)
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		message := fmt.Sprintf("Failed to start task %d: %v", task.TaskId, err)
		log.Printf("❌ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		task.Status = "F"                              // Mark as failed
		reporter.UpdateTask(task.TaskId, "V", message, "") // Mark as failed
		return
	}

	// Track bare processes for signal handling (keyed by workerName:taskID to avoid cross-test collisions)
	bareKey := fmt.Sprintf("%s:%d", workerName, task.TaskId)
	if isBare && cmd.Process != nil {
		bareProcesses.Store(bareKey, cmd.Process)
		defer bareProcesses.Delete(bareKey)
	}

	// Open log stream
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.SendTaskLogs(ctx)
	if err != nil {
		message := fmt.Sprintf("Failed to open log stream for task %d: %v", task.TaskId, err)
		log.Printf("⚠️ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		cmd.Wait()
		task.Status = "F"                              // Mark as failed
		reporter.UpdateTask(task.TaskId, "V", message, "") // Mark as failed
		log.Printf("⚠️ Failed to open log stream for task %d: %v", task.TaskId, err)
		return
	}

	// Register recovery hooks so the SignalTask handler (when `docker kill`
	// fails with "No such container") and the per-task liveness watchdog
	// (when the container disappears but executeTask is still spinning) can
	// both forcibly unstick this invocation. See the taskHooks doc comment.
	hooks := &taskHooks{
		cancel:        cancel,
		cmd:           cmd,
		containerName: containerName,
		isBare:        isBare,
	}
	hooks.capturedExit.Store(-1) // sentinel: "no captured exit code yet"
	taskHooksMap.Store(task.TaskId, hooks)
	defer taskHooksMap.Delete(task.TaskId)

	// Ensure the (no longer auto-removed) docker container is cleaned up
	// regardless of how executeTask exits. Best-effort: log on failure but
	// don't propagate. -f removes whether the container is running or
	// stopped, so it also handles the rare case where cmd.Wait() returned
	// before the container fully cleaned up.
	if !isBare && containerName != "" {
		defer func() {
			if out, err := exec.Command("docker", "rm", "-f", containerName).CombinedOutput(); err != nil {
				outStr := strings.TrimSpace(string(out))
				// "No such container" just means cleanup isn't needed.
				if !strings.Contains(outStr, "No such container") {
					log.Printf("⚠️ Task %d: docker rm cleanup failed: %v (%s)", task.TaskId, err, outStr)
				}
			}
		}()
	}

	// Liveness watchdog. If the workload (docker container or bare process)
	// is observed gone for two consecutive checks (≥2 minutes), assume
	// executeTask is stuck — most commonly because a sendLogs goroutine is
	// blocked inside stream.Send() on the gRPC log stream and logWg.Wait()
	// therefore never returns. recoverTask cancels the gRPC context (unstuck
	// stream → sendLogs goroutines exit → logWg.Wait() returns) and kills
	// the docker-run client / bare process (cmd.Wait() returns), letting the
	// normal post-Wait status path report the task as F to the server.
	if isBare {
		go watchdogBare(ctx, task.TaskId, cmd, hooks)
	} else {
		go watchdogContainer(ctx, task.TaskId, containerName, hooks)
	}

	// Function to send logs
	var logWg sync.WaitGroup
	sendLogs := func(reader io.Reader, stream pb.TaskQueue_SendTaskLogsClient, logType string) {
		defer logWg.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if err := stream.Send(&pb.TaskLog{TaskId: task.TaskId, LogType: logType, LogText: line}); err != nil {
				log.Printf("⚠️ Failed to send log line for task %d: %v", task.TaskId, err)
				break
			}
		}
	}

	// Stream logs concurrently
	logWg.Add(2)
	go sendLogs(stdout, stream, "stdout")
	go sendLogs(stderr, stream, "stderr")

	// Wait for log goroutines to finish reading stdout/stderr BEFORE cmd.Wait(),
	// because cmd.Wait() closes the pipe read ends (Go docs: "It is thus incorrect
	// to call Wait before all reads from the pipe have completed.").
	logWg.Wait()
	err = cmd.Wait()
	// CloseAndRecv waits for the server to confirm all log messages are written,
	// ensuring logs are on disk before we send the status update.
	stream.CloseAndRecv()

	// If recoverTask phase 2 had to SIGKILL cmd but successfully captured
	// the container's real exit code via `docker inspect` first, override
	// cmd.Wait()'s "signal: killed" with the workload's true outcome.
	// Without this, a workload that actually finished cleanly but got
	// stuck in our log pipeline would be incorrectly reported as F.
	if captured := hooks.capturedExit.Load(); captured >= 0 {
		if captured == 0 {
			err = nil
			log.Printf("✅ Task %d: container exit code 0 captured via inspect; overriding kill-status as success", task.TaskId)
		} else {
			err = fmt.Errorf("container exited with code %d", captured)
			log.Printf("ℹ️ Task %d: container exit code %d captured via inspect; overriding kill-status with real code", task.TaskId, captured)
		}
	}

	// **UPDATE TASK STATUS BASED ON SUCCESS/FAILURE**
	sec := int32(time.Since(runStart).Seconds())
	if err != nil {
		message := fmt.Sprintf("Task %d failed: %v", task.TaskId, err)
		log.Printf("❌ %s", message)
		reporter.Event("E", "task", message, map[string]any{
			"task_id": task.TaskId,
			"command": task.Command,
		})
		task.Status = "F"
		// Classify so the eventual terminal F (sent by the uploader) can
		// carry the right failure_class for the server's retry-decision
		// path (spec: addition_from_nextflow.md A).
		fc := classifyExecFailure(err, hooks)
		task.FailureClass = &fc
		reporter.UpdateTaskAsync(task.TaskId, "V", "", &sec, "") // Mark as failed
	} else {
		log.Printf("✅ Task %d completed successfully", task.TaskId)
		task.Status = "S" // Mark as success
		reporter.UpdateTaskAsync(task.TaskId, "U", "", &sec, "")
	}
}

// classifyExecFailure inspects a cmd.Wait error and the recovery hooks to
// decide which failure_class to attach to the eventual terminal F status.
// Values: "oom" (container exit 137 / Linux OOMKilled), "timeout" (running
// timeout / context cancelled), "other" (everything else). "eviction" is
// never produced here — server-side reaping owns that classification.
func classifyExecFailure(err error, hooks *taskHooks) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	msg := err.Error()
	// cmd.Wait reports docker / process exit codes via *exec.ExitError —
	// we already have its String() embedded in err. Both forms ("exit
	// status 137" from exec, "container exited with code 137" from our
	// own captured-exit override path) contain "137".
	if strings.Contains(msg, " 137") || strings.HasSuffix(msg, "137") {
		return "oom"
	}
	if hooks != nil {
		if c := hooks.capturedExit.Load(); c == 137 {
			return "oom"
		}
	}
	return "other"
}

// cleanupTaskWorkingDir removes the task working directory regardless of task outcome.
// recoverTask forcibly unsticks an executeTask invocation that has been
// stranded (container exited but worker never noticed; docker daemon
// unresponsive; etc.). Idempotent — multiple callers (signal handler,
// watchdog) can fire it without compounding effects. Returns true if this
// call actually performed recovery (false if recovery was already done).
//
// Two-phase by design:
//
//  1. Immediate: cancel the gRPC log-stream context. This is sufficient
//     in the common case where the wedge is a sendLogs goroutine blocked
//     inside stream.Send() — sendLogs errors out, logWg.Wait() returns,
//     cmd.Wait() reaps the (now-zombie) docker run / bare process and
//     returns its *natural* exit code. The post-Wait path then reports
//     S or F based on the actual workload result, preserving a
//     successful workload that just had a wedged log pipeline.
//
//  2. Fallback (after 30s grace): if executeTask hasn't returned yet,
//     the wedge is deeper than the log stream — most likely cmd.Wait()
//     itself is blocked because the underlying process is still alive
//     (docker daemon unresponsive, docker run stuck on a full output
//     pipe, bare process in some uninterruptible state). SIGKILL the
//     cmd. The natural exit code is then lost, cmd.Wait() returns
//     "signal: killed", and the task is reported as F — but at least
//     the worker stops being stuck.
func recoverTask(taskID int32, hooks *taskHooks, reason string) bool {
	if hooks == nil {
		return false
	}
	if !hooks.recovered.CompareAndSwap(false, true) {
		return false
	}
	log.Printf("🩺 Task %d: forcing recovery (%s)", taskID, reason)
	// Phase 1: cancel-only.
	if hooks.cancel != nil {
		hooks.cancel()
	}
	// Phase 2: SIGKILL fallback.
	go func() {
		time.Sleep(30 * time.Second)
		if _, stillRegistered := taskHooksMap.Load(taskID); !stillRegistered {
			// executeTask already finished naturally with its real exit
			// code — perfect, nothing to do.
			return
		}
		// Before killing, try to recover the container's actual exit code
		// from the daemon. With --rm dropped, the container persists in
		// stopped state until our cleanup defer removes it; until then
		// State.ExitCode is queryable. If we get a clean code, the
		// post-Wait path in executeTask will use it instead of
		// "signal: killed", preserving a successful workload.
		if !hooks.isBare && hooks.containerName != "" {
			out, err := exec.Command("docker", "inspect", "-f", "{{.State.ExitCode}}", hooks.containerName).CombinedOutput()
			if err == nil {
				if code, perr := strconv.Atoi(strings.TrimSpace(string(out))); perr == nil {
					hooks.capturedExit.Store(int32(code))
					log.Printf("🩺 Task %d recovery: captured container exit code %d before SIGKILL", taskID, code)
				}
			}
		}
		log.Printf("🩺 Task %d: still stuck 30s after cancel; SIGKILL'ing cmd process", taskID)
		if hooks.cmd != nil && hooks.cmd.Process != nil {
			if err := hooks.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") {
				log.Printf("⚠️ Task %d recovery: kill cmd process failed: %v", taskID, err)
			}
		}
	}()
	return true
}

// signalRecover is the fast-path bridge from the SignalTask handler into
// recoverTask: looks up the per-task hooks and triggers recovery if found.
// No-op if the task isn't currently registered (already finished, never
// started, etc.).
func signalRecover(taskID int32, reason string) {
	if h, ok := taskHooksMap.Load(taskID); ok {
		recoverTask(taskID, h.(*taskHooks), reason)
	}
}

// watchdogContainer polls `docker inspect` on a per-task basis and triggers
// recoverTask if the container is missing or stopped for two consecutive
// checks. Skips a 60s grace period after launch so docker pull / network
// setup / other startup latency is never mistaken for a missing container.
func watchdogContainer(ctx context.Context, taskID int32, containerName string, hooks *taskHooks) {
	// Grace period for image pull, container creation, etc. before the first
	// liveness check. 60s is comfortably longer than typical pull times for
	// already-cached images and short enough to catch a wedge within minutes.
	select {
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		// docker inspect -f '{{.State.Running}}' returns "true"/"false" if the
		// container exists, or non-zero exit + "No such object" on stderr if
		// it's gone. Either of those signals "container is no longer doing
		// work for us" and counts as a miss.
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).CombinedOutput()
		alive := err == nil && strings.TrimSpace(string(out)) == "true"
		if alive {
			misses = 0
			continue
		}
		misses++
		if misses < 2 {
			continue
		}
		reason := fmt.Sprintf("container %s gone or stopped for ≥2 min (last inspect: err=%v out=%q)",
			containerName, err, strings.TrimSpace(string(out)))
		recoverTask(taskID, hooks, reason)
		return
	}
}

// watchdogBare is the bare-task analogue of watchdogContainer. It detects
// "process is a zombie or gone" by reading /proc/<pid>/status on Linux. On
// systems without /proc (e.g. macOS dev machines) the watchdog is a no-op
// — bare tasks just won't get the safety net there. The same recoverTask
// path is used once a stuck workload is detected.
func watchdogBare(ctx context.Context, taskID int32, cmd *exec.Cmd, hooks *taskHooks) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if _, err := os.Stat("/proc"); err != nil {
		// No /proc — can't detect zombie state reliably. Skip the watchdog.
		return
	}
	pid := cmd.Process.Pid
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	// Same grace + cadence as watchdogContainer.
	select {
	case <-ctx.Done():
		return
	case <-time.After(60 * time.Second):
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		state, gone, err := readProcState(statusPath)
		// "Gone" = /proc/<pid>/status disappeared (process reaped or never
		// existed). "Zombie" = exited but not yet reaped (cmd.Wait() hasn't
		// run because logWg.Wait() is stuck — that's our smoking gun).
		// "X (dead)" is the terminal state Linux briefly reports between
		// exit and full cleanup; treat the same as zombie.
		dead := gone || state == "Z" || state == "X"
		if err != nil && !gone {
			// Transient read error (not ENOENT) — don't count as a miss,
			// retry next tick. We only want firm signals.
			continue
		}
		if !dead {
			misses = 0
			continue
		}
		misses++
		if misses < 2 {
			continue
		}
		var reason string
		if gone {
			reason = fmt.Sprintf("bare pid %d gone (no /proc entry) for ≥2 min", pid)
		} else {
			reason = fmt.Sprintf("bare pid %d in state %q for ≥2 min", pid, state)
		}
		recoverTask(taskID, hooks, reason)
		return
	}
}

// readProcState parses /proc/<pid>/status and returns the single-letter
// process state (e.g. "R", "S", "Z", "X"). gone is true when the file does
// not exist (process has been reaped or never started).
func readProcState(statusPath string) (state string, gone bool, err error) {
	data, err := os.ReadFile(statusPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true, nil
		}
		return "", false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "State:") {
			continue
		}
		// Format: "State:\tZ (zombie)" — the first non-space token after
		// the colon is the single-letter state code.
		fields := strings.Fields(strings.TrimPrefix(line, "State:"))
		if len(fields) > 0 {
			return fields[0], false, nil
		}
	}
	return "", false, nil
}

func cleanupTaskWorkingDir(store string, taskID int32, reporter *event.Reporter) {
	dir := filepath.Join(store, "tasks", fmt.Sprint(taskID))
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("⚠️ Failed to cleanup working dir for task %d: %v", taskID, err)
		reporter.Event("W", "cleanup", "failed to cleanup task working dir", map[string]any{
			"task_id": taskID,
			"dir":     dir,
			"error":   err.Error(),
		})
		return
	}
	log.Printf("🧹 Cleaned up working dir for task %d", taskID)
}

// fetchTasks requests new tasks from the server. Returns the task list,
// the operator-triggered upgrade flag (if any), and whether the server
// itself is in a graceful-drain window — all carried in the same ping
// response.
// fetchTasks: `caps` points to the shared LiveCaps so that any
// server-pushed change to max_cpu / max_mem on the ping response can be
// applied here and immediately visible to per-task goroutines.
func (w *WorkerConfig) fetchTasks(caps *LiveCaps,
	ctx context.Context,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	id int32,
	sem *utils.ResizableSemaphore,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
	numaAllocator *NumaAllocator,
	lastDerivedStep *int32, // step_id we last auto-derived concurrency for; 0 = none yet
) ([]*pb.Task, string, bool, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var query *pb.PingAndGetNewTasksRequest
	ws, err := workerstats.CollectWorkerStats()
	if err != nil {
		log.Printf("⚠️ Error could not collect stats: %v", err)
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id}
	} else {
		// Apply current CPU cap to the reported stat so what scitq sees
		// from this worker stays consistent with what we advertised.
		// Reads through caps so a server-pushed change is reflected on
		// the very next heartbeat. (Memory % is a percentage of the real
		// host, independent of the cap — left alone.)
		if c := caps.CPU(); c > 0 && c < ws.NumCPUs {
			ws.NumCPUs = c
		}
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id, Stats: ws.ToProto()}
	}

	// Report the tasks we're actually tracking locally so the server can
	// reconcile: any task it believes is active on us but that we no longer
	// list has been lost (finished + cleaned up but terminal status update
	// failed to land). ReportsKnownTasks=true tells the server this list is
	// authoritative for us — an old client never sets it, so the server won't
	// mistake its silence for "all tasks lost".
	known := make([]int32, 0)
	activeTasks.Range(func(k, _ any) bool {
		known = append(known, k.(int32))
		return true
	})
	query.KnownTaskIds = known
	query.ReportsKnownTasks = true

	res, err := client.PingAndTakeNewTasks(ctx, query)
	if err != nil {
		log.Printf("⚠️ Error calling fetch tasks: %v", err)
		return nil, "", false, 0, err
	}

	// Apply any server-pushed cap change. The server's view of the
	// worker's flavor.cpu/mem is the authoritative cap; if the operator
	// edited it via UpdateWorker, the new value arrives here. Validate
	// against host capacity client-side — the server doesn't know the
	// real machine size, only what we advertised at registration time.
	if res.MaxCpu != nil {
		newCPU := res.GetMaxCpu()
		if newCPU > 0 && newCPU != caps.CPU() {
			if hostCPU := int32(runtime.NumCPU()); newCPU > hostCPU {
				log.Printf("⚠️ Server pushed max_cpu=%d exceeds host capacity (%d) — ignoring", newCPU, hostCPU)
			} else {
				log.Printf("⚙️  Resizing CPU cap %d → %d (server-pushed)", caps.CPU(), newCPU)
				caps.SetCPU(newCPU)
			}
		}
	}
	if res.MaxMem != nil {
		newMem := res.GetMaxMem()
		if newMem > 0 && newMem != caps.Mem() {
			var hostMem float32
			if vmem, err := mem.VirtualMemory(); err == nil {
				hostMem = float32(vmem.Total) / (1024 * 1024 * 1024)
			}
			if hostMem > 0 && newMem > hostMem {
				log.Printf("⚠️ Server pushed max_mem=%.2f GiB exceeds host capacity (%.2f GiB) — ignoring", newMem, hostMem)
			} else {
				log.Printf("⚙️  Resizing memory cap %.2f → %.2f GiB (server-pushed)", caps.Mem(), newMem)
				caps.SetMem(newMem)
			}
		}
	}

	// Apply task weights if any
	updates := make(map[int32]float64)
	for taskID, update := range res.Updates.Updates {
		taskWeights.Store(taskID, update.Weight)
		updates[taskID] = update.Weight
	}
	if res.Concurrency != w.Concurrency {
		log.Printf("Resizing concurrency from %d to %d", w.Concurrency, res.Concurrency)
		sem.ResizeAll(float64(res.Concurrency), updates)
		w.Concurrency = res.Concurrency
	} else {
		sem.ResizeTasks(updates)
	}

	// Auto-derive concurrency from NUMA the first time we see a
	// numa-bound task *on a given step*. The DSL's `task_spec.numa: N`
	// implies "give each task N nodes," and the natural concurrency on
	// this host is floor(host_nodes / N). Doing this here means the
	// operator doesn't have to remember to set `--concurrency` to match
	// the topology — `numa: 1` on a 4-node EPYC just works.
	// EffectiveNodeCount() floors at 1 so a non-NUMA host still behaves
	// sensibly (concurrency=1 for numa:1).
	//
	// Scoped to "first time per step" rather than "first time ever per
	// worker boot": a permanent worker that gets recycled across
	// workflows (recyclable_scope=W) ends up on a fresh step with
	// concurrency reset by the recruiter's placeholder (=1), so we
	// re-derive then. Once we've derived for a step we don't fight the
	// operator if they later `worker update --concurrency M` it.
	// tryDerive returns true when it acted on this task (either derived for
	// a new step, or recognised the task is for an already-derived step).
	// The poll-side loop uses that to stop early; the activeTasks fallback
	// only stops on an actual new derive — see below.
	tryDerive := func(t *pb.Task) (acted, stepIsNew bool) {
		if t == nil || t.Numa == nil || t.GetNuma() < 1 {
			return false, false
		}
		stepID := t.GetStepId()
		if stepID == 0 {
			return false, false
		}
		if stepID == *lastDerivedStep {
			return true, false // already derived; nothing to do
		}
		nodes := numaAllocator.EffectiveNodeCount()
		desired := int32(nodes / int(t.GetNuma()))
		if desired < 1 {
			desired = 1
		}
		if desired != w.Concurrency {
			log.Printf("📐 Auto-deriving concurrency from numa: %d → %d (step %d, host has %d NUMA node(s), task wants %d)",
				w.Concurrency, desired, stepID, nodes, t.GetNuma())
			if _, err := client.UpdateWorker(ctx, &pb.WorkerUpdateRequest{
				WorkerId:    w.WorkerId,
				Concurrency: &desired,
			}); err != nil {
				log.Printf("⚠️ Auto-derive: UpdateWorker failed: %v — staying at %d", err, w.Concurrency)
				return true, true
			}
			// Resize locally too so the executer thread starts using
			// the new value immediately; the next ping will push the
			// same value back from the server, which is idempotent.
			sem.ResizeAll(float64(desired), updates)
			w.Concurrency = desired
		}
		// Mark this step as derived even when desired==current — we
		// don't want to re-emit the log line on every ping just because
		// the operator happened to have the right value already.
		*lastDerivedStep = stepID
		return true, true
	}
	derivedNewStep := false
	for _, t := range res.Tasks {
		if acted, isNew := tryDerive(t); acted {
			derivedNewStep = isNew
			break
		}
	}
	// Fallback for the chicken-and-egg case: a permanent worker upgraded
	// mid-workflow is already running a numa task at concurrency=1, so the
	// server has nothing new to push (res.Tasks is empty) and the loop
	// above never fires. Scan locally-known active tasks — they carry the
	// same Numa/StepId fields. Stop only on an actual new derive, so we
	// don't get stuck on a stale active task from the previously-derived
	// step when a new-step task is also present.
	if !derivedNewStep {
		activeTasks.Range(func(_, v any) bool {
			t, ok := v.(*pb.Task)
			if !ok {
				return true
			}
			_, isNew := tryDerive(t)
			return !isNew
		})
	}

	// Check activeTasks map
	// 1) Build a set of server-active tasks
	serverActiveTasks := make(map[int32]struct{})
	for _, tid := range res.ActiveTasks {
		serverActiveTasks[tid] = struct{}{}
	}
	log.Printf("📡 fetchTasks: server reports %d active, %d new tasks", len(serverActiveTasks), len(res.Tasks))

	// 2) For any server-active task we don't know locally, debounce before failing
	now := time.Now()
	for tid := range serverActiveTasks {
		if _, known := activeTasks.Load(tid); !known {
			// have we seen this "unknown" before?
			if v, ok := lostTrackSeen.Load(tid); ok {
				first := v.(time.Time)
				// if it has remained unknown for ≥ one fetch cycle (or e.g. ≥3s), warn once
				if now.Sub(first) >= 3*time.Second {
					// emit a single WARNING event, cllient will catch up later
					reporter.Event("W", "task", fmt.Sprintf("lost track of task %d", tid), map[string]any{
						"task_id":      tid,
						"component":    "fetchTasks",
						"active_count": func() int { n := 0; activeTasks.Range(func(_, _ any) bool { n++; return true }); return n }(),
					})
					lostTrackSeen.Delete(tid)
				}
			} else {
				// first time we see this mismatch: remember it and give the worker loop a chance to store
				lostTrackSeen.Store(tid, now)
				log.Printf("⏳ Server says task %d active but client doesn't know it yet; deferring decision.", tid)
				reporter.Event("I", "trace", "server-active task unknown locally (will recheck)", map[string]any{"task_id": tid})
			}
		} else {
			// if we do know it locally, clear any lingering first-seen marker
			lostTrackSeen.Delete(tid)
		}
	}

	// 3) Reverse direction: for any task the client still believes is active
	//    but that the server no longer reports (neither as an incoming 'A'
	//    task nor in ActiveTasks), assume the server dropped it (hidden by
	//    edit_and_retry_task, deleted workflow, reassigned, etc.) and kill
	//    the local container. Debounce by one extra fetch cycle to tolerate
	//    the natural race where a freshly-assigned task hasn't yet appeared
	//    in the server's response.
	serverKnownSet := make(map[int32]struct{}, len(res.Tasks)+len(serverActiveTasks))
	for _, t := range res.Tasks {
		serverKnownSet[t.TaskId] = struct{}{}
	}
	for tid := range serverActiveTasks {
		serverKnownSet[tid] = struct{}{}
	}
	activeTasks.Range(func(key, _ any) bool {
		taskID := key.(int32)
		if _, known := serverKnownSet[taskID]; known {
			orphanSeen.Delete(taskID)
			return true
		}
		if v, ok := orphanSeen.Load(taskID); ok {
			first := v.(time.Time)
			if now.Sub(first) >= 3*time.Second {
				containerName := fmt.Sprintf("scitq-%s-task-%d", w.Name, taskID)
				log.Printf("🧹 Orphan task %d: server stopped tracking it — killing container %s", taskID, containerName)
				reporter.Event("W", "task", fmt.Sprintf("orphan task %d killed (server stopped tracking)", taskID), map[string]any{
					"task_id":   taskID,
					"component": "fetchTasks.orphan",
				})
				if out, err := exec.Command("docker", "kill", containerName).CombinedOutput(); err != nil {
					log.Printf("⚠️ docker kill %s failed: %v (%s)", containerName, err, strings.TrimSpace(string(out)))
				}
				bareKey := fmt.Sprintf("%s:%d", w.Name, taskID)
				if proc, ok := bareProcesses.Load(bareKey); ok {
					p := proc.(*os.Process)
					_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
				}
				activeTasks.Delete(taskID)
				activeSince.Delete(taskID)
				orphanSeen.Delete(taskID)
			}
		} else {
			orphanSeen.Store(taskID, now)
			log.Printf("⏳ Task %d active locally but server didn't mention it; will kill if still orphan on next ping.", taskID)
		}
		return true
	})

	// Execute signals.
	//   K = SIGKILL via docker kill (or pgkill for bare)
	//   T = SIGTERM via docker stop (or SIGTERM-then-grace-then-SIGKILL for bare)
	//   B = reread-on-edit; equivalent to K when a container exists (input
	//       may have been touched by the in-progress run, safest to kill
	//       and let the operator re-launch deliberately).
	// For all three, if NO bare process and NO container exist yet (task
	// still in C/D/O), the signal is parked in pendingDirectives — the
	// launch checkpoint in executeTask reads it and either cancels the
	// launch (K/T) or re-reads task fields from the server (B) before
	// docker run.
	for _, sig := range res.Signals {
		sig := sig
		go func() {
			// Check if this is a bare process
			bareKey := fmt.Sprintf("%s:%d", w.Name, sig.TaskId)
			if proc, ok := bareProcesses.Load(bareKey); ok {
				p := proc.(*os.Process)
				pgid := -p.Pid // negative PID = process group
				if sig.Signal == "T" {
					log.Printf("🛑 Stopping bare task %d (SIGTERM to pgid %d)", sig.TaskId, p.Pid)
					syscall.Kill(pgid, syscall.SIGTERM)
					// Wait for grace period, then SIGKILL
					grace := 10
					if sig.GracePeriod != nil && *sig.GracePeriod > 0 {
						grace = int(*sig.GracePeriod)
					}
					go func() {
						time.Sleep(time.Duration(grace) * time.Second)
						if _, stillRunning := bareProcesses.Load(bareKey); stillRunning {
							syscall.Kill(pgid, syscall.SIGKILL)
						}
					}()
				} else {
					// K or B — both kill the bare process. B can't safely
					// hot-swap the command on a running task.
					log.Printf("🔪 Killing bare task %d (SIGKILL to pgid %d, signal=%s)", sig.TaskId, p.Pid, sig.Signal)
					syscall.Kill(pgid, syscall.SIGKILL)
				}
				return
			}
			// Docker container signal
			containerName := fmt.Sprintf("scitq-%s-task-%d", w.Name, sig.TaskId)
			parkPendingDirective := func(reason string) {
				// Container/bare process doesn't exist — the task hasn't
				// reached launch yet. Park the directive for the launch
				// checkpoint to pick up.
				directive := "cancel"
				if sig.Signal == "B" {
					directive = "reread"
				}
				pendingDirectives.Store(sig.TaskId, directive)
				log.Printf("📌 Signal %s parked as %q for task %d (%s)", sig.Signal, directive, sig.TaskId, reason)
			}
			if sig.Signal == "T" {
				args := []string{"stop"}
				if sig.GracePeriod != nil && *sig.GracePeriod > 0 {
					args = append(args, "--time", fmt.Sprintf("%d", *sig.GracePeriod))
				}
				args = append(args, containerName)
				log.Printf("🛑 Stopping container %s (task %d, SIGTERM)", containerName, sig.TaskId)
				if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
					outStr := strings.TrimSpace(string(out))
					log.Printf("⚠️ docker stop %s failed: %v (%s)", containerName, err, outStr)
					if strings.Contains(outStr, "No such container") {
						parkPendingDirective("container not yet launched")
						signalRecover(sig.TaskId, "docker stop reported container already gone")
					}
				}
			} else {
				// K or B — both translate to docker kill when a container
				// exists. B differs from K only when the container does
				// NOT yet exist (parkPendingDirective uses sig.Signal to
				// pick "cancel" vs "reread").
				log.Printf("🔪 Killing container %s (task %d, signal=%s)", containerName, sig.TaskId, sig.Signal)
				if out, err := exec.Command("docker", "kill", containerName).CombinedOutput(); err != nil {
					outStr := strings.TrimSpace(string(out))
					log.Printf("⚠️ docker kill %s failed: %v (%s)", containerName, err, outStr)
					if strings.Contains(outStr, "No such container") {
						parkPendingDirective("container not yet launched")
						signalRecover(sig.TaskId, "docker kill reported container already gone")
					}
				}
			}
		}()
	}

	return res.Tasks, res.UpgradeRequested, res.ServerUpgradeInProgress, len(res.ActiveTasks), nil
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(ctx context.Context, client pb.TaskQueueClient, reporter *event.Reporter, config WorkerConfig, caps *LiveCaps, sem *utils.ResizableSemaphore, dm *DownloadManager, um *UploadManager, taskWeights *sync.Map, activeTasks *sync.Map, numaAllocator *NumaAllocator, lastDerivedStep *int32) {
	store := dm.Store

	var consecErrors int
	upgradeSup := newUpgradeSupervisor()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Drain any failed-download notifications; perform centralized failure handling
		for {
			select {
			case fail := <-dm.FailedQueue:
				if fail != nil && fail.Task != nil {
					tid := fail.Task.TaskId
					msg := fail.Message
					// Single point: update server status to F once
					if msg == "" {
						msg = "Download failed"
					}
					reporter.UpdateTask(tid, "F", msg, "network")
					// Cleanup and clear local bookkeeping
					cleanupTaskWorkingDir(store, tid, reporter)
					activeTasks.Delete(tid)
					log.Printf("🧹 Centralized failure: task %d marked F; cleaned up and cleared active flag", tid)
				}
				continue
			default:
			}
			break
		}

		// Drain any failed-upload notifications; perform centralized failure handling
		for {
			select {
			case fail := <-um.FailedQueue:
				if fail != nil && fail.Task != nil {
					tid := fail.Task.TaskId
					msg := fail.Message
					if msg == "" {
						msg = "Upload failed"
					}
					// Single point: update server status to F once
					reporter.UpdateTask(tid, "F", msg, "network")
					// Cleanup and clear local bookkeeping
					cleanupTaskWorkingDir(store, tid, reporter)
					activeTasks.Delete(tid)
					log.Printf("🧹 Centralized failure (upload): task %d marked F; cleaned up and cleared active flag", tid)
				}
				continue
			default:
			}
			break
		}

		tasks, upgradeReq, serverGating, serverActiveCount, err := config.fetchTasks(caps, ctx, client, reporter, config.WorkerId, sem, taskWeights, activeTasks, numaAllocator, lastDerivedStep)
		if err != nil {
			log.Printf("⚠️ Error fetching tasks: %v", err)
			consecErrors++
			if consecErrors >= fetchTaskErrorThreshold {
				reporter.Event("W", "runtime", fmt.Sprintf("consecutive fetch errors (%d)", consecErrors), map[string]any{
					"last_error": err.Error(),
				})
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		consecErrors = 0 // Reset error count on success

		// Phase II: operator-triggered upgrade. The supervisor latches
		// onto a request, optionally puts the worker into draining mode,
		// waits for in-flight tasks to settle, then performs the swap.
		// performUpgrade calls os.Exit on success — supervisor restart
		// brings up the new binary. See specs/worker_autoupgrade.md.
		upgradeSup.tick(ctx, client, reporter, &config, activeTasks, serverActiveCount, upgradeReq)

		// Phase III: server is in graceful drain. Don't pick up new
		// tasks this iteration — existing in-flight ones keep running,
		// and the gRPC retry loop will absorb the brief restart.
		if serverGating {
			log.Printf("ℹ️ Server is draining for upgrade; deferring new task pickup")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		if len(tasks) == 0 {
			log.Printf("No tasks available, retrying in 5 seconds...")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, task := range tasks {
			// Drop duplicates from the server: if this task is already active or already being scheduled for downloads, ignore it.
			if _, alreadyActive := activeTasks.Load(task.TaskId); alreadyActive {
				log.Printf("🔁 Server returned duplicate task %d — already active; ignoring", task.TaskId)
				continue
			}
			if dm != nil && dm.EnqueuedTasks != nil && dm.EnqueuedTasks[task.TaskId] {
				log.Printf("🔁 Server returned duplicate task %d — already scheduled for downloads; ignoring", task.TaskId)
				continue
			}

			task.Status = "C" // Accepted
			now := time.Now()
			activeTasks.Store(task.TaskId, task)
			activeSince.Store(task.TaskId, now)
			reporter.UpdateTaskAsync(task.TaskId, task.Status, "", nil, "")
			log.Printf("📝 Task %d accepted at %s", task.TaskId, now.Format(time.RFC3339))
			log.Printf("📌 Marked task %d active locally", task.TaskId)
			// Trace: queued for download (client side)
			reporter.Event("I", "phase", "queued for download", map[string]any{
				"task_id": task.TaskId,
				"at":      now.Format(time.RFC3339),
			})

			failed := false
			for _, folder := range []string{"input", "output", "tmp", "resource"} {
				path := filepath.Join(store, "tasks", fmt.Sprint(task.TaskId), folder)
				if err := os.MkdirAll(path, 0777); err != nil {
					log.Printf("⚠️ Failed to create directory %s for task %d: %v", folder, task.TaskId, err)

					// Report event
					reporter.Event("E", "task", fmt.Sprintf("failed to create directory %s for task %d", folder, task.TaskId), map[string]any{
						"task_id":    task.TaskId,
						"folder":     folder,
						"store_path": path,
						"error":      err.Error(),
					})

					// Mark as failed
					reporter.UpdateTask(task.TaskId, "F", fmt.Sprintf("failed to create directory %s for task %d: %v", folder, task.TaskId, err), "other")
					failed = true
					break
				}
			}

			if failed {
				// Cleanup working directory even though execution never started
				cleanupTaskWorkingDir(store, task.TaskId, reporter)

				// Clean up bookkeeping
				activeTasks.Delete(task.TaskId)
				continue // do not enqueue into TaskQueue
			}

			// Only enqueue if all folders were created successfully
			dm.TaskQueue <- task
			log.Printf("📥 Enqueued task %d to download queue", task.TaskId)
		}

	}
}

func excuterThread(
	exexQueue chan *pb.Task,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	sem *utils.ResizableSemaphore,
	store string,
	dm *DownloadManager,
	um *UploadManager,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
	workerName string,
	noBare bool,
	serverAddr string,
	workerToken string,
	caps *LiveCaps, // shared cap state; 0 = autodetect from runtime.NumCPU / host mem
	numaAllocator *NumaAllocator, // NUMA slot manager; nil-safe on non-NUMA hosts
) {
	var wg sync.WaitGroup

	for task := range exexQueue {
		wg.Add(1)
		log.Printf("Received task %d with status: %v", task.TaskId, task.Status)

		// Get task weight or default to 1.0
		weight := 1.0
		if w, ok := taskWeights.Load(task.TaskId); ok {
			weight = w.(float64)
		}

		go func(t *pb.Task, w float64) {
			// mark that we are attempting to execute (post-download, i.e. leaving O)
			reporter.Event("I", "phase", "attempting to acquire slots for execution", map[string]any{
				"task_id": t.TaskId,
				"weight":  w,
			})
			log.Printf("⏳ Waiting semaphore for task %d (weight=%.3f, current size=%.3f)", t.TaskId, w, sem.Size())

			if err := sem.AcquireWithWeight(context.Background(), w, t.TaskId); err != nil {
				message := fmt.Sprintf("Failed to acquire semaphore for task %d: %v", t.TaskId, err)
				log.Printf("❌ %s", message)
				reporter.Event("E", "task", message, map[string]any{
					"task_id": t.TaskId,
					"status":  t.Status,
					"command": t.Command,
				})
				t.Status = "F" // Mark as failed
				reporter.UpdateTask(t.TaskId, "F", message, "other")
				wg.Done()
				return
			}
			executingTasks.Store(t.TaskId, struct{}{})
			reporter.Event("I", "phase", "acquired slot; promoting to R soon", map[string]any{
				"task_id": t.TaskId,
				"weight":  w,
			})
			log.Printf("✅ Acquired semaphore for task %d (weight=%.3f)", t.TaskId, w)

			// Per-task $CPU / $MEM derive from what we *advertised* to
			// scitq (caps), not the raw host — otherwise a
			// partial-contribution worker would expose more resources to
			// each task than it actually offered. Reads through caps so
			// a server-pushed change takes effect on the *next* task
			// without restarting the client.
			advertisedCPU := float64(runtime.NumCPU())
			if c := caps.CPU(); c > 0 {
				advertisedCPU = float64(c)
			}
			cpu := max(int32(advertisedCPU/sem.Size()), 1)
			var memGB int32
			advertisedMem := float64(0)
			if m := caps.Mem(); m > 0 {
				advertisedMem = float64(m)
			} else if vmem, err := mem.VirtualMemory(); err == nil {
				advertisedMem = float64(vmem.Total) / (1024 * 1024 * 1024)
			}
			if advertisedMem > 0 {
				memGB = max(int32(advertisedMem/sem.Size()), 1)
			}
			// NUMA binding (optional). When task.Numa is set, try to
			// reserve that many NUMA nodes; on success the docker
			// command will get --cpuset-cpus / --cpuset-mems and the
			// per-task $CPU/$MEM are recomputed from the allocated
			// nodes (rather than from the host-wide cap divided by
			// concurrency). On failure (host has no NUMA, host has
			// fewer nodes than requested, or all slots occupied) we
			// log a warning and run unbound — the task still works.
			var numaAlloc Allocation
			var numaOK bool
			if t.Numa != nil && *t.Numa > 0 {
				numaAlloc, numaOK = numaAllocator.Allocate(t.TaskId, int(*t.Numa))
				if numaOK {
					cpu = int32(numaAlloc.CPUCount)
					// Approximate per-task memory budget: total advertised mem
					// scaled by node share. Topology-aware per-node memory
					// (parsing node*/meminfo) is a refinement for later.
					if advertisedMem > 0 && numaAllocator.NumaNodeCount() > 0 {
						memGB = max(int32(advertisedMem*float64(len(numaAlloc.Nodes))/float64(numaAllocator.NumaNodeCount())), 1)
					}
					log.Printf("🧩 NUMA-bound task %d → nodes=%v cpuset=%s memset=%s ($CPU=%d $MEM=%d)",
						t.TaskId, numaAlloc.Nodes, numaAlloc.Cpuset, numaAlloc.Memset, cpu, memGB)
					reporter.Event("I", "runtime", "numa bound", map[string]any{
						"task_id": t.TaskId, "numa_nodes": numaAlloc.Nodes,
						"cpuset": numaAlloc.Cpuset, "memset": numaAlloc.Memset,
					})
				} else {
					log.Printf("⚠️ NUMA request for task %d (numa=%d) could not be honoured on this host — running unbound",
						t.TaskId, *t.Numa)
					reporter.Event("W", "runtime", "numa unavailable", map[string]any{
						"task_id": t.TaskId, "numa_requested": *t.Numa,
						"host_nodes": numaAllocator.NumaNodeCount(),
					})
				}
			}

			log.Printf("Available CPU threads estimated to %d, memory to %d GB", cpu, memGB)
			reporter.Event("I", "runtime", "cpu threads estimated", map[string]any{"task_id": t.TaskId, "cpu": cpu, "mem_gb": memGB})
			executeTask(client, reporter, t, &wg, store, dm, cpu, memGB, workerName, noBare, serverAddr, workerToken, numaAlloc, numaOK)

			if numaOK {
				numaAllocator.Release(t.TaskId)
			}
			sem.ReleaseTask(t.TaskId) // always release same weight
			um.EnqueueTaskOutput(t)

			// execution finished; clear trackers
			executingTasks.Delete(t.TaskId)
			activeTasks.Delete(t.TaskId)
			activeSince.Delete(t.TaskId)
			log.Printf("🧹 Cleared local flags for task %d (done)", t.TaskId)

			// Clean up memory if task is done
			taskWeights.Delete(t.TaskId)

		}(task, weight)
	}
}

// / client launcher
func Run(ctx context.Context, serverAddr string, concurrency int32, name, store, token string, isPermanent bool, provider *string, region *string, maxCPU int32, maxMem float32, noBare ...bool) error {

	// Ensure store directory exists
	if err := os.MkdirAll(store, 0777); err != nil {
		store = "/tmp/" + store
		if err := os.MkdirAll(store, 0777); err != nil {
			return fmt.Errorf("could not create store directory %s: %v", store, err)
		}
		log.Printf("Using a temporary store.")
	}
	noBareFlag := len(noBare) > 0 && noBare[0]
	config := WorkerConfig{ServerAddr: serverAddr, Concurrency: concurrency, Name: name, Store: store, Token: token, IsPermanent: isPermanent, Provider: provider, Region: region, NoBare: noBareFlag, MaxCPU: maxCPU, MaxMem: maxMem}
	taskWeights := &sync.Map{}
	activeTasks := &sync.Map{}

	// Establish connection to the server
	qclient, err := lib.CreateClient(config.ServerAddr, config.Token)
	if err != nil {
		return fmt.Errorf("could not connect to server: %v", err)
	}
	defer qclient.Close()

	rcloneRemotes, err := qclient.Client.GetRcloneConfig(ctx, &emptypb.Empty{})
	if err != nil {
		event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "rclone", "Failed to get Rclone config", map[string]any{"error": err.Error()})
		return fmt.Errorf("could not get Rclone config: %v", err)
	}

	// Ensure docker credentials are present (write once)
	if _, err := os.Stat(install.DockerCredentialFile); os.IsNotExist(err) {
		log.Printf("⚠️ Docker credentials file not found, creating a new one.")
		creds, err := qclient.Client.GetDockerCredentials(ctx, &emptypb.Empty{})
		if err != nil {
			event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "docker", "Failed to get Docker credentials", map[string]any{"error": err.Error()})
			return fmt.Errorf("could not get Docker credentials: %v", err)
		}
		if err := install.InstallDockerCredentials(creds); err != nil {
			event.SendRuntimeEventWithRetry(config.ServerAddr, config.Token, 0, config.Name, "E", "docker", "Failed to install Docker credentials", map[string]any{"error": err.Error()})
			return fmt.Errorf("could not install Docker credentials: %v", err)
		}
	}

	config.registerWorker(qclient.Client)
	createHelpers(config.Store)
	sem := utils.NewResizableSemaphore(float64(config.Concurrency))

	// Shared live-resizable caps. Seeded from the CLI flags; the
	// fetchTasks loop reconciles them with the server's view on every
	// ping, so an operator can change them via UpdateWorker without
	// restarting the client.
	caps := NewLiveCaps(config.MaxCPU, config.MaxMem)

	// NUMA topology + slot manager. nil topology on a non-NUMA host
	// makes Allocate return ok=false, so tasks with `numa` set on such
	// hosts just run unbound with a single warning at dispatch.
	numaTopo := DiscoverNumaTopology()
	numaAlloc := NewNumaAllocator(numaTopo)
	if numaTopo != nil {
		log.Printf("🧩 NUMA topology: %d nodes detected", len(numaTopo.Nodes))
		// If a previous client process is still draining tasks
		// (e.g. systemd Restart=on-failure after an in-place upgrade),
		// inherit their NUMA assignments so we don't double-book a node.
		numaAlloc.RebuildFromDocker(ctx, config.Name)
	}
	// Per-step memory for the numa→concurrency auto-derivation in
	// fetchTasks. Stores the step_id we last derived for; the auto-
	// derive fires whenever the worker arrives on a *new* step
	// (covering the recycled-permanent-worker case where the recruiter
	// resets concurrency to its placeholder). A subsequent manual
	// `worker update --concurrency M` from the operator still wins:
	// the auto-derive won't re-fire on the same step again.
	var lastDerivedStep int32 // 0 = nothing derived yet

	reporter := event.NewReporter(qclient.Client, config.WorkerId, config.Name, 5*time.Second)
	defer reporter.StopOutbox()

	//TODO: we need to add somehow an error state (or we could update the task status in the task with the error notably in case download fails)

	// Launching download Manager
	dm := RunDownloader(store, reporter, rcloneRemotes)

	// Launching upload Manager
	um := RunUploader(store, qclient.Client, activeTasks, reporter, rcloneRemotes)

	// Launching execution thread
	go excuterThread(dm.ExecQueue, qclient.Client, reporter, sem, store, dm, um, taskWeights, activeTasks, config.Name, config.NoBare, config.ServerAddr, config.Token, caps, numaAlloc)

	// Start processing tasks
	go workerLoop(ctx, qclient.Client, reporter, config, caps, sem, dm, um, taskWeights, activeTasks, numaAlloc, &lastDerivedStep)

	// 🔎 Periodic diagnostics: detect tasks stuck active but not executing (likely in O)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// snapshot counts
				activeCount := 0
				activeIDs := make([]int32, 0, 64)
				activeTasks.Range(func(k, _ any) bool {
					activeCount++
					if len(activeIDs) < 64 {
						activeIDs = append(activeIDs, k.(int32))
					}
					return true
				})

				executingCount := 0
				executingTasks.Range(func(k, _ any) bool {
					executingCount++
					return true
				})

				// find stale candidates (>10min active, not executing)
				stale := make([]int32, 0, 64)
				cutoff := time.Now().Add(-10 * time.Minute)
				activeSince.Range(func(k, v any) bool {
					tid := k.(int32)
					if _, ok := executingTasks.Load(tid); ok {
						return true
					}
					if t0, ok := v.(time.Time); ok && t0.Before(cutoff) {
						if len(stale) < 64 {
							stale = append(stale, tid)
						}
					}
					return true
				})

				if len(stale) > 0 {
					log.Printf("🟥 STALE-O? active=%d executing=%d stale>=10m=%d (examples: %v)", activeCount, executingCount, len(stale), stale)
					reporter.Event("W", "diagnostics", "tasks active for >10m but not executing (possible O-stall)", map[string]any{
						"active_count":    activeCount,
						"executing_count": executingCount,
						"task_ids":        stale,
					})
				} else {
					log.Printf("📊 heartbeat: active=%d executing=%d (examples: %v)", activeCount, executingCount, activeIDs)
				}
			}
		}
	}()

	// Block until context is canceled
	<-ctx.Done()
	return ctx.Err()
}
