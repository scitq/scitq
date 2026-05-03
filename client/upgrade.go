package client

// Phase II of the worker version-awareness feature: operator-triggered
// upgrade. The server latches an upgrade request onto a worker via the
// upgrade_requested column; the worker reads it from each ping and acts.
// See specs/worker_autoupgrade.md.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/scitq/scitq/client/event"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/internal/version"

	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// emergencyDrainTimeout caps how long an emergency drain will wait for
// in-flight tasks to finish before forcibly upgrading. The operator
// chose emergency knowing this; tasks killed past the cap are marked
// failed and re-scheduled by normal recruitment.
const emergencyDrainTimeout = 30 * time.Minute

const supportedUpgradeArch = "linux/amd64"

type upgradeSupervisor struct {
	mu               sync.Mutex
	mode             string // current latched mode: "", "normal", "emergency"
	emergencyStarted time.Time
	statusSet        bool // we have already issued UpdateWorkerStatus("X") for emergency
	announced        bool // we have already emitted the lifecycle event for the latch
}

func newUpgradeSupervisor() *upgradeSupervisor {
	return &upgradeSupervisor{}
}

// tick is called once per worker-loop iteration after a successful ping.
// It absorbs the server-reported flag, walks the state machine, and
// invokes performUpgrade once the worker is ready. performUpgrade exits
// the process on success; tick only returns when no upgrade happens
// yet (still waiting, hash mismatch retried later, unsupported arch).
func (u *upgradeSupervisor) tick(
	ctx context.Context,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	config *WorkerConfig,
	activeTasks *sync.Map,
	requested string,
) {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Absorb the latest server-reported flag. The server clears the
	// column when the worker re-registers on the matching commit, so
	// "" here means either no request or the request is fulfilled.
	if requested != u.mode {
		u.statusSet = false
		u.announced = false
		u.emergencyStarted = time.Time{}
		u.mode = requested
	}
	if u.mode == "" {
		return
	}

	// Architecture gate: refuse politely on non-amd64. Leave the latch
	// in place so the request stays visible in `worker list`; operator
	// clears it with `--cancel`.
	thisArch := runtime.GOOS + "/" + runtime.GOARCH
	if thisArch != supportedUpgradeArch {
		if !u.announced {
			reporter.Event("W", "lifecycle", "operator requested upgrade but worker arch is not supported", map[string]any{
				"requested_mode": u.mode,
				"worker_arch":    thisArch,
				"supported":      supportedUpgradeArch,
			})
			u.announced = true
		}
		return
	}

	if !u.announced {
		reporter.Event("I", "lifecycle", fmt.Sprintf("upgrade request received: %s", u.mode), map[string]any{
			"mode":         u.mode,
			"worker_arch":  thisArch,
			"build_commit": currentCommit(),
		})
		u.announced = true
	}

	idle := activeTaskCount(activeTasks) == 0

	switch u.mode {
	case "normal":
		// Wait for natural idle. Keep accepting tasks until then.
		if !idle {
			return
		}
	case "emergency":
		if !u.statusSet {
			u.emergencyStarted = time.Now()
			// Setting status to anything other than 'R' makes the
			// server's task-assignment query skip this worker. 'X'
			// is unused elsewhere — see assigntask.go's `w.status='R'`
			// filter — and the watchdog treats unknown statuses as
			// no-op on ping (see WorkerPinged switch).
			if _, err := client.UpdateWorkerStatus(ctx, &pb.WorkerStatus{
				WorkerId: config.WorkerId,
				Status:   "X",
			}); err != nil {
				log.Printf("⚠️ Could not set draining status on worker %d: %v", config.WorkerId, err)
			}
			u.statusSet = true
			reporter.Event("I", "lifecycle", "emergency drain started", map[string]any{
				"timeout_seconds": int(emergencyDrainTimeout.Seconds()),
			})
		}
		if !idle && time.Since(u.emergencyStarted) < emergencyDrainTimeout {
			return
		}
		if !idle {
			killRemainingTasks(activeTasks, reporter, config.Name)
		}
	default:
		// Unknown mode (server added one this older worker doesn't
		// understand). Stay safe: don't act.
		return
	}

	// Ready to upgrade.
	if err := performUpgrade(ctx, client, reporter, config, u.mode); err != nil {
		log.Printf("⚠️ Upgrade attempt failed: %v", err)
		reporter.Event("E", "lifecycle", "upgrade attempt failed", map[string]any{
			"mode":  u.mode,
			"error": err.Error(),
		})
	}
}

