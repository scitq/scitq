package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// startServerWithClientBinary runs startServerForTest after seeding a
// fake client binary in a temp dir and pointing the server config at
// it. Returns the binary path so the test can verify SHA-256 against
// known content.
func startServerWithClientBinary(t *testing.T, content []byte) (serverAddr, workerToken, adminUser, adminPassword, binaryPath string, httpBase string, cleanup func()) {
	t.Helper()

	dir := t.TempDir()
	binaryPath = filepath.Join(dir, "scitq-client")
	require.NoError(t, os.WriteFile(binaryPath, content, 0o755))

	override := &config.Config{}
	override.Scitq.ClientBinaryPath = binaryPath
	override.Scitq.ClientDownloadToken = "test-download-token"

	serverAddr, workerToken, adminUser, adminPassword, cleanup = startServerForTest(t, override)

	// startServerForTest forces DisableHTTPS=true; in that mode the
	// HTTP listener runs at gRPC port + 1. The base URL has no scheme
	// here so callers can prepend http:// or https:// as needed.
	parts := strings.Split(serverAddr, ":")
	require.Len(t, parts, 2)
	port := 0
	_, err := fmt.Sscanf(parts[1], "%d", &port)
	require.NoError(t, err)
	httpBase = fmt.Sprintf("http://%s:%d", parts[0], port+1)
	return
}

// adminCLI logs in as admin and returns an authenticated gRPC client.
// Uses extractToken to skip stray output from concurrent tests writing
// to os.Stdout while captureOutput holds the swap.
func adminCLI(t *testing.T, serverAddr, adminUser, adminPassword string) lib.QueueClient {
	t.Helper()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	require.NoError(t, err)
	tok := extractToken(out)
	qc, err := lib.CreateClient(serverAddr, tok)
	require.NoError(t, err)
	return qc
}

// registerWorker registers a fresh worker via the worker-token client.
// Returns the worker's ID.
func registerWorker(t *testing.T, serverAddr, workerToken, name string) int32 {
	t.Helper()
	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()
	conc := int32(1)
	arch := "linux/amd64"
	res, err := wk.Client.RegisterWorker(context.Background(), &pb.WorkerInfo{
		Name:        name,
		Concurrency: &conc,
		BuildArch:   &arch,
	})
	require.NoError(t, err)
	return res.GetWorkerId()
}

func TestRequestWorkerUpgrade_NormalAndCancel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	content := []byte("fake scitq client binary normal-cancel test\n")
	serverAddr, workerToken, adminUser, adminPassword, _, _, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()

	wid := registerWorker(t, serverAddr, workerToken, "wu-normal")

	adm := adminCLI(t, serverAddr, adminUser, adminPassword)
	defer adm.Close()

	// Set normal request.
	resp, err := adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		WorkerIds: []int32{wid},
		Mode:      "normal",
	})
	require.NoError(t, err)
	require.Equal(t, []int32{wid}, resp.GetAffectedWorkerIds())

	// Verify it's reflected on ListWorkers and on a worker ping.
	list, err := adm.Client.ListWorkers(ctx, &pb.ListWorkersRequest{})
	require.NoError(t, err)
	var found *pb.Worker
	for _, w := range list.Workers {
		if w.WorkerId == wid {
			found = w
			break
		}
	}
	require.NotNil(t, found, "worker %d not found in ListWorkers", wid)
	require.Equal(t, "normal", found.GetUpgradeRequested())

	// Worker pings see the same flag.
	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()
	pingResp, err := wk.Client.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{WorkerId: wid})
	require.NoError(t, err)
	require.Equal(t, "normal", pingResp.GetUpgradeRequested())

	// Cancel.
	resp, err = adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		WorkerIds: []int32{wid},
		Mode:      "cancel",
	})
	require.NoError(t, err)
	require.Equal(t, []int32{wid}, resp.GetAffectedWorkerIds())

	// Ping again — flag is now empty.
	pingResp, err = wk.Client.PingAndTakeNewTasks(ctx, &pb.PingAndGetNewTasksRequest{WorkerId: wid})
	require.NoError(t, err)
	require.Equal(t, "", pingResp.GetUpgradeRequested())

	// A re-cancel is a no-op (no rows updated).
	resp, err = adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		WorkerIds: []int32{wid},
		Mode:      "cancel",
	})
	require.NoError(t, err)
	require.Empty(t, resp.GetAffectedWorkerIds(), "re-cancelling an already-cleared worker should not report it as affected")
}

