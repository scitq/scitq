package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/stretchr/testify/require"
)

// mcpSession opens an MCP session announcing the given client name, logs in,
// and returns the session id.
func mcpSession(t *testing.T, mcpURL, clientName, user, password string) string {
	t.Helper()
	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0", ID: 1, Method: "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": clientName, "version": "1.0.0"},
		},
	})
	resp, err := http.Post(mcpURL, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	sessionID := resp.Header.Get("Mcp-Session-Id")
	resp.Body.Close()
	require.NotEmpty(t, sessionID)

	login := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0", ID: 2, Method: "tools/call",
		Params: map[string]any{
			"name":      "login",
			"arguments": map[string]any{"user": user, "password": password},
		},
	})
	require.Nil(t, login.Error, "MCP login should succeed")
	return sessionID
}

// requireAuditEvent asserts the worker's audit trail holds an event with the
// given action and actor.
func requireAuditEvent(t *testing.T, qc pb.TaskQueueClient, workerID int32, action, actorKind, actorName, channel, client string) {
	t.Helper()
	kind := "worker"
	res, err := qc.ListAuditEvents(context.Background(), &pb.AuditFilter{ObjectKind: &kind, ObjectId: &workerID})
	require.NoError(t, err)
	for _, e := range res.Events {
		if e.Action == action && e.ActorKind == actorKind && e.ActorName == actorName && e.Channel == channel &&
			(client == "" || (e.Client != nil && *e.Client == client)) {
			return
		}
	}
	require.Failf(t, "audit event not found",
		"worker %d: no %s by %s %q via %s (client %q); got %v", workerID, action, actorKind, actorName, channel, client, res.Events)
}