func activeTaskCount(activeTasks *sync.Map) int {
	n := 0
	activeTasks.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// killRemainingTasks force-kills any docker containers / bare processes
// the worker still tracks. Called when an emergency drain hits its hard
// timeout. Best-effort; the post-restart server reconciliation will
// mark the killed tasks F via normal status flow.
func killRemainingTasks(activeTasks *sync.Map, reporter *event.Reporter, workerName string) {
	activeTasks.Range(func(key, _ any) bool {
		taskID := key.(int32)
		containerName := fmt.Sprintf("scitq-%s-task-%d", workerName, taskID)
		log.Printf("⏰ Emergency drain timeout: force-killing task %d (container %s)", taskID, containerName)
		reporter.Event("W", "lifecycle", "emergency drain timeout: force-killing task", map[string]any{
			"task_id":   taskID,
			"container": containerName,
		})
		_ = exec.Command("docker", "kill", containerName).Run()
		bareKey := fmt.Sprintf("%s:%d", workerName, taskID)
		if proc, ok := bareProcesses.Load(bareKey); ok {
			p := proc.(*os.Process)
			_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
		return true
	})
}

// performUpgrade fetches the new binary, verifies its checksum, swaps
// it in atomically, then exits. The supervisor (systemd, docker
// --restart, the launch script) brings the new binary back up.
//
// Returns:
//   - nil : never (process exits before this returns).
//   - err : upgrade did not happen; caller leaves the latch in place
//     and retries on the next tick.
func performUpgrade(
	ctx context.Context,
	client pb.TaskQueueClient,
	reporter *event.Reporter,
	config *WorkerConfig,
	mode string,
) error {
	info, err := client.GetClientUpgradeInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("GetClientUpgradeInfo: %w", err)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	target := filepath.Join(filepath.Dir(self), filepath.Base(self)+".new")

	expected, err := fetchExpectedHash(info.Sha256Url, info.InsecureSkipVerify)
	if err != nil {
		return fmt.Errorf("fetch sha256: %w", err)
	}
	got, err := downloadToFileAndHash(info.BinaryUrl, target, info.InsecureSkipVerify)
	if err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("download binary: %w", err)
	}
	if got != expected {
		_ = os.Remove(target)
		return fmt.Errorf("hash mismatch: got %s, expected %s", got, expected)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("chmod %s: %w", target, err)
	}

	reporter.Event("I", "lifecycle", "upgrade ready: swapping binary and exiting for supervisor restart", map[string]any{
		"mode":        mode,
		"from_commit": currentCommit(),
		"to_sha256":   expected,
		"binary_path": self,
	})

	// Atomic rename. On Linux, unlink-while-open keeps the running
	// process alive (we still hold an FD on the old inode); the
	// supervisor relaunches the path and gets the new binary.
	if err := os.Rename(target, self); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("rename %s -> %s: %w", target, self, err)
	}

	log.Printf("🚀 Upgrade in place; exiting for supervisor restart.")
	os.Exit(0)
	return nil // unreachable
}

func fetchExpectedHash(url string, insecure bool) (string, error) {
	resp, err := newHTTPClient(insecure).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func downloadToFileAndHash(url, dest string, insecure bool) (string, error) {
	resp, err := newHTTPClient(insecure).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newHTTPClient(insecure bool) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}
	return &http.Client{
		Transport: tr,
		Timeout:   5 * time.Minute,
	}
}

// currentCommit returns the worker's own commit SHA at runtime. Mirrors
// registerWorker so audit-trail events reference the same string the
// server has on file.
func currentCommit() string {
	if vcs, ok := version.ReadVCS(); ok && vcs.Revision != "" {
		return vcs.Revision
	}
	if version.Commit != "" && version.Commit != "none" {
		return version.Commit
	}
	return ""
}
