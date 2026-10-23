package client

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/scitq/scitq/client/event"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

func newTestUploadManager(t *testing.T) *UploadManager {
	t.Helper()
	return &UploadManager{
		Store:         t.TempDir(),
		UploadQueue:   make(chan *UploadFile, 16),
		Completion:    make(chan *pb.Task, 16),
		FailedQueue:   make(chan *UploadFailure, 16),
		reporter:      event.NewReporter(nil, 0, "", 0),
		RcloneRemotes: &pb.RcloneRemotes{},
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
	_, term := um.FinishTask(got)
	if term.ProducedFiles == nil || *term.ProducedFiles != 0 || term.UploadedFiles == nil || *term.UploadedFiles != 0 {
		t.Fatalf("an empty output must be reported as 0 files produced and uploaded, got %+v", term)
	}
}

// writeOutputs creates the task's output directory with the given files
// (relative path → content) and returns a local upload target directory.
func writeOutputs(t *testing.T, um *UploadManager, taskID int32, files map[string]string) string {
	t.Helper()
	outDir := filepath.Join(um.Store, "tasks", fmt.Sprint(taskID), "output")
	for rel, content := range files {
		p := filepath.Join(outDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return t.TempDir() + "/"
}

func TestOutputManifestCountsVerifiedUploads(t *testing.T) {
	um := newTestUploadManager(t)
	target := writeOutputs(t, um, 45, map[string]string{"a.tsv": "hello", "sub/b.json": "{}"})
	task := &pb.Task{TaskId: 45, Status: "S", Output: &target}

	um.EnqueueTaskOutput(task)
	for i := 0; i < 2; i++ {
		if err := um.uploadFile(<-um.UploadQueue); err != nil {
			t.Fatalf("upload failed: %v", err)
		}
	}
	<-um.Completion
	<-um.Completion

	_, term := um.FinishTask(task)
	if *term.ProducedFiles != 2 || *term.ProducedBytes != 7 || *term.UploadedFiles != 2 {
		t.Fatalf("expected 2 files / 7 bytes / 2 uploaded, got %d / %d / %d",
			*term.ProducedFiles, *term.ProducedBytes, *term.UploadedFiles)
	}
	if b, err := os.ReadFile(filepath.Join(target, "sub", "b.json")); err != nil || string(b) != "{}" {
		t.Fatalf("nested output not uploaded: %v %q", err, b)
	}
}

func TestVerifyUploadRejectsSizeMismatch(t *testing.T) {
	um := newTestUploadManager(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "a.tsv")
	if err := os.WriteFile(target, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := um.verifyUpload(&UploadFile{TargetPath: target, Size: 5}); err != nil {
		t.Fatalf("matching size must verify, got %v", err)
	}
	if err := um.verifyUpload(&UploadFile{TargetPath: target, Size: 6}); err == nil {
		t.Fatal("a size mismatch must fail verification")
	}
	if err := um.verifyUpload(&UploadFile{TargetPath: filepath.Join(dir, "missing.tsv"), Size: 1}); err == nil {
		t.Fatal("a missing object must fail verification")
	}
}

func TestFailTaskReportsOncePerTask(t *testing.T) {
	um := newTestUploadManager(t)
	task := &pb.Task{TaskId: 46, Status: "S"}
	um.manifests.Store(task.TaskId, &outputManifest{files: 3, bytes: 30, targets: 1})
	um.manifest(task.TaskId).uploadedPairs.Add(1)

	um.failTask(task, "first")
	um.failTask(task, "second")
	if n := len(um.FailedQueue); n != 1 {
		t.Fatalf("expected one failure report, got %d", n)
	}

	_, term := um.FinishTask(task)
	if *term.ProducedFiles != 3 || *term.UploadedFiles != 1 {
		t.Fatalf("the failure must report the shortfall (3 produced, 1 uploaded), got %+v", term)
	}
	um.failTask(task, "straggler after the task ended")
	if n := len(um.FailedQueue); n != 1 {
		t.Fatalf("a failure after the task ended must not be reported, got %d reports", n)
	}
}
