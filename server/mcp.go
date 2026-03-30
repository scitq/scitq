package server

import (
	"context"
	"crypto/rand"
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
		session, sessionID = h.handleInitialize(w, r, req)
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

func (h *mcpHandler) handleInitialize(w http.ResponseWriter, r *http.Request, req jsonrpcRequest) (*mcpSession, string) {
	sid := generateSessionID()
	session := &mcpSession{}

	// If Authorization: Bearer <token> is provided, pre-authenticate the session
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		session.token = strings.TrimPrefix(auth, "Bearer ")
		log.Printf("🔐 MCP session %s pre-authenticated via Bearer token", sid)
	}

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
		// --- Worker tools ---
		{
			Name:        "list_workers",
			Description: "List deployed workers.",
			InputSchema: inputSchema{Type: "object"},
		},
		{
			Name:        "delete_worker",
			Description: "Delete a worker (destroys the VM if cloud-deployed).",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"worker_id": {Type: "integer", Description: "Worker ID"}},
				Required: []string{"worker_id"},
			},
		},
		// --- Task management ---
		{
			Name:        "retry_task",
			Description: "Retry a failed task (creates a fresh clone).",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"task_id": {Type: "integer", Description: "Task ID"}},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "force_run_task",
			Description: "Force a waiting (W) task to pending (P), bypassing dependency checks.",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"task_id": {Type: "integer", Description: "Task ID"}},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "task_status_counts",
			Description: "Get task count per status for a workflow or globally.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"workflow_id": {Type: "integer", Description: "Filter by workflow ID (optional)"},
				},
			},
		},
		// --- Workflow management ---
		{
			Name:        "update_workflow_status",
			Description: "Update a workflow's status (R=Running, P=Paused, D=Debug).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"workflow_id": {Type: "integer", Description: "Workflow ID"},
					"status":     {Type: "string", Description: "New status: R (Running), P (Paused), D (Debug)"},
				},
				Required: []string{"workflow_id", "status"},
			},
		},
		{
			Name:        "delete_workflow",
			Description: "Delete a workflow and all its tasks.",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"workflow_id": {Type: "integer", Description: "Workflow ID"}},
				Required: []string{"workflow_id"},
			},
		},
		// --- Template/module download ---
		{
			Name:        "download_template",
			Description: "Download a template script source code.",
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
			Name:        "upload_module",
			Description: "Upload a private YAML module to the server.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"filename": {Type: "string", Description: "Module filename (e.g. my_step.yaml)"},
					"content":  {Type: "string", Description: "YAML content of the module"},
					"force":    {Type: "boolean", Description: "Overwrite if exists"},
				},
				Required: []string{"filename", "content"},
			},
		},
		{
			Name:        "download_module",
			Description: "Download a private YAML module from the server.",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"name": {Type: "string", Description: "Module filename"}},
				Required: []string{"name"},
			},
		},
		{
			Name:        "edit_and_retry_task",
			Description: "Edit a task's command and retry it. Returns the new task ID.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"task_id": {Type: "integer", Description: "Task ID to edit"},
					"command": {Type: "string", Description: "New command"},
				},
				Required: []string{"task_id", "command"},
			},
		},
		{
			Name:        "edit_step_command",
			Description: "Find/replace in all failed tasks of a step and retry them.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"step_id":   {Type: "integer", Description: "Step ID"},
					"find":      {Type: "string", Description: "Text or regexp to find"},
					"replace":   {Type: "string", Description: "Replacement text"},
					"is_regexp": {Type: "boolean", Description: "Treat find as regexp"},
				},
				Required: []string{"step_id", "find", "replace"},
			},
		},
		// --- Steps and recruiters ---
		{
			Name:        "list_steps",
			Description: "List steps, optionally filtered by workflow.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"workflow_id": {Type: "integer", Description: "Filter by workflow ID (optional)"},
				},
			},
		},
		// --- File operations ---
		{
			Name:        "file_list",
			Description: "List files at a remote URI (cloud storage).",
			InputSchema: inputSchema{
				Type:     "object",
				Properties: map[string]schemaProperty{"uri": {Type: "string", Description: "URI to list (e.g. s3://bucket/path/)"}},
				Required: []string{"uri"},
			},
		},
		// --- Run management ---
		{
			Name:        "list_template_runs",
			Description: "List template execution runs.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"template_id": {Type: "integer", Description: "Filter by template ID (optional)"},
				},
			},
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
	case "download_template":
		return h.toolDownloadTemplate(authCtx, call.Arguments)
	case "list_workflows":
		return h.toolListWorkflows(authCtx, call.Arguments)
	case "update_workflow_status":
		return h.toolUpdateWorkflowStatus(authCtx, call.Arguments)
	case "delete_workflow":
		return h.toolDeleteWorkflow(authCtx, call.Arguments)
	case "list_tasks":
		return h.toolListTasks(authCtx, call.Arguments)
	case "task_logs":
		return h.toolTaskLogs(authCtx, call.Arguments)
	case "retry_task":
		return h.toolRetryTask(authCtx, call.Arguments)
	case "force_run_task":
		return h.toolForceRunTask(authCtx, call.Arguments)
	case "task_status_counts":
		return h.toolTaskStatusCounts(authCtx, call.Arguments)
	case "list_workers":
		return h.toolListWorkers(authCtx)
	case "delete_worker":
		return h.toolDeleteWorker(authCtx, call.Arguments)
	case "list_modules":
		return h.toolListModules(authCtx)
	case "upload_module":
		return h.toolUploadModule(authCtx, call.Arguments)
	case "download_module":
		return h.toolDownloadModule(authCtx, call.Arguments)
	case "edit_and_retry_task":
		return h.toolEditAndRetryTask(authCtx, call.Arguments)
	case "edit_step_command":
		return h.toolEditStepCommand(authCtx, call.Arguments)
	case "list_steps":
		return h.toolListSteps(authCtx, call.Arguments)
	case "file_list":
		return h.toolFileList(authCtx, call.Arguments)
	case "list_template_runs":
		return h.toolListTemplateRuns(authCtx, call.Arguments)
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