func TestRequestWorkerUpgrade_All(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	content := []byte("fake scitq client binary all test\n")
	serverAddr, workerToken, adminUser, adminPassword, _, _, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()

	w1 := registerWorker(t, serverAddr, workerToken, "wu-all-1")
	w2 := registerWorker(t, serverAddr, workerToken, "wu-all-2")
	w3 := registerWorker(t, serverAddr, workerToken, "wu-all-3")

	adm := adminCLI(t, serverAddr, adminUser, adminPassword)
	defer adm.Close()

	resp, err := adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		All:  true,
		Mode: "emergency",
	})
	require.NoError(t, err)
	got := map[int32]bool{}
	for _, id := range resp.GetAffectedWorkerIds() {
		got[id] = true
	}
	require.True(t, got[w1])
	require.True(t, got[w2])
	require.True(t, got[w3])
}

func TestRequestWorkerUpgrade_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	content := []byte("fake scitq client binary validation test\n")
	serverAddr, workerToken, adminUser, adminPassword, _, _, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()

	wid := registerWorker(t, serverAddr, workerToken, "wu-validation")

	adm := adminCLI(t, serverAddr, adminUser, adminPassword)
	defer adm.Close()

	// Invalid mode rejected.
	_, err := adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		WorkerIds: []int32{wid},
		Mode:      "panic",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())

	// Empty target list rejected.
	_, err = adm.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		Mode: "normal",
	})
	require.Error(t, err)
	st, ok = status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRequestWorkerUpgrade_RequiresAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	content := []byte("fake scitq client binary admin test\n")
	serverAddr, workerToken, _, _, _, _, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()

	wid := registerWorker(t, serverAddr, workerToken, "wu-admin")

	// Worker token = NOT admin — RequestWorkerUpgrade is admin-gated.
	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()
	_, err = wk.Client.RequestWorkerUpgrade(ctx, &pb.WorkerUpgradeRequest{
		WorkerIds: []int32{wid},
		Mode:      "normal",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.PermissionDenied, st.Code())
}

// TestClientSha256Endpoint exercises the HTTP sibling that workers hit
// to verify a downloaded binary. The endpoint is token-gated and
// caches by (mtime, size); both tokens (download + worker) authorize.
func TestClientSha256Endpoint(t *testing.T) {
	t.Parallel()

	content := []byte("known content for sha test\n")
	serverAddr, workerToken, _, _, _, httpBase, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()
	_ = serverAddr // unused but proves the server is up

	expected := sha256.Sum256(content)
	expectedHex := hex.EncodeToString(expected[:])

	// download-token authorizes
	hash := getOrFail(t, httpBase+"/scitq-client.sha256?token=test-download-token", http.StatusOK)
	require.Equal(t, expectedHex, strings.TrimSpace(hash))

	// worker-token also authorizes
	hash = getOrFail(t, httpBase+"/scitq-client.sha256?token="+workerToken, http.StatusOK)
	require.Equal(t, expectedHex, strings.TrimSpace(hash))

	// no token rejected
	_ = getOrFail(t, httpBase+"/scitq-client.sha256", http.StatusUnauthorized)

	// wrong token rejected
	_ = getOrFail(t, httpBase+"/scitq-client.sha256?token=nope", http.StatusUnauthorized)
}

// TestClientBinaryUpgradeRoundtrip simulates what the worker does at
// upgrade time, end-to-end against the live server: pull the URLs from
// GetClientUpgradeInfo, fetch the binary + checksum, verify hash.
func TestClientBinaryUpgradeRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	content := []byte("binary content for round-trip test\n")
	serverAddr, workerToken, _, _, _, _, cleanup := startServerWithClientBinary(t, content)
	defer cleanup()

	wk, err := lib.CreateClient(serverAddr, workerToken)
	require.NoError(t, err)
	defer wk.Close()

	info, err := wk.Client.GetClientUpgradeInfo(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	// startServerForTest sets DisableHTTPS=true, so the URLs use http://
	// and embed the WorkerToken (since that's what the worker auth'd as).
	require.True(t, strings.HasPrefix(info.GetBinaryUrl(), "http://"))
	require.Contains(t, info.GetBinaryUrl(), "/scitq-client?token=")
	require.Contains(t, info.GetSha256Url(), "/scitq-client.sha256?token=")
	require.False(t, info.GetInsecureSkipVerify(), "DisableHTTPS=true → no TLS, no skip-verify needed")

	gotBin := getBytesOrFail(t, info.GetBinaryUrl(), http.StatusOK)
	require.Equal(t, content, gotBin)
	gotSha := strings.TrimSpace(getOrFail(t, info.GetSha256Url(), http.StatusOK))
	wantSha := sha256.Sum256(content)
	require.Equal(t, hex.EncodeToString(wantSha[:]), gotSha)
}

func getOrFail(t *testing.T, url string, expectStatus int) string {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, expectStatus, resp.StatusCode, "GET %s", url)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func getBytesOrFail(t *testing.T, url string, expectStatus int) []byte {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, expectStatus, resp.StatusCode, "GET %s", url)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return body
}
