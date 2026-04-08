package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

// normalizeArgs converts string-encoded numbers to real JSON numbers.
// MCP clients (e.g. Claude Code) may send integers as strings despite the schema declaring them as integer.
func normalizeArgs(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	changed := false
	for k, v := range m {
		if s, ok := v.(string); ok {
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				m[k] = n
				changed = true
			}
		}
	}
	if !changed {
		return raw
	}
	out, _ := json.Marshal(m)
	return out
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
		session.token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		log.Printf("🔐 MCP session %s pre-authenticated via Bearer token (len=%d, prefix=%q)", sid, len(session.token), session.token[:min(20, len(session.token))])
	} else {
		log.Printf("⚠️ MCP session %s: no Bearer token (Authorization header: %q)", sid, r.Header.Get("Authorization"))
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
			"instructions": `scitq2 — distributed task queue for scientific workloads.

## Core concepts

- **Task**: a command executed inside a Docker container on a worker. Tasks have inputs (downloaded before execution), resources (shared read-only data), and outputs (uploaded after execution). Status codes: W (waiting for dependencies), P (pending), A (assigned), C (accepted), D (downloading), O (on hold), R (running), U/V (uploading), S (succeeded), F (failed).
- **Step**: groups tasks that share the same container, resources, and compute requirements. Workers are assigned to steps, not to individual tasks.
- **Workflow**: groups steps into a pipeline. Steps within a workflow can have dependencies (inputs from a previous step). Workflow status: R (running), P (paused), D (debug), S (succeeded), F (failed/stuck).
- **Worker**: a compute instance (cloud VM or local machine) that executes tasks. Workers have concurrency (how many tasks run in parallel) and prefetch (how many tasks to download in advance while others run).
- **Flavor**: an instance type (CPU, memory, disk, GPU, cost). Use list_flavors with a filter to find suitable ones BEFORE deploying.
- **Provider**: a cloud provider configuration (e.g. azure.primary, openstack.ovh). Each provider has regions.
- **Recruiter**: auto-deploys workers for a step. Defined by a protofilter (e.g. "cpu>=8:mem>=60:provider=azure.primary"), max_workers cap, concurrency, and prefetch settings.

## YAML workflow templates

The primary way to define and run workflows is via YAML templates. Use list_templates to discover available templates, template_detail to inspect parameters, and run_template to execute.

When writing new YAML templates:
- Define all steps at once in a single template rather than submitting tasks one by one. This is more efficient and lets scitq manage the full DAG.
- Use template_detail on existing templates to understand the YAML structure and conventions.
- Use download_template to read the source of existing templates as examples.

## Operational guidance

### Start small
- Always cap recruitment with a parameter (e.g. max_workers as a template param) and start with 1 worker unless the template is well-tested or a close variant of one.
- Once tasks start completing successfully, increase recruitment via update_recruiter.

### Check flavors first
- Before launching a workflow, use list_flavors with appropriate filters (e.g. "cpu>=8:mem>=60:cost>0") to verify suitable instance types exist in the target provider/region.

### Prefetch tuning
Prefetch is key to scitq performance but tricky to set right. Diagnose live:
1. Compare running tasks vs expected (active_workers × concurrency). If running tasks are consistently below capacity, it's either a prefetch or warm-up issue.
2. **Warm-up**: if tasks use large resources (reference databases, mmap-heavy tools like hermes or bowtie2), the first task on each worker is slow while data loads into memory. This is normal and not a prefetch problem.
3. **Prefetch shortage**: if there are no large resources but tasks are quick (seconds to a few minutes), time is lost between tasks on download/preparation. Increase prefetch so the next task is ready before the current one finishes.
4. The right prefetch depends on task speed, input size, and worker disk capacity. It can be adjusted live via update_worker (prefetch is per-worker), but should also be updated in the YAML template for future runs.

### Troubleshooting
- Use task_status_counts to get a quick overview of workflow progress.
- Use task_logs to inspect stdout/stderr of failed tasks.
- Use list_worker_events to check worker lifecycle issues (install failures, evictions).
- Use retry_task or edit_and_retry_task to recover from failures.
- Use kill_task to stop a stuck or runaway task.

Always call login first to authenticate before using other tools.`,
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
					"workflow_id":  {Type: "integer", Description: "Filter by workflow ID"},
					"status":       {Type: "string", Description: "Filter by status (P, A, C, R, S, F, W)"},
					"limit":        {Type: "integer", Description: "Max results (default: 50)"},
					"show_hidden":  {Type: "boolean", Description: "Include hidden tasks (retried parents)"},
				},
			},
		},
		{
			Name:        "submit_task",
			Description: "Submit a task for execution. Returns the new task ID.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"command":   {Type: "string", Description: "Command to execute"},
					"container": {Type: "string", Description: "Docker container image"},
					"shell":     {Type: "string", Description: "Shell to use (sh, bash, python). Default: sh"},
				},
				Required: []string{"command", "container"},
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
			Name:        "deploy_worker",
			Description: "Deploy new worker instances on a cloud provider.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"provider":    {Type: "string", Description: "Provider (e.g. azure.primary, openstack.ovh)"},
					"flavor":      {Type: "string", Description: "Flavor name (e.g. Standard_D32s_v5)"},
					"region":      {Type: "string", Description: "Region (optional, defaults to provider default)"},
					"count":       {Type: "integer", Description: "Number of workers (default: 1)"},
					"concurrency": {Type: "integer", Description: "Initial concurrency (default: 1)"},
					"prefetch":    {Type: "integer", Description: "Initial prefetch (default: 0)"},
					"step_id":     {Type: "integer", Description: "Step ID to assign to (optional)"},
				},
				Required: []string{"provider", "flavor"},
			},
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
		{
			Name:        "update_worker",
			Description: "Update worker settings (step assignment, concurrency, prefetch, permanent flag, recyclable scope).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"worker_id":        {Type: "integer", Description: "Worker ID"},
					"step_id":          {Type: "integer", Description: "Step ID to assign to (optional)"},
					"concurrency":      {Type: "integer", Description: "New concurrency (optional)"},
					"prefetch":         {Type: "integer", Description: "New prefetch (optional)"},
					"permanent":        {Type: "boolean", Description: "Set as permanent (optional)"},
					"recyclable_scope": {Type: "string", Description: "Recyclable scope: S (step), W (workflow), G (global) (optional)"},
				},
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
			Name:        "kill_task",
			Description: "Send a kill signal to a running task. The task's container will be killed on the next worker ping.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]schemaProperty{"task_id": {Type: "integer", Description: "Task ID"}},
				Required:   []string{"task_id"},
			},
		},
		{
			Name:        "stop_task",
			Description: "Send a graceful stop signal (SIGTERM) to a running task. The task's container receives SIGTERM and has 10s to exit before SIGKILL.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]schemaProperty{"task_id": {Type: "integer", Description: "Task ID"}},
				Required:   []string{"task_id"},
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
			Description: "Update a workflow's status and/or maximum workers.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"workflow_id":    {Type: "integer", Description: "Workflow ID"},
					"status":        {Type: "string", Description: "New status: R (Running), P (Paused), D (Debug)"},
					"maximum_workers": {Type: "integer", Description: "Maximum workers allowed for this workflow"},
				},
				Required: []string{"workflow_id"},
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
			Name:        "upload_template",
			Description: "Upload a template script (YAML or Python). Returns the template ID and metadata.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"filename": {Type: "string", Description: "Filename with extension (e.g. biomscope.yaml or qc.py)"},
					"content":  {Type: "string", Description: "Template file content"},
					"force":    {Type: "boolean", Description: "Overwrite existing template with same name/version"},
				},
				Required: []string{"filename", "content"},
			},
		},
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
		// --- Recruiters ---
		{
			Name:        "list_recruiters",
			Description: "List recruiters, optionally filtered by step.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"step_id": {Type: "integer", Description: "Filter by step ID (optional)"},
				},
			},
		},
		{
			Name:        "create_recruiter",
			Description: "Create a recruiter for a step. Protofilter defines worker selection (e.g. 'cpu>=32:mem>=120').",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"step_id":      {Type: "integer", Description: "Step ID"},
					"protofilter":  {Type: "string", Description: "Worker filter (e.g. cpu>=32:mem>=120:provider=azure.primary)"},
					"rank":         {Type: "integer", Description: "Rank (default: 1)"},
					"max_workers":  {Type: "integer", Description: "Maximum workers to recruit"},
					"concurrency":  {Type: "integer", Description: "Static concurrency per worker"},
					"prefetch":     {Type: "integer", Description: "Prefetch count"},
					"cpu_per_task": {Type: "integer", Description: "CPU per task (dynamic concurrency)"},
					"rounds":       {Type: "integer", Description: "Recruitment rounds (default: 1)"},
					"timeout":      {Type: "integer", Description: "Timeout in seconds"},
				},
				Required: []string{"step_id", "protofilter"},
			},
		},
		{
			Name:        "update_recruiter",
			Description: "Update a recruiter's settings.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"step_id":      {Type: "integer", Description: "Step ID"},
					"rank":         {Type: "integer", Description: "Rank"},
					"protofilter":  {Type: "string", Description: "Updated worker filter"},
					"max_workers":  {Type: "integer", Description: "Updated max workers"},
					"concurrency":  {Type: "integer", Description: "Updated concurrency"},
					"prefetch":     {Type: "integer", Description: "Updated prefetch"},
					"cpu_per_task": {Type: "integer", Description: "Updated CPU per task"},
				},
				Required: []string{"step_id", "rank"},
			},
		},
		{
			Name:        "delete_recruiter",
			Description: "Delete a recruiter.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"step_id": {Type: "integer", Description: "Step ID"},
					"rank":    {Type: "integer", Description: "Rank"},
				},
				Required: []string{"step_id", "rank"},
			},
		},
		// --- Flavors ---
		{
			Name:        "list_flavors",
			Description: "List available instance flavors. Use filter to narrow results (e.g. cpu>=8:mem>=30:cost>0).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"filter": {Type: "string", Description: "Filter expression (e.g. cpu>=32:mem>=120:provider=azure.primary:cost>0)"},
					"limit":  {Type: "integer", Description: "Max results (default: 20)"},
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
		// --- Worker events ---
		{
			Name:        "list_worker_events",
			Description: "List worker events (logs from worker lifecycle: install, bootstrap, runtime errors).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"worker_id": {Type: "integer", Description: "Filter by worker ID (optional)"},
					"level":     {Type: "string", Description: "Filter by level: D (debug), I (info), W (warning), E (error)"},
					"class":     {Type: "string", Description: "Filter by event class (e.g. install, bootstrap, runtime)"},
					"limit":     {Type: "integer", Description: "Max events (default: 50)"},
				},
			},
		},
		{
			Name:        "prune_worker_events",
			Description: "Delete old worker events. Use dry_run=true to preview.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProperty{
					"before":    {Type: "string", Description: "Delete events before this date (RFC3339, e.g. 2025-01-01T00:00:00Z)"},
					"level":     {Type: "string", Description: "Filter by level"},
					"class":     {Type: "string", Description: "Filter by event class"},
					"worker_id": {Type: "integer", Description: "Filter by worker ID"},
					"dry_run":   {Type: "boolean", Description: "Preview only, don't delete"},
				},
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
	call.Arguments = normalizeArgs(call.Arguments)

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
	case "upload_template":
		return h.toolUploadTemplate(authCtx, call.Arguments)
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
	case "submit_task":
		return h.toolSubmitTask(authCtx, call.Arguments)
	case "task_logs":
		return h.toolTaskLogs(authCtx, call.Arguments)
	case "retry_task":
		return h.toolRetryTask(authCtx, call.Arguments)
	case "force_run_task":
		return h.toolForceRunTask(authCtx, call.Arguments)
	case "kill_task":
		return h.toolKillTask(authCtx, call.Arguments)
	case "stop_task":
		return h.toolStopTask(authCtx, call.Arguments)
	case "task_status_counts":
		return h.toolTaskStatusCounts(authCtx, call.Arguments)
	case "list_workers":
		return h.toolListWorkers(authCtx)
	case "deploy_worker":
		return h.toolDeployWorker(authCtx, call.Arguments)
	case "delete_worker":
		return h.toolDeleteWorker(authCtx, call.Arguments)
	case "update_worker":
		return h.toolUpdateWorker(authCtx, call.Arguments)
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
	case "list_recruiters":
		return h.toolListRecruiters(authCtx, call.Arguments)
	case "create_recruiter":
		return h.toolCreateRecruiter(authCtx, call.Arguments)
	case "update_recruiter":
		return h.toolUpdateRecruiter(authCtx, call.Arguments)
	case "delete_recruiter":
		return h.toolDeleteRecruiter(authCtx, call.Arguments)
	case "list_flavors":
		return h.toolListFlavors(authCtx, call.Arguments)
	case "file_list":
		return h.toolFileList(authCtx, call.Arguments)
	case "list_worker_events":
		return h.toolListWorkerEvents(authCtx, call.Arguments)
	case "prune_worker_events":
		return h.toolPruneWorkerEvents(authCtx, call.Arguments)
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
		ShowHidden bool   `json:"show_hidden"`
	}
	json.Unmarshal(args, &p)

	req := &pb.ListTasksRequest{}
	if p.WorkflowID != 0 {
		req.WorkflowIdFilter = &p.WorkflowID
	}
	if p.Status != "" {
		req.StatusFilter = &p.Status
	}
	if p.ShowHidden {
		req.ShowHidden = &p.ShowHidden
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
	// Return summary (without full command text which can be huge)
	type taskSummary struct {
		TaskID           int32  `json:"task_id"`
		TaskName         string `json:"task_name,omitempty"`
		Status           string `json:"status"`
		StepID           int32  `json:"step_id,omitempty"`
		WorkerID         int32  `json:"worker_id,omitempty"`
		WorkflowID       int32  `json:"workflow_id,omitempty"`
		Container        string `json:"container"`
		RetryCount       int32  `json:"retry_count,omitempty"`
		DownloadDuration int32  `json:"download_duration,omitempty"`
		RunDuration      int32  `json:"run_duration,omitempty"`
		UploadDuration   int32  `json:"upload_duration,omitempty"`
	}
	summaries := make([]taskSummary, 0, len(res.Tasks))
	for _, t := range res.Tasks {
		s := taskSummary{
			TaskID:     t.TaskId,
			Status:     t.Status,
			Container:  t.Container,
			RetryCount: t.RetryCount,
		}
		if t.TaskName != nil { s.TaskName = *t.TaskName }
		if t.StepId != nil { s.StepID = *t.StepId }
		if t.WorkerId != nil { s.WorkerID = *t.WorkerId }
		if t.WorkflowId != nil { s.WorkflowID = *t.WorkflowId }
		if t.DownloadDuration != nil { s.DownloadDuration = *t.DownloadDuration }
		if t.RunDuration != nil { s.RunDuration = *t.RunDuration }
		if t.UploadDuration != nil { s.UploadDuration = *t.UploadDuration }
		summaries = append(summaries, s)
	}
	return jsonResult(summaries), nil
}

