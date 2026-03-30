package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// jsonrpc helpers
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func mcpPost(t *testing.T, url, sessionID string, req rpcRequest) rpcResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var rpcResp rpcResponse
	require.NoError(t, json.Unmarshal(respBody, &rpcResp), "response: %s", string(respBody))
	return rpcResp
}

func TestMCPEndToEnd(t *testing.T) {
	t.Parallel()

	serverAddr, _, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	// HTTP server is on gRPC port + 1 when HTTPS is disabled
	parts := splitHostPort(t, serverAddr)
	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", parts.port+1)

	// Wait for HTTP server to be ready
	require.Eventually(t, func() bool {
		resp, err := http.Post(mcpURL, "application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}, 10*time.Second, 200*time.Millisecond, "HTTP server should be ready")

	// --- Step 1: Initialize ---
	initResp := mcpPost(t, mcpURL, "", rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "test",
				"version": "1.0.0",
			},
		},
	})
	require.Nil(t, initResp.Error, "initialize should succeed")

	var initResult map[string]any
	require.NoError(t, json.Unmarshal(initResp.Result, &initResult))
	require.Equal(t, "2025-03-26", initResult["protocolVersion"])

	// Extract session ID from the initialize response (we need to parse it from result)
	// Actually, the session ID comes from the HTTP header. Let's do the full flow manually.
	body, _ := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      10,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "test", "version": "1.0.0"},
		},
	})
	httpResp, err := http.Post(mcpURL, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	sessionID := httpResp.Header.Get("Mcp-Session-Id")
	httpResp.Body.Close()
	require.NotEmpty(t, sessionID, "server should return Mcp-Session-Id header")

	t.Logf("Got session ID: %s", sessionID)

	// --- Step 2: List tools ---
	toolsResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})
	require.Nil(t, toolsResp.Error, "tools/list should succeed")

	var toolsResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(toolsResp.Result, &toolsResult))
	require.NotEmpty(t, toolsResult.Tools, "should have tools")

	toolNames := make([]string, len(toolsResult.Tools))
	for i, tool := range toolsResult.Tools {
		toolNames[i] = tool.Name
	}
	require.Contains(t, toolNames, "login")
	require.Contains(t, toolNames, "list_templates")
	require.Contains(t, toolNames, "run_template")
	require.Contains(t, toolNames, "list_tasks")
	t.Logf("Available tools: %v", toolNames)

	// --- Step 3: Call tool without login → should fail ---
	noAuthResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_templates",
			"arguments": map[string]any{},
		},
	})
	require.Nil(t, noAuthResp.Error, "should not be a protocol error")
	var noAuthResult struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(noAuthResp.Result, &noAuthResult)
	require.True(t, noAuthResult.IsError, "should fail: not authenticated")

	// --- Step 4: Login ---
	loginResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "login",
			"arguments": map[string]any{
				"user":     adminUser,
				"password": adminPassword,
			},
		},
	})
	require.Nil(t, loginResp.Error, "login should succeed")
	var loginResult struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(loginResp.Result, &loginResult)
	require.False(t, loginResult.IsError, "login should not be an error")
	t.Log("Login successful")

	// --- Step 5: List templates (authenticated) ---
	listResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_templates",
			"arguments": map[string]any{},
		},
	})
	require.Nil(t, listResp.Error, "list_templates should succeed")
	var listResult struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(listResp.Result, &listResult)
	require.False(t, listResult.IsError, "list_templates should not error")
	t.Log("list_templates succeeded (empty list expected)")

	// --- Step 6: List workflows ---
	wfResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_workflows",
			"arguments": map[string]any{},
		},
	})
	require.Nil(t, wfResp.Error)
	t.Log("list_workflows succeeded")

	// --- Step 7: List modules ---
	modResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_modules",
			"arguments": map[string]any{},
		},
	})
	require.Nil(t, modResp.Error)
	t.Log("list_modules succeeded")

	// --- Step 8: Unknown tool → protocol error ---
	unknownResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      8,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "nonexistent_tool",
			"arguments": map[string]any{},
		},
	})
	require.NotNil(t, unknownResp.Error, "unknown tool should return protocol error")
	require.Equal(t, -32602, unknownResp.Error.Code)
	t.Logf("Unknown tool error: %s", unknownResp.Error.Message)

	// --- Step 9: Delete session ---
	delReq, _ := http.NewRequest("DELETE", mcpURL, nil)
	delReq.Header.Set("Mcp-Session-Id", sessionID)
	delResp, err := http.DefaultClient.Do(delReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, delResp.StatusCode)
	delResp.Body.Close()
	t.Log("Session deleted")

	// --- Step 10: Request with deleted session → 404 ---
	staleResp := mcpPost(t, mcpURL, sessionID, rpcRequest{
		JSONRPC: "2.0",
		ID:      9,
		Method:  "tools/list",
	})
	require.NotNil(t, staleResp.Error, "deleted session should return error")
	t.Log("Stale session correctly rejected")
}

type hostPort struct {
	host string
	port int
}

func splitHostPort(t *testing.T, addr string) hostPort {
	t.Helper()
	parts := strings.SplitN(addr, ":", 2)
	require.Len(t, parts, 2, "failed to parse %q", addr)
	port, err := strconv.Atoi(parts[1])
	require.NoError(t, err, "failed to parse port from %q", addr)
	return hostPort{host: parts[0], port: port}
}
