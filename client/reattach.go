package client

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scitq/scitq/client/event"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// A client restart (an upgrade of the host, a crash) leaves the containers
// of its running tasks behind: Docker runs them, not the client. The next
// client finds them by name, reports their task ids when it registers, and
// re-attaches to those the server still assigns to it (reattached); it
// removes the others, whose result nobody would collect.

// reattached holds the tasks to re-attach to, mapped to the time from
// which their logs are replayed ("" for the whole log). executeTask
// consumes the entry.
var reattached sync.Map // task id → string

// aliveFile is touched by every pass of the worker loop. Its time tells a
// restarted client when the previous one stopped forwarding logs.
const aliveFile = "client.alive"

// aliveMargin is replayed before the previous client's last sign of life:
// the loop passes every few seconds, so lines may repeat but none is lost.
const aliveMargin = 10 * time.Second

// taskContainers lists this worker's task containers ("scitq-<worker>-
// task-<id>", running or stopped), by task id.
func taskContainers(workerName string) (map[int32]string, error) {
	prefix := "scitq-" + workerName + "-task-"
	filter := "name=^/?" + regexp.QuoteMeta(prefix)
	out, err := exec.Command("docker", "ps", "-a", "--filter", filter, "--format", "{{.Names}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	containers := map[int32]string{}
	for _, name := range strings.Fields(string(out)) {
		id, err := strconv.ParseInt(strings.TrimPrefix(name, prefix), 10, 32)
		if err != nil || !strings.HasPrefix(name, prefix) {
			continue
		}
		containers[int32(id)] = name
	}
	return containers, nil
}

// removeContainers removes the given containers, running or not.
func removeContainers(names []string) error {
	if len(names) == 0 {
		return nil
	}
	if out, err := exec.Command("docker", append([]string{"rm", "-f"}, names...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("docker rm: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// markAlive records that this client is forwarding logs.
func markAlive(store string) {
	now := time.Now()
	path := filepath.Join(store, aliveFile)
	if err := os.Chtimes(path, now, now); os.IsNotExist(err) {
		_ = os.WriteFile(path, nil, 0644)
	}
}

// replaySince is the time from which a restarted client replays the logs of
// the containers it re-attaches to, as `docker logs --since` takes it; ""
// (the whole log) when the previous client left no sign of life.
func replaySince(store string) string {
	info, err := os.Stat(filepath.Join(store, aliveFile))
	if err != nil {
		return ""
	}
	return info.ModTime().Add(-aliveMargin).UTC().Format(time.RFC3339)
}

// reattachTasks queues the kept tasks to the executor, which follows their
// containers instead of launching them (executeTask). It runs before the
// first ping, so the server finds them among the tasks this worker knows.
// A task the server cannot describe is given up: its container is removed.
func reattachTasks(client pb.TaskQueueClient, reporter *event.Reporter, kept []int32, containers map[int32]string,
	store string, queue chan<- *pb.Task, activeTasks, taskWeights *sync.Map) {
	since := replaySince(store)
	for _, id := range kept {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		task, err := client.GetTask(ctx, &pb.TaskId{TaskId: id})
		cancel()
		if err != nil {
			log.Printf("⚠️ Cannot re-attach to task %d (%v): removing its container", id, err)
			_ = removeContainers([]string{containers[id]})
			continue
		}
		reattached.Store(id, since)
		activeTasks.Store(id, task)
		if task.Weight != nil {
			taskWeights.Store(id, *task.Weight)
		}
		log.Printf("🔗 Re-attaching to task %d (container %s, logs since %q)", id, containers[id], since)
		reporter.Event("I", "lifecycle", "re-attached to task container left by a previous client run", map[string]any{
			"task_id":   id,
			"container": containers[id],
			"since":     since,
		})
		queue <- task
	}
}

// containerStartedAt is when the container started, or the zero time.
func containerStartedAt(name string) time.Time {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.StartedAt}}", name).Output()
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
	return t
}

// reattachedOutcome reads how a re-attached container ended: nil for a
// clean exit, an error otherwise. It records an OOM kill on hooks.
func reattachedOutcome(name string, hooks *taskHooks) error {
	out, err := exec.Command("docker", "inspect", "-f", containerStateFormat, name).Output()
	if err != nil {
		return fmt.Errorf("re-attached container %s: %w", name, err)
	}
	end := parseContainerState(string(out))
	if end.oomKilled {
		hooks.oomKilled.Store(true)
	}
	switch {
	case !end.finished:
		return fmt.Errorf("re-attached container %s did not run to an end (state %q)", name, strings.TrimSpace(string(out)))
	case end.exitCode != 0:
		return fmt.Errorf("container exited with code %d", end.exitCode)
	}
	return nil
}
