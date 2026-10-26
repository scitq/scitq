package fetch

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

func testRemotes() *pb.RcloneRemotes {
	return &pb.RcloneRemotes{Remotes: map[string]*pb.RcloneRemote{
		"loc": {Options: map[string]string{"type": "local"}},
		"s3":  {Options: map[string]string{"type": "s3", "provider": "Minio", "endpoint": "http://127.0.0.1:1"}},
	}}
}

// TestConcurrentOperationsShareOneConfig: operations running in parallel —
// a worker's downloads and uploads — use one config file that stays in
// place, so rclone never finds its config missing (it exits the process
// when that happens). Run with -race: rclone's config path is a
// process-wide variable.
func TestConcurrentOperationsShareOneConfig(t *testing.T) {
	defer Cleanup()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := List(testRemotes(), dir+"/"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("list failed: %v", err)
	}

	configMu.Lock()
	path := configFile
	configMu.Unlock()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file %q gone after the operations: %v", path, err)
	}
}

// TestSameRemotesReuseTheConfigFile: the file is written once for a given
// set of remotes, whatever the order Go iterates their maps in.
func TestSameRemotesReuseTheConfigFile(t *testing.T) {
	defer Cleanup()
	if err := loadConfigFromMemory(testRemotes()); err != nil {
		t.Fatal(err)
	}
	first := configFile
	for i := 0; i < 20; i++ {
		if err := loadConfigFromMemory(testRemotes()); err != nil {
			t.Fatal(err)
		}
		if configFile != first {
			t.Fatalf("config rewritten for identical remotes: %q then %q", first, configFile)
		}
	}

	changed := testRemotes()
	changed.Remotes["other"] = &pb.RcloneRemote{Options: map[string]string{"type": "local"}}
	if err := loadConfigFromMemory(changed); err != nil {
		t.Fatal(err)
	}
	if configFile == first {
		t.Fatal("config not rewritten when the remotes changed")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("previous config file %q not removed", first)
	}
}
