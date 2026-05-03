package server

import "testing"

func TestWorkerUpgradeStatus(t *testing.T) {
	cases := []struct {
		name         string
		workerArch   string
		workerCommit string
		serverCommit string
		want         string
	}{
		{"same commit on supported arch", "linux/amd64", "abc123", "abc123", UpgradeStatusUpToDate},
		{"different commit on supported arch", "linux/amd64", "abc123", "def456", UpgradeStatusNeedsUpgrade},
		{"different commit but unsupported arch", "linux/arm64", "abc123", "def456", UpgradeStatusUnsupportedArch},
		{"same commit but unsupported arch", "linux/arm64", "abc123", "abc123", UpgradeStatusUnsupportedArch},
		{"missing worker arch", "", "abc123", "def456", UpgradeStatusUnknown},
		{"missing worker commit", "linux/amd64", "", "def456", UpgradeStatusUnknown},
		{"missing server commit (dev build)", "linux/amd64", "abc123", "", UpgradeStatusUnknown},
		{"darwin/amd64 dev worker", "darwin/amd64", "abc123", "def456", UpgradeStatusUnsupportedArch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WorkerUpgradeStatus(tc.workerArch, tc.workerCommit, tc.serverCommit)
			if got != tc.want {
				t.Errorf("WorkerUpgradeStatus(%q, %q, %q) = %q, want %q",
					tc.workerArch, tc.workerCommit, tc.serverCommit, got, tc.want)
			}
		})
	}
}