func (h *mcpHandler) toolListWorkers(ctx context.Context) (any, *rpcError) {
	res, err := h.server.ListWorkers(ctx, &pb.ListWorkersRequest{})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Workers), nil
}

func (h *mcpHandler) toolDeleteWorker(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ WorkerID int32 `json:"worker_id"` }
	json.Unmarshal(args, &p)
	_, err := h.server.DeleteWorker(ctx, &pb.WorkerDeletion{WorkerId: p.WorkerID})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Worker %d deletion initiated", p.WorkerID)), nil
}

func (h *mcpHandler) toolRetryTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ TaskID int32 `json:"task_id"` }
	json.Unmarshal(args, &p)
	res, err := h.server.RetryTask(ctx, &pb.RetryTaskRequest{TaskId: p.TaskID})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Task %d retried — new task ID: %d", p.TaskID, res.TaskId)), nil
}

func (h *mcpHandler) toolForceRunTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ TaskID int32 `json:"task_id"` }
	json.Unmarshal(args, &p)
	_, err := h.server.ForceRunTask(ctx, &pb.ForceRunTaskRequest{TaskId: p.TaskID})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Task %d forced from W to P", p.TaskID)), nil
}

func (h *mcpHandler) toolTaskStatusCounts(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	res, err := h.server.GetTaskStatusCounts(ctx, &pb.TaskStatusCountsRequest{})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]any{
		"global_counts": res.GlobalCounts,
		"total":         res.TotalCount,
	}), nil
}

func (h *mcpHandler) toolUpdateWorkflowStatus(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		WorkflowID int32  `json:"workflow_id"`
		Status     string `json:"status"`
	}
	json.Unmarshal(args, &p)
	_, err := h.server.UpdateWorkflowStatus(ctx, &pb.WorkflowStatusUpdate{
		WorkflowId: p.WorkflowID,
		Status:     p.Status,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Workflow %d status updated to %s", p.WorkflowID, p.Status)), nil
}

func (h *mcpHandler) toolDeleteWorkflow(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ WorkflowID int32 `json:"workflow_id"` }
	json.Unmarshal(args, &p)
	_, err := h.server.DeleteWorkflow(ctx, &pb.WorkflowId{WorkflowId: p.WorkflowID})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Workflow %d deleted", p.WorkflowID)), nil
}