func (h *mcpHandler) toolSubmitTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Command   string `json:"command"`
		Container string `json:"container"`
		Shell     string `json:"shell"`
	}
	json.Unmarshal(args, &p)
	req := &pb.TaskRequest{
		Command:   p.Command,
		Container: p.Container,
		Status:    "P",
	}
	if p.Shell != "" {
		req.Shell = &p.Shell
	}
	res, err := h.server.SubmitTask(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]any{"task_id": res.TaskId}), nil
}

func (h *mcpHandler) toolTaskLogs(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		TaskID int32 `json:"task_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(fmt.Errorf("invalid arguments: %w (raw: %s)", err, string(args))), nil
	}
	if p.TaskID == 0 {
		return errorResult(fmt.Errorf("task_id is required (raw args: %s)", string(args))), nil
	}

	res, err := h.server.GetLogsChunk(ctx, &pb.GetLogsRequest{
		TaskIds:   []int32{p.TaskID},
		ChunkSize: 10000,
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

func (h *mcpHandler) toolDeployWorker(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Provider    string `json:"provider"`
		Flavor      string `json:"flavor"`
		Region      string `json:"region"`
		Count       int32  `json:"count"`
		Concurrency int32  `json:"concurrency"`
		Prefetch    int32  `json:"prefetch"`
		StepID      int32  `json:"step_id"`
	}
	json.Unmarshal(args, &p)
	req := &pb.CreateWorkerByNameRequest{
		Provider:    p.Provider,
		Flavor:      p.Flavor,
		Region:      p.Region,
		Count:       p.Count,
		Concurrency: p.Concurrency,
		Prefetch:    p.Prefetch,
	}
	if p.StepID != 0 {
		req.StepId = &p.StepID
	}
	res, err := h.server.CreateWorkerByName(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	var ids []int32
	for _, w := range res.WorkersDetails {
		ids = append(ids, w.WorkerId)
	}
	return jsonResult(map[string]any{"worker_ids": ids}), nil
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

func (h *mcpHandler) toolUpdateWorker(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		WorkerID        int32  `json:"worker_id"`
		StepID          int32  `json:"step_id"`
		Concurrency     int32  `json:"concurrency"`
		Prefetch        int32  `json:"prefetch"`
		Permanent       bool   `json:"permanent"`
		RecyclableScope string `json:"recyclable_scope"`
	}
	json.Unmarshal(args, &p)
	req := &pb.WorkerUpdateRequest{WorkerId: p.WorkerID}
	if p.StepID != 0 {
		req.StepId = &p.StepID
	}
	if p.Concurrency != 0 {
		req.Concurrency = &p.Concurrency
	}
	if p.Prefetch != 0 {
		req.Prefetch = &p.Prefetch
	}
	if p.Permanent {
		v := true
		req.IsPermanent = &v
	}
	if p.RecyclableScope != "" {
		req.RecyclableScope = &p.RecyclableScope
	}
	_, err := h.server.UserUpdateWorker(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Worker %d updated", p.WorkerID)), nil
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

func (h *mcpHandler) toolKillTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ TaskID int32 `json:"task_id"` }
	json.Unmarshal(args, &p)
	_, err := h.server.SignalTask(ctx, &pb.TaskSignalRequest{TaskId: p.TaskID, Signal: "K"})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Kill signal queued for task %d", p.TaskID)), nil
}

func (h *mcpHandler) toolStopTask(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ TaskID int32 `json:"task_id"` }
	json.Unmarshal(args, &p)
	_, err := h.server.SignalTask(ctx, &pb.TaskSignalRequest{TaskId: p.TaskID, Signal: "T"})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Stop signal (SIGTERM) queued for task %d", p.TaskID)), nil
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
		WorkflowID     int32  `json:"workflow_id"`
		Status         string `json:"status"`
		MaximumWorkers int32  `json:"maximum_workers"`
	}
	json.Unmarshal(args, &p)
	req := &pb.WorkflowStatusUpdate{
		WorkflowId: p.WorkflowID,
		Status:     p.Status,
	}
	if p.MaximumWorkers != 0 {
		req.MaximumWorkers = &p.MaximumWorkers
	}
	_, err := h.server.UpdateWorkflowStatus(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	msg := fmt.Sprintf("Workflow %d updated", p.WorkflowID)
	if p.Status != "" {
		msg += fmt.Sprintf(" (status=%s)", p.Status)
	}
	if p.MaximumWorkers != 0 {
		msg += fmt.Sprintf(" (maximum_workers=%d)", p.MaximumWorkers)
	}
	return textResult(msg), nil
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

func (h *mcpHandler) toolUploadTemplate(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
		Force    bool   `json:"force"`
	}
	json.Unmarshal(args, &p)
	resp, err := h.server.UploadTemplate(ctx, &pb.UploadTemplateRequest{
		Script:   []byte(p.Content),
		Force:    p.Force,
		Filename: &p.Filename,
	})
	if err != nil {
		return errorResult(err), nil
	}
	if !resp.Success {
		return errorResult(fmt.Errorf("%s", resp.Message)), nil
	}
	return jsonResult(map[string]any{
		"template_id": resp.GetWorkflowTemplateId(),
		"name":        resp.GetName(),
		"version":     resp.GetVersion(),
		"description": resp.GetDescription(),
	}), nil
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

func (h *mcpHandler) toolListRecruiters(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct{ StepID int32 `json:"step_id"` }
	json.Unmarshal(args, &p)
	filter := &pb.RecruiterFilter{}
	if p.StepID != 0 {
		filter.StepId = &p.StepID
	}
	res, err := h.server.ListRecruiters(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Recruiters), nil
}

func (h *mcpHandler) toolCreateRecruiter(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		StepID      int32  `json:"step_id"`
		Protofilter string `json:"protofilter"`
		Rank        int32  `json:"rank"`
		MaxWorkers  int32  `json:"max_workers"`
		Concurrency int32  `json:"concurrency"`
		Prefetch    int32  `json:"prefetch"`
		CpuPerTask  int32  `json:"cpu_per_task"`
		Rounds      int32  `json:"rounds"`
		Timeout     int32  `json:"timeout"`
	}
	json.Unmarshal(args, &p)
	if p.Rank == 0 { p.Rank = 1 }
	if p.Rounds == 0 { p.Rounds = 1 }
	req := &pb.Recruiter{
		StepId:      p.StepID,
		Rank:        p.Rank,
		Protofilter: p.Protofilter,
		Rounds:      p.Rounds,
		Timeout:     p.Timeout,
	}
	if p.MaxWorkers != 0 { req.MaxWorkers = &p.MaxWorkers }
	if p.Concurrency != 0 { req.Concurrency = &p.Concurrency }
	if p.Prefetch != 0 { req.Prefetch = &p.Prefetch }
	if p.CpuPerTask != 0 { req.CpuPerTask = &p.CpuPerTask }
	_, err := h.server.CreateRecruiter(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Recruiter created for step %d rank %d", p.StepID, p.Rank)), nil
}

func (h *mcpHandler) toolUpdateRecruiter(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		StepID      int32  `json:"step_id"`
		Rank        int32  `json:"rank"`
		Protofilter string `json:"protofilter"`
		MaxWorkers  int32  `json:"max_workers"`
		Concurrency int32  `json:"concurrency"`
		Prefetch    int32  `json:"prefetch"`
		CpuPerTask  int32  `json:"cpu_per_task"`
	}
	json.Unmarshal(args, &p)
	req := &pb.RecruiterUpdate{
		StepId: p.StepID,
		Rank:   p.Rank,
	}
	if p.Protofilter != "" { req.Protofilter = &p.Protofilter }
	if p.MaxWorkers != 0 { req.MaxWorkers = &p.MaxWorkers }
	if p.Concurrency != 0 { req.Concurrency = &p.Concurrency }
	if p.Prefetch != 0 { req.Prefetch = &p.Prefetch }
	if p.CpuPerTask != 0 { req.CpuPerTask = &p.CpuPerTask }
	_, err := h.server.UpdateRecruiter(ctx, req)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Recruiter updated for step %d rank %d", p.StepID, p.Rank)), nil
}

func (h *mcpHandler) toolDeleteRecruiter(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		StepID int32 `json:"step_id"`
		Rank   int32 `json:"rank"`
	}
	json.Unmarshal(args, &p)
	_, err := h.server.DeleteRecruiter(ctx, &pb.RecruiterId{StepId: p.StepID, Rank: p.Rank})
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(fmt.Sprintf("Recruiter deleted for step %d rank %d", p.StepID, p.Rank)), nil
}

func (h *mcpHandler) toolListFlavors(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Filter string `json:"filter"`
		Limit  int32  `json:"limit"`
	}
	json.Unmarshal(args, &p)
	if p.Limit == 0 {
		p.Limit = 20
	}
	res, err := h.server.ListFlavors(ctx, &pb.ListFlavorsRequest{
		Filter: p.Filter,
		Limit:  p.Limit,
	})
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Flavors), nil
}

func (h *mcpHandler) toolListWorkerEvents(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		WorkerID int32  `json:"worker_id"`
		Level    string `json:"level"`
		Class    string `json:"class"`
		Limit    int32  `json:"limit"`
	}
	json.Unmarshal(args, &p)
	filter := &pb.WorkerEventFilter{}
	if p.WorkerID != 0 { filter.WorkerId = &p.WorkerID }
	if p.Level != "" { filter.Level = &p.Level }
	if p.Class != "" { filter.Class = &p.Class }
	if p.Limit != 0 {
		filter.Limit = &p.Limit
	} else {
		limit := int32(50)
		filter.Limit = &limit
	}
	res, err := h.server.ListWorkerEvents(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(res.Events), nil
}

func (h *mcpHandler) toolPruneWorkerEvents(ctx context.Context, args json.RawMessage) (any, *rpcError) {
	var p struct {
		Before   string `json:"before"`
		Level    string `json:"level"`
		Class    string `json:"class"`
		WorkerID int32  `json:"worker_id"`
		DryRun   bool   `json:"dry_run"`
	}
	json.Unmarshal(args, &p)
	filter := &pb.WorkerEventPruneFilter{DryRun: p.DryRun}
	if p.Before != "" { filter.Before = &p.Before }
	if p.Level != "" { filter.Level = &p.Level }
	if p.Class != "" { filter.Class = &p.Class }
	if p.WorkerID != 0 { filter.WorkerId = &p.WorkerID }
	res, err := h.server.PruneWorkerEvents(ctx, filter)
	if err != nil {
		return errorResult(err), nil
	}
	return jsonResult(map[string]int32{"matched": res.Matched, "deleted": res.Deleted}), nil
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
	// Always store the raw token so extractTokenFromContext can find it
	// (needed by RunTemplate to pass SCITQ_TOKEN to the script runner)
	ctx = context.WithValue(ctx, tokenContextKey, token)

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
