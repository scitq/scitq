package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
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

// Track how long tasks have been locally active and whether they are executing
var activeSince sync.Map    // map[int32]time.Time
var executingTasks sync.Map // map[int32]struct{}

// auto detect self specs
func (w *WorkerConfig) registerSpecs(client pb.TaskQueueClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. CPU
	cpuCount := int32(runtime.NumCPU())

	// 2. Memory
	vmem, err := mem.VirtualMemory()
	if err != nil {
		log.Printf("⚠️ Could not detect memory: %v", err)
		return
	}
	totalMem := float32(vmem.Total) / (1024 * 1024 * 1024)

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
func executeTask(client pb.TaskQueueClient, reporter *event.Reporter, task *pb.Task, wg *sync.WaitGroup, store string, dm *DownloadManager, cpu int32, memGB int32, workerName string, noBare bool) {
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
	// Promote to Running locally and on the server
	reporter.Event("I", "phase", "promoting to R (start exec)", map[string]any{
		"task_id": task.TaskId,
	})
	task.Status = "R"
	reporter.UpdateTaskAsync(task.TaskId, "R", "", nil)

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
		reporter.UpdateTask(task.TaskId, "V", message)
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
		log.Printf("🛠️  Running bare command: %s -c %s", shell, task.Command)
	} else {
		// Docker execution: existing logic (containerName hoisted above).
		// `--rm` was removed deliberately: keeping the container in stopped
		// state until executeTask explicitly removes it lets recoverTask
		// query State.ExitCode via `docker inspect` if it ever needs to
		// SIGKILL the cmd. The defer below is responsible for cleanup so
		// the daemon doesn't accumulate stopped containers.
		command := []string{"run", "--name", containerName, "-e", fmt.Sprintf("CPU=%d", cpu), "-e", fmt.Sprintf("THREADS=%d", cpu), "-e", fmt.Sprintf("MEM=%d", memGB)}
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
				reporter.UpdateTask(task.TaskId, "V", message)
				return
			}
			if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
				message := fmt.Sprintf("Failed to write script file for task %d: %v", task.TaskId, err)
				log.Printf("❌ %s", message)
				reporter.Event("E", "task", message, map[string]any{"task_id": task.TaskId, "command": task.Command})
				task.Status = "F"
				reporter.UpdateTask(task.TaskId, "V", message)
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
					reporter.UpdateTask(task.TaskId, "V", message)
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
		reporter.UpdateTask(task.TaskId, "V", message) // Mark as failed
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
		reporter.UpdateTask(task.TaskId, "V", message) // Mark as failed
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
		reporter.UpdateTaskAsync(task.TaskId, "V", "", &sec) // Mark as failed
	} else {
		log.Printf("✅ Task %d completed successfully", task.TaskId)
		task.Status = "S" // Mark as success
		reporter.UpdateTaskAsync(task.TaskId, "U", "", &sec)
	}
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

// fetchTasks requests new tasks from the server. Returns the task list
// and the operator-triggered upgrade flag (if any) carried in the same
// ping response.
func (w *WorkerConfig) fetchTasks(
	ctx context.Context,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	id int32,
	sem *utils.ResizableSemaphore,
	taskWeights *sync.Map,
	activeTasks *sync.Map,
) ([]*pb.Task, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var query *pb.PingAndGetNewTasksRequest
	ws, err := workerstats.CollectWorkerStats()
	if err != nil {
		log.Printf("⚠️ Error could not collect stats: %v", err)
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id}
	} else {
		query = &pb.PingAndGetNewTasksRequest{WorkerId: id, Stats: ws.ToProto()}
	}

	res, err := client.PingAndTakeNewTasks(ctx, query)
	if err != nil {
		log.Printf("⚠️ Error calling fetch tasks: %v", err)
		return nil, "", err
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

	// Execute signals (K=SIGKILL via docker kill, T=SIGTERM via docker stop)
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
					log.Printf("🔪 Killing bare task %d (SIGKILL to pgid %d)", sig.TaskId, p.Pid)
					syscall.Kill(pgid, syscall.SIGKILL)
				}
				return
			}
			// Docker container signal
			containerName := fmt.Sprintf("scitq-%s-task-%d", w.Name, sig.TaskId)
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
						signalRecover(sig.TaskId, "docker stop reported container already gone")
					}
				}
			} else {
				log.Printf("🔪 Killing container %s (task %d, SIGKILL)", containerName, sig.TaskId)
				if out, err := exec.Command("docker", "kill", containerName).CombinedOutput(); err != nil {
					outStr := strings.TrimSpace(string(out))
					log.Printf("⚠️ docker kill %s failed: %v (%s)", containerName, err, outStr)
					if strings.Contains(outStr, "No such container") {
						signalRecover(sig.TaskId, "docker kill reported container already gone")
					}
				}
			}
		}()
	}

	return res.Tasks, res.UpgradeRequested, nil
}