func (h *mcpHandler) toolDownloadTemplate(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	json.Unmarshal(args, &p)
	req := &pb.DownloadTemplateRequest{Name: &p.Name}
	if p.Version != "" {
		req.Version = &p.Version
	}
	res, err := h.server.DownloadTemplate(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: string(res.Content)}},
	}, nil
}

func (h *mcpHandler) toolUploadModule(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
		Force    bool   `json:"force"`
	}
	json.Unmarshal(args, &p)
	_, err := h.server.UploadModule(ctx, &pb.UploadModuleRequest{
		Filename: p.Filename,
		Content:  []byte(p.Content),
		Force:    p.Force,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Module '%s' uploaded", p.Filename)), nil
}

func (h *mcpHandler) toolDownloadModule(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ Name string `json:"name"` }
	json.Unmarshal(args, &p)
	res, err := h.server.DownloadModule(ctx, &pb.DownloadModuleRequest{Filename: p.Name})
	if err != nil {
		return errorResult(err), nil
	}
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: string(res.Content)}},
	}, nil
}

func (h *mcpHandler) toolEditAndRetryTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		TaskID  int32  `json:"task_id"`
		Command string `json:"command"`
	}
	json.Unmarshal(args, &p)
	res, err := h.server.EditAndRetryTask(ctx, &pb.EditAndRetryTaskRequest{
		TaskId:  p.TaskID,
		Command: p.Command,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Task %d edited and retried → new task ID: %d", p.TaskID, res.TaskId)), nil
}

func (h *mcpHandler) toolEditStepCommand(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		StepID   int32  `json:"step_id"`
		Find     string `json:"find"`
		Replace  string `json:"replace"`
		IsRegexp bool   `json:"is_regexp"`
	}
	json.Unmarshal(args, &p)
	res, err := h.server.EditStepCommand(ctx, &pb.EditStepCommandRequest{
		StepId:   p.StepID,
		Find:     p.Find,
		Replace:  p.Replace,
		IsRegexp: p.IsRegexp,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res), nil
}

func (h *mcpHandler) toolListSteps(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ WorkflowID int32 `json:"workflow_id"` }
	json.Unmarshal(args, &p)
	filter := &pb.StepFilter{WorkflowId: p.WorkflowID}
	res, err := h.server.ListSteps(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Steps), nil
}

func (h *mcpHandler) toolFileList(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ URI string `json:"uri"` }
	json.Unmarshal(args, &p)
	res, err := h.server.FetchList(ctx, &pb.FetchListRequest{Uri: p.URI})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Files), nil
}

func (h *mcpHandler) toolListTemplateRuns(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ TemplateID int32 `json:"template_id"` }
	json.Unmarshal(args, &p)
	filter := &pb.TemplateRunFilter{}
	if p.TemplateID != 0 {
		filter.WorkflowTemplateId = &p.TemplateID
	}
	res, err := h.server.ListTemplateRuns(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Runs), nil
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

func textResult(msg string) *toolResult {
	return &toolResult{
		Content: []contentBlock{{Type: "text", Text: msg}},
	}
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

// resolveSessionContext resolves a token (JWT or session) to an authenticated user context.
func (h *mcpHandler) resolveSessionContext(ctx context.Context, token string) context.Context {
	// Try JWT first (from scitq login --json)
	claims, err := parseJWT(token, h.server.cfg.Scitq.JwtSecret)
	if err == nil {
		userID := int(claims["user_id"].(float64))
		username, _ := claims["username"].(string)
		isAdmin, _ := claims["is_admin"].(bool)
		return context.WithValue(ctx, userContextKey, &AuthenticatedUser{
			UserID:   userID,
			Username: username,
			IsAdmin:  isAdmin,
		})
	}

	// Fall back to session token (from Login RPC)
	var userID int
	var username string
	var isAdmin bool
	err = h.server.db.QueryRow(
		`SELECT u.user_id, u.username, u.is_admin FROM scitq_user_session s
		 JOIN scitq_user u ON s.user_id=u.user_id
		 WHERE s.session_id=$1 AND s.expires_at > now()`,
		token).Scan(&userID, &username, &isAdmin)
	if err != nil {
		log.Printf("MCP: invalid token (neither JWT nor session)")
		return ctx
	}
	return context.WithValue(ctx, userContextKey, &AuthenticatedUser{
		UserID:   userID,
		Username: username,
		IsAdmin:  isAdmin,
	})
}
