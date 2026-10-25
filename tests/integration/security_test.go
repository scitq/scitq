package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cli "github.com/scitq/scitq/cli"
	pb "github.com/scitq/scitq/gen/taskqueuepb"
	lib "github.com/scitq/scitq/lib"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func loginToken(t *testing.T, serverAddr, user, password string) string {
	t.Helper()
	var c cli.CLI
	c.Attr.Server = serverAddr
	out, err := runCLICommand(c, []string{"login", "--user", user, "--password", password})
	require.NoError(t, err)
	return extractToken(out)
}

func clientWithToken(t *testing.T, serverAddr, token string) pb.TaskQueueClient {
	t.Helper()
	qc, err := lib.CreateClient(serverAddr, token)
	require.NoError(t, err)
	t.Cleanup(func() { qc.Close() })
	return qc.Client
}

func requireCode(t *testing.T, err error, want codes.Code, msg string) {
	t.Helper()
	require.Error(t, err, msg)
	require.Equal(t, want, status.Code(err), "%s: %v", msg, err)
}

// TestWorkerTokenScope: the shared worker token allows the calls a worker
// makes and nothing else.
func TestWorkerTokenScope(t *testing.T) {
	t.Parallel()
	serverAddr, workerToken, _, _, cleanup := startServerForTest(t, nil)
	defer cleanup()
	ctx := context.Background()
	wk := clientWithToken(t, serverAddr, workerToken)

	_, err := wk.ServerVersion(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	_, err = wk.GetRcloneConfig(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	_, err = wk.ListWorkflows(ctx, &pb.WorkflowFilter{})
	requireCode(t, err, codes.PermissionDenied, "ListWorkflows with the worker token")
	_, err = wk.CreateWorker(ctx, &pb.WorkerRequest{})
	requireCode(t, err, codes.PermissionDenied, "CreateWorker with the worker token")
	_, err = wk.ListUsers(ctx, &emptypb.Empty{})
	requireCode(t, err, codes.PermissionDenied, "ListUsers with the worker token")
}

// TestStreamsNeedAuth: streaming RPCs check the caller like unary ones.
func TestStreamsNeedAuth(t *testing.T) {
	t.Parallel()
	serverAddr, _, _, _, cleanup := startServerForTest(t, nil)
	defer cleanup()
	anon := clientWithToken(t, serverAddr, "not-a-token")

	stream, err := anon.StreamTaskLogsOutput(context.Background(), &pb.TaskId{TaskId: 1})
	if err == nil {
		_, err = stream.Recv()
	}
	requireCode(t, err, codes.Unauthenticated, "reading task logs without a session")

	send, err := anon.SendTaskLogs(context.Background())
	if err == nil {
		_ = send.Send(&pb.TaskLog{TaskId: 1, LogType: "stdout", LogText: "injected"})
		_, err = send.CloseAndRecv()
	}
	requireCode(t, err, codes.Unauthenticated, "writing task logs without a session")
}

// TestSessionRevocation: Logout, a password change and an admin reset
// revoke sessions for gRPC and MCP alike.
func TestSessionRevocation(t *testing.T) {
	t.Parallel()
	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()
	ctx := context.Background()
	admin := clientWithToken(t, serverAddr, loginToken(t, serverAddr, adminUser, adminPassword))

	// Logout revokes that session only.
	t1 := loginToken(t, serverAddr, adminUser, adminPassword)
	t2 := loginToken(t, serverAddr, adminUser, adminPassword)
	_, err := clientWithToken(t, serverAddr, t2).Logout(ctx, &pb.Token{Token: t2})
	require.NoError(t, err)
	_, err = clientWithToken(t, serverAddr, t2).ListWorkflows(ctx, &pb.WorkflowFilter{})
	requireCode(t, err, codes.Unauthenticated, "logged-out session")
	_, err = clientWithToken(t, serverAddr, t1).ListWorkflows(ctx, &pb.WorkflowFilter{})
	require.NoError(t, err, "another session of the same user survives a logout")

	// A password change keeps the caller's session and revokes the others.
	uid, err := admin.CreateUser(ctx, &pb.CreateUserRequest{Username: "bob", Password: "old-pw", Email: "bob@example.org"})
	require.NoError(t, err)
	b1 := loginToken(t, serverAddr, "bob", "old-pw")
	b2 := loginToken(t, serverAddr, "bob", "old-pw")
	_, err = clientWithToken(t, serverAddr, b1).ChangePassword(ctx, &pb.ChangePasswordRequest{Username: "bob", OldPassword: "old-pw", NewPassword: "new-pw"})
	require.NoError(t, err)
	_, err = clientWithToken(t, serverAddr, b1).ListWorkflows(ctx, &pb.WorkflowFilter{})
	require.NoError(t, err, "the session that changed the password survives")
	_, err = clientWithToken(t, serverAddr, b2).ListWorkflows(ctx, &pb.WorkflowFilter{})
	requireCode(t, err, codes.Unauthenticated, "other session after a password change")

	// An admin reset revokes every session, MCP included.
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", httpPortForAddr(t, serverAddr))
	sessionID := mcpSession(t, mcpURL, "security-test", "bob", "new-pw")
	listWorkflows := rpcRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: map[string]any{"name": "list_workflows", "arguments": map[string]any{}}}
	resp := mcpPost(t, mcpURL, sessionID, listWorkflows)
	require.NotContains(t, string(resp.Result), "Authentication failed")

	_, err = admin.AdminResetPassword(ctx, &pb.AdminResetPasswordRequest{UserId: uid.UserId, NewPassword: "reset-pw"})
	require.NoError(t, err)
	_, err = clientWithToken(t, serverAddr, b1).ListWorkflows(ctx, &pb.WorkflowFilter{})
	requireCode(t, err, codes.Unauthenticated, "session after an admin reset")
	resp = mcpPost(t, mcpURL, sessionID, listWorkflows)
	require.Contains(t, string(resp.Result), "Authentication failed", "MCP session after an admin reset")
}

// TestHTTPEndpointsNeedSession: the event socket needs a session, and the
// worker token is not served over HTTP.
func TestHTTPEndpointsNeedSession(t *testing.T) {
	t.Parallel()
	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()
	base := fmt.Sprintf("http://localhost:%d", httpPortForAddr(t, serverAddr))
	token := loginToken(t, serverAddr, adminUser, adminPassword)

	resp, err := http.Get(base + "/ws")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "/ws without a session")

	req, _ := http.NewRequest("GET", base+"/ws", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "/ws with a session")

	req, _ = http.NewRequest("GET", base+"/WorkerToken", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NotContains(t, string(body), workerToken)
}

// TestTaskTokenScope: a scitq_auth task holds its own token, which allows the
// file calls only and stops working when the task ends.
func TestTaskTokenScope(t *testing.T) {
	t.Parallel()
	qc, serverAddr, cleanup := taskTokenTestSetup(t)
	defer cleanup()
	ctx := context.Background()

	dir := t.TempDir()
	tokenFile, stopFile := filepath.Join(dir, "token"), filepath.Join(dir, "stop")
	shell, retry, auth := "sh", int32(0), true
	task, err := qc.SubmitTask(ctx, &pb.TaskRequest{
		Command: fmt.Sprintf(`printf %%s "$SCITQ_TOKEN" > %s.tmp && mv %s.tmp %s && while [ ! -f %s ]; do sleep 0.2; done`,
			tokenFile, tokenFile, tokenFile, stopFile),
		Container: "bare", Shell: &shell, Status: "P", Retry: &retry, ScitqAuth: &auth,
	})
	require.NoError(t, err)

	var token string
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(tokenFile)
		token = string(b)
		return err == nil
	}, 120*time.Second, 200*time.Millisecond, "task should write its token")
	require.True(t, strings.HasPrefix(token, "task-"), "task holds a task token, got %q", token)

	tc := clientWithToken(t, serverAddr, token)
	_, err = tc.GetRcloneConfig(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	_, err = tc.ListWorkflows(ctx, &pb.WorkflowFilter{})
	requireCode(t, err, codes.PermissionDenied, "ListWorkflows with a task token")
	_, err = tc.CreateWorker(ctx, &pb.WorkerRequest{})
	requireCode(t, err, codes.PermissionDenied, "CreateWorker with a task token")
	_, err = clientWithToken(t, serverAddr, token+"0").GetRcloneConfig(ctx, &emptypb.Empty{})
	requireCode(t, err, codes.Unauthenticated, "forged task token")

	require.NoError(t, os.WriteFile(stopFile, nil, 0o644))
	require.Equal(t, "S", waitTerminal(t, qc, task.TaskId).Status)
	_, err = tc.GetRcloneConfig(ctx, &emptypb.Empty{})
	requireCode(t, err, codes.Unauthenticated, "task token after the task ended")
}

func taskTokenTestSetup(t *testing.T) (pb.TaskQueueClient, string, func()) {
	t.Helper()
	serverAddr, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	qc := clientWithToken(t, serverAddr, loginToken(t, serverAddr, adminUser, adminPassword))
	cleanupClient, _ := startClientForTest(t, serverAddr, "task-token-worker", workerToken, 1)
	return qc, serverAddr, func() {
		cleanupClient()
		cleanup()
	}
}
