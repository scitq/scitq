package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/internal/version"
	"google.golang.org/protobuf/types/known/emptypb"
)

// MCP (Model Context Protocol) server — Streamable HTTP transport.
// Single endpoint: POST /mcp accepts JSON-RPC 2.0 messages.

type mcpSession struct {
	token string // JWT token set by login tool
}

type mcpHandler struct {
	server   *taskQueueServer
	sessions sync.Map // session ID → *mcpSession
}

// JSON-RPC 2.0 types
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id,omitempty"`
	Result  any         `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types
type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type schemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func newMCPHandler(s *taskQueueServer) *mcpHandler {
	return &mcpHandler{server: s}
}

func (h *mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		h.handlePost(w, r)
	case "DELETE":
		sid := r.Header.Get("Mcp-Session-Id")
		if sid != "" {
			h.sessions.Delete(sid)
		}
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *mcpHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, jsonrpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, jsonrpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32600, Message: "Invalid Request: jsonrpc must be 2.0"},
		})
		return
	}

	// Notifications (no ID) — accept silently
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Get or create session
	sessionID := r.Header.Get("Mcp-Session-Id")
	var session *mcpSession

	switch req.Method {
	case "initialize":
		session, sessionID = h.handleInitialize(w, req)
		if session == nil {
			return // already wrote response
		}
	default:
		if sessionID == "" {
			writeJSON(w, http.StatusBadRequest, jsonrpcResponse{
				JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: -32600, Message: "Missing Mcp-Session-Id header"},
			})
			return
		}
		val, ok := h.sessions.Load(sessionID)
		if !ok {
			writeJSON(w, http.StatusNotFound, jsonrpcResponse{
				JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: -32600, Message: "Session not found — send initialize first"},
			})
			return
		}
		session = val.(*mcpSession)
	}

	var result any
	var rpcErr *rpcError

	switch req.Method {
	case "initialize":
		// Already handled above
		return
	case "tools/list":
		result = map[string]any{"tools": h.listTools()}
	case "tools/call":
		result, rpcErr = h.callTool(r.Context(), session, req.Params)
	default:
		rpcErr = &rpcError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)}
	}

	resp := jsonrpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	w.Header().Set("Mcp-Session-Id", sessionID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *mcpHandler) handleInitialize(w http.ResponseWriter, req jsonrpcRequest) (*mcpSession, string) {
	sid := generateSessionID()
	session := &mcpSession{}
	h.sessions.Store(sid, session)

	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "scitq",
				"version": version.Version,
			},
			"instructions": "scitq distributed task queue. Use the login tool first to authenticate, then list_templates to discover available workflows.",
		},
	}
	w.Header().Set("Mcp-Session-Id", sid)
	writeJSON(w, http.StatusOK, resp)
	return session, sid
}

// Tool definitions
func (h *mcpHandler) listTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "login",
			Description: "Authenticate and get a session token. Must be called before other tools.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"user":     {Type: "string", Description: "Username"},
					"password": {Type: "string", Description: "Password"},
				},
				Required: []string{"user", "password"},
			},
		},
		{
			Name:        "list_templates",
			Description: "List available workflow templates.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"name": {Type: "string", Description: "Filter by name (optional)"},
				},
			},
		},
		{
			Name:        "template_detail",
			Description: "Get template metadata and parameter schema.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"name":    {Type: "string", Description: "Template name"},
					"version": {Type: "string", Description: "Version (default: latest)"},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "run_template",
			Description: "Run a template with parameters. Returns the template run ID and workflow ID.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"name":           {Type: "string", Description: "Template name"},
					"version":        {Type: "string", Description: "Version (default: latest)"},
					"params":         {Type: "object", Description: "Parameter key-value pairs"},
					"no_recruiters":  {Type: "boolean", Description: "Create workflow without deploying workers"},
				},
				Required: []string{"name", "params"},
			},
		},
		{
			Name:        "list_workflows",
			Description: "List workflows, optionally filtered by name.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"name": {Type: "string", Description: "Filter by name pattern (optional)"},
				},
			},
		},
		{
			Name:        "list_tasks",
			Description: "List tasks with optional filters.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"workflow_id": {Type: "integer", Description: "Filter by workflow ID"},
					"status":      {Type: "string", Description: "Filter by status (P, A, C, R, S, F, W)"},
					"limit":       {Type: "integer", Description: "Max results (default: 50)"},
				},
			},
		},
		{
			Name:        "task_logs",
			Description: "Get stdout and stderr of a task.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"task_id": {Type: "integer", Description: "Task ID"},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "list_modules",
			Description: "List private YAML modules on the server.",
			InputSchema: inputSchema{Type: "object"},
		},
	}
}

// Tool dispatch
func (h *mcpHandler) callTool(ctx context.Context, session *mcpSession, raw json.RawMessage) (any, *rpcError) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return nil, &rpcError{Code: -32602, Message: "Invalid params"}
	}

	// Login doesn't need auth
	if call.Name == "login" {
		return h.toolLogin(ctx, session, call.Arguments)
	}

	// All other tools require auth
	if session.token == "" {
		return &toolResult{
			Content: []contentBlock{{Type: "text", Text: "Not authenticated. Call the login tool first."}},
			IsError: true,
		}, nil
	}

	// Resolve session token to authenticated user context
	authCtx := h.resolveSessionContext(ctx, session.token)

	switch call.Name {
	case "list_templates":
		return h.toolListTemplates(authCtx, call.Arguments)
	case "template_detail":
		return h.toolTemplateDetail(authCtx, call.Arguments)
	case "run_template":
		return h.toolRunTemplate(authCtx, call.Arguments)
	case "list_workflows":
		return h.toolListWorkflows(authCtx, call.Arguments)
	case "list_tasks":
		return h.toolListTasks(authCtx, call.Arguments)
	case "task_logs":
		return h.toolTaskLogs(authCtx, call.Arguments)
	case "list_modules":
		return h.toolListModules(authCtx)
	default:
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("Unknown tool: %s", call.Name)}
	}
}

// --- Tool implementations ---

func (h *mcpHandler) toolLogin(ctx context.Context, session *mcpSession, args json.RawMessage) (any, *rpcError) {
	var p struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	json.Unmarshal(args, &p)

	resp, err := h.server.Login(ctx, &pb.LoginRequest{Username: p.User, Password: p.Password})
	if err != nil {
		return &toolResult{
			Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("Login failed: %v", err)}},
			IsError: true,
		}, nil
	}
	session.token = resp.GetToken()
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: "Authenticated successfully."}},
	}, nil
}

func (h *mcpHandler) toolListTemplates(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Name string `json:"name"`
	}
	json.Unmarshal(args, &p)

	filter := &pb.TemplateFilter{}
	if p.Name != "" {
		filter.Name = &p.Name
	}
	latest := "latest"
	filter.Version = &latest

	res, err := h.server.ListTemplates(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Templates), nil
}

func (h *mcpHandler) toolTemplateDetail(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	json.Unmarshal(args, &p)

	filter := &pb.TemplateFilter{Name: &p.Name}
	if p.Version != "" {
		filter.Version = &p.Version
	} else {
		latest := "latest"
		filter.Version = &latest
	}

	res, err := h.server.ListTemplates(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	if len(res.Templates) == 0 {
		return &toolResult{
			Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("Template '%s' not found", p.Name)}},
			IsError: true,
		}, nil
	}
	return jsonResult(res.Templates[0]), nil
}

func (h *mcpHandler) toolRunTemplate(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Name         string         `json:"name"`
		Version      string         `json:"version"`
		Params       map[string]any `json:"params"`
		NoRecruiters bool           `json:"no_recruiters"`
	}
	json.Unmarshal(args, &p)

	// Resolve template ID
	filter := &pb.TemplateFilter{Name: &p.Name}
	if p.Version != "" {
		filter.Version = &p.Version
	} else {
		latest := "latest"
		filter.Version = &latest
	}
	templates, err := h.server.ListTemplates(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	if len(templates.Templates) == 0 {
		return &toolResult{
			Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("Template '%s' not found", p.Name)}},
			IsError: true,
		}, nil
	}

	// Convert params to JSON string
	paramJSON, _ := json.Marshal(p.Params)

	res, err := h.server.RunTemplate(ctx, &pb.RunTemplateRequest{
		WorkflowTemplateId: templates.Templates[0].WorkflowTemplateId,
		ParamValuesJson:    string(paramJSON),
		NoRecruiters:       p.NoRecruiters,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res), nil
}

func (h *mcpHandler) toolListWorkflows(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Name string `json:"name"`
	}
	json.Unmarshal(args, &p)

	filter := &pb.WorkflowFilter{}
	if p.Name != "" {
		filter.NameLike = &p.Name
	}
	res, err := h.server.ListWorkflows(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Workflows), nil
}

func (h *mcpHandler) toolListTasks(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		WorkflowID int32  `json:"workflow_id"`
		Status     string `json:"status"`
		Limit      int32  `json:"limit"`
	}
	json.Unmarshal(args, &p)

	req := &pb.ListTasksRequest{}
	if p.WorkflowID != 0 {
		req.WorkflowIdFilter = &p.WorkflowID
	}
	if p.Status != "" {
		req.StatusFilter = &p.Status
	}
	limit := p.Limit
	if limit == 0 {
		limit = 50
	}
	req.Limit = &limit

	res, err := h.server.ListTasks(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Tasks), nil
}

func (h *mcpHandler) toolTaskLogs(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		TaskID int32 `json:"task_id"`
	}
	json.Unmarshal(args, &p)

	logType := "stdout"
	res, err := h.server.GetLogsChunk(ctx, &pb.GetLogsRequest{
		TaskIds:  []int32{p.TaskID},
		LogType:  &logType,
	})
	if err != nil {
		return errorResult(err), nil
	}

	var stdoutText, stderrText string
	for _, chunk := range res.Logs {
		stdoutText += strings.Join(chunk.Stdout, "\n")
		stderrText += strings.Join(chunk.Stderr, "\n")
	}

	text := fmt.Sprintf("=== STDOUT ===\n%s\n=== STDERR ===\n%s", stdoutText, stderrText)
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: text}},
	}, nil
}

func (h *mcpHandler) toolListModules(ctx context.Context) (any, *rpcError) {
	res, err := h.server.ListModules(ctx, &emptypb.Empty{})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Modules), nil
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func jsonResult(v any) *toolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: string(b)}},
	}
}

func errorResult(err error) *toolResult {
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: err.Error()}},
		IsError: true,
	}
}

// resolveSessionContext resolves a session token to an authenticated user context.
func (h *mcpHandler) resolveSessionContext(ctx context.Context, sessionToken string) context.Context {
	var userID int
	var username string
	var isAdmin bool
	err := h.server.db.QueryRow(
		`SELECT u.user_id, u.username, u.is_admin FROM scitq_user_session s
		 JOIN scitq_user u ON s.user_id=u.user_id
		 WHERE s.session_id=$1 AND s.expires_at > now()`,
		sessionToken).Scan(&userID, &username, &isAdmin)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("MCP: invalid session token")
		}
		return ctx
	}
	return context.WithValue(ctx, userContextKey, &AuthenticatedUser{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
	})
}