// workerLoop continuously fetches and executes tasks in parallel.
func workerLoop(ctx context.Context, client pb.TaskQueueClient, reporter *event.Reporter, config WorkerConfig, sem *utils.ResizableSemaphore, dm *DownloadManager, um *UploadManager, taskWeights *sync.Map, activeTasks *sync.Map) {
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
					reporter.UpdateTask(tid, "F", msg)
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
					reporter.UpdateTask(tid, "F", msg)
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

		tasks, upgradeReq, err := config.fetchTasks(ctx, client, reporter, config.WorkerId, sem, taskWeights, activeTasks)
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
		upgradeSup.tick(ctx, client, reporter, &config, activeTasks, upgradeReq)

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
			activeTasks.Store(task.TaskId, struct{}{})
			activeSince.Store(task.TaskId, now)
			reporter.UpdateTaskAsync(task.TaskId, task.Status, "", nil)
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
					reporter.UpdateTask(task.TaskId, "F", fmt.Sprintf("failed to create directory %s for task %d: %v", folder, task.TaskId, err))
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
				reporter.UpdateTask(t.TaskId, "F", message)
				wg.Done()
				return
			}
			executingTasks.Store(t.TaskId, struct{}{})
			reporter.Event("I", "phase", "acquired slot; promoting to R soon", map[string]any{
				"task_id": t.TaskId,
				"weight":  w,
			})
			log.Printf("✅ Acquired semaphore for task %d (weight=%.3f)", t.TaskId, w)

			cpu := max(int32(float64(runtime.NumCPU())/sem.Size()), 1)
			var memGB int32
			if vmem, err := mem.VirtualMemory(); err == nil {
				memGB = max(int32(float64(vmem.Total)/(1024*1024*1024)/sem.Size()), 1)
			}
			log.Printf("Available CPU threads estimated to %d, memory to %d GB", cpu, memGB)
			reporter.Event("I", "runtime", "cpu threads estimated", map[string]any{"task_id": t.TaskId, "cpu": cpu, "mem_gb": memGB})
			executeTask(client, reporter, t, &wg, store, dm, cpu, memGB, workerName, noBare)

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
func Run(ctx context.Context, serverAddr string, concurrency int32, name, store, token string, isPermanent bool, provider *string, region *string, noBare ...bool) error {

	// Ensure store directory exists
	if err := os.MkdirAll(store, 0777); err != nil {
		store = "/tmp/" + store
		if err := os.MkdirAll(store, 0777); err != nil {
			return fmt.Errorf("could not create store directory %s: %v", store, err)
		}
		log.Printf("Using a temporary store.")
	}
	noBareFlag := len(noBare) > 0 && noBare[0]
	config := WorkerConfig{ServerAddr: serverAddr, Concurrency: concurrency, Name: name, Store: store, Token: token, IsPermanent: isPermanent, Provider: provider, Region: region, NoBare: noBareFlag}
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

	reporter := event.NewReporter(qclient.Client, config.WorkerId, config.Name, 5*time.Second)
	defer reporter.StopOutbox()

	//TODO: we need to add somehow an error state (or we could update the task status in the task with the error notably in case download fails)

	// Launching download Manager
	dm := RunDownloader(store, reporter, rcloneRemotes)

	// Launching upload Manager
	um := RunUploader(store, qclient.Client, activeTasks, reporter, rcloneRemotes)

	// Launching execution thread
	go excuterThread(dm.ExecQueue, qclient.Client, reporter, sem, store, dm, um, taskWeights, activeTasks, config.Name, config.NoBare)

	// Start processing tasks
	go workerLoop(ctx, qclient.Client, reporter, config, sem, dm, um, taskWeights, activeTasks)

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
