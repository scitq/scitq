package integration_test

import (
	"context"
	"strings"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestWorkerUpgradeStatus exercises the Phase I detection plumbing:
// workers report their build identity on RegisterWorker and the server
// projects an `upgrade_status` on ListWorkers based on the comparison
// with its own commit.
//
// The test runs four scenarios in subtests:
//   - same arch + same commit  → "up_to_date"
//   - same arch + diff commit  → "needs_upgrade"
//   - diff arch (linux/arm64)  → "unsupported_arch"
//   - missing commit altogether → "unknown"
//
// See specs/worker_autoupgrade.md.
func TestWorkerUpgradeStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	// Admin client (for ListWorkers).
	var adm cli.CLI
	adm.Attr.Server = serverAddr
	out, err := runCLICommand(adm, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	admToken := strings.TrimSpace(out)
	admClient, err := lib.CreateClient(serverAddr, admToken)
	require.NoError(t, err)
	defer admClient.Close()

	// Worker-token client (for RegisterWorker).
	wkClient, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wkClient.Close()

	// Snapshot the server's own commit. If empty (test build with no VCS
	// stamp) we skip the strict status assertions for the "matched" cases
	// since the server's "unknown" path swallows them — but we can still
	// exercise the field-persistence and the unsupported-arch branch.
	srvVer, err := admClient.Client.ServerVersion(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	serverCommit := srvVer.GetCommit()
	t.Logf("Server commit reported by ServerVersion: %q", serverCommit)

	register := func(t *testing.T, name, ver, commit, arch string) {
		t.Helper()
		concurrency := int32(1)
		req := &pb.WorkerInfo{
			Name:        name,
			Concurrency: &concurrency,
		}
		if ver != "" {
			v := ver
			req.Version = &v
		}
		if commit != "" {
			c := commit
			req.Commit = &c
		}
		if arch != "" {
			a := arch
			req.BuildArch = &a
		}
		_, err := wkClient.Client.RegisterWorker(ctx, req)
		require.NoError(t, err)
	}

	findWorker := func(t *testing.T, name string) *pb.Worker {
		t.Helper()
		list, err := admClient.Client.ListWorkers(ctx, &pb.ListWorkersRequest{})
		require.NoError(t, err)
		for _, w := range list.Workers {
			if w.Name == name {
				return w
			}
		}
		t.Fatalf("worker %q not found in ListWorkers response", name)
		return nil
	}

	t.Run("up_to_date when commits match on linux/amd64", func(t *testing.T) {
		if serverCommit == "" {
			t.Skip("server has no VCS-stamped commit (test build); cannot exercise the matched-commit path")
		}
		register(t, "wv-uptodate", "v1.0.0-test", serverCommit, "linux/amd64")
		w := findWorker(t, "wv-uptodate")
		require.Equal(t, "v1.0.0-test", w.GetVersion())
		require.Equal(t, serverCommit, w.GetCommit())
		require.Equal(t, "linux/amd64", w.GetBuildArch())
		require.Equal(t, "up_to_date", w.GetUpgradeStatus())
	})

	t.Run("needs_upgrade when commits differ on linux/amd64", func(t *testing.T) {
		if serverCommit == "" {
			t.Skip("server has no VCS-stamped commit; can't form a 'different' commit deterministically")
		}
		register(t, "wv-stale", "v0.9.0-test", "deadbeef00000000000000000000000000000000", "linux/amd64")
		w := findWorker(t, "wv-stale")
		require.Equal(t, "deadbeef00000000000000000000000000000000", w.GetCommit())
		require.Equal(t, "needs_upgrade", w.GetUpgradeStatus())
	})

	t.Run("unsupported_arch on linux/arm64 even when commits match", func(t *testing.T) {
		// This branch doesn't depend on the server commit — the arch check
		// short-circuits before commit comparison.
		register(t, "wv-arm64", "v1.0.0-test", "0123456789abcdef0123456789abcdef01234567", "linux/arm64")
		w := findWorker(t, "wv-arm64")
		require.Equal(t, "linux/arm64", w.GetBuildArch())
		require.Equal(t, "unsupported_arch", w.GetUpgradeStatus())
	})

	t.Run("unknown when worker does not report build identity", func(t *testing.T) {
		register(t, "wv-preupgrade", "", "", "")
		w := findWorker(t, "wv-preupgrade")
		require.Equal(t, "", w.GetVersion())
		require.Equal(t, "", w.GetCommit())
		require.Equal(t, "", w.GetBuildArch())
		require.Equal(t, "unknown", w.GetUpgradeStatus())
	})
}

// TestServerVersionRPC verifies the RPC the CLI uses to detect a version
// mismatch on every command.
func TestServerVersionRPC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	tok := strings.TrimSpace(out)

	qc, err := lib.CreateClient(serverAddr, tok)
	require.NoError(t, err)
	defer qc.Close()

	resp, err := qc.Client.ServerVersion(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	// Version is always at least the package default `dev`.
	require.NotEmpty(t, resp.GetVersion(), "ServerVersion.Version should never be empty")
	// build_arch is always a real GOOS/GOARCH combo.
	require.Contains(t, resp.GetBuildArch(), "/", "ServerVersion.BuildArch should be GOOS/GOARCH")
	// `urgent` is reserved for Phase II; Phase I always emits false.
	require.False(t, resp.GetUrgent(), "Phase I server should report Urgent=false")
}
