package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDocker puts a `docker` on PATH that logs its arguments and prints out.
func fakeDocker(t *testing.T, out string) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\nprintf '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func TestTaskContainersAreFoundByName(t *testing.T) {
	calls := fakeDocker(t, `scitq-bioit3-task-1387120\nscitq-bioit3-task-1387121\nscitq-bioit3-task-x\n`)
	got, err := taskContainers("bioit3")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1387120] != "scitq-bioit3-task-1387120" || got[1387121] != "scitq-bioit3-task-1387121" {
		t.Fatalf("containers %v", got)
	}
	// The worker name is matched literally, not as a pattern.
	calls = fakeDocker(t, ``)
	if _, err := taskContainers("w.1"); err != nil {
		t.Fatal(err)
	}
	if log, _ := os.ReadFile(calls); string(log) != "ps -a --filter name=^/?scitq-w\\.1-task- --format {{.Names}}\n" {
		t.Fatalf("docker calls:\n%s", log)
	}
}

func TestReplayStartsBeforeThePreviousClientsLastPass(t *testing.T) {
	store := t.TempDir()
	if since := replaySince(store); since != "" {
		t.Fatalf("no previous client: replay from %q, want the whole log", since)
	}
	markAlive(store)
	since, err := time.Parse(time.RFC3339, replaySince(store))
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(since); age < aliveMargin-time.Second || age > aliveMargin+2*time.Second {
		t.Fatalf("replay starts %v ago, want about %v", age, aliveMargin)
	}
}

func TestReattachedOutcome(t *testing.T) {
	for state, want := range map[string]string{
		"exited|0|2026-11-13T06:40:00Z|false":  "",
		"exited|3|2026-11-13T06:40:00Z|false":  "container exited with code 3",
		"exited|137|2026-11-13T06:40:00Z|true": "container exited with code 137",
		"running|0|0001-01-01T00:00:00Z|false": "did not run to an end",
		"created|0|0001-01-01T00:00:00Z|false": "did not run to an end",
	} {
		fakeDocker(t, state+`\n`)
		hooks := &taskHooks{}
		err := reattachedOutcome("c", hooks)
		switch {
		case want == "" && err != nil, want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: got %v, want %q", state, err, want)
		}
		if hooks.oomKilled.Load() != strings.HasSuffix(state, "true") {
			t.Errorf("%s: oomKilled %v", state, hooks.oomKilled.Load())
		}
	}
}
