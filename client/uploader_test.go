package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scitq/scitq/client/event"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

func newTestUploadManager(t *testing.T) *UploadManager {
	t.Helper()
	return &UploadManager{
		Store:      t.TempDir(),
		Completion: make(chan *pb.Task, 1),
		reporter:   event.NewReporter(nil, 0, "", 0),
	}
}

func TestMissingOutputDirFailsSucceededTask(t *testing.T) {
	um := newTestUploadManager(t)
	out := "s3://bucket/out/"
	task := &pb.Task{TaskId: 42, Status: "S", Output: &out}

	um.EnqueueTaskOutput(task)
	got := <-um.Completion

	if got.Status != "F" {
		t.Fatalf("a succeeded task with no output directory must become F, got %q", got.Status)
	}
	if got.FailureClass == nil || *got.FailureClass != "other" {
		t.Fatalf("expected failure_class other (retryable), got %v", got.FailureClass)
	}
}

func TestMissingOutputDirKeepsFailedTaskFailed(t *testing.T) {
	um := newTestUploadManager(t)
	out := "s3://bucket/out/"
	oom := "oom"
	task := &pb.Task{TaskId: 43, Status: "F", Output: &out, FailureClass: &oom}

	um.EnqueueTaskOutput(task)
	got := <-um.Completion

	if got.Status != "F" || got.FailureClass == nil || *got.FailureClass != "oom" {
		t.Fatalf("an already-failed task keeps its status and class, got %q / %v", got.Status, got.FailureClass)
	}
}

func TestEmptyOutputDirKeepsSuccess(t *testing.T) {
	um := newTestUploadManager(t)
	out := "s3://bucket/out/"
	task := &pb.Task{TaskId: 44, Status: "S", Output: &out}
	if err := os.MkdirAll(filepath.Join(um.Store, "tasks", "44", "output"), 0o755); err != nil {
		t.Fatal(err)
	}

	um.EnqueueTaskOutput(task)
	got := <-um.Completion

	if got.Status != "S" {
		t.Fatalf("a program that legitimately wrote nothing stays S, got %q", got.Status)
	}
}
