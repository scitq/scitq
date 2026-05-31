package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	ws "github.com/scitq/scitq/server/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Workflow chain — template-to-template sequencing.
// See specs/workflow_chain.md for the full design.
//
// This file implements the CRUD + lifecycle RPCs. The actual firing of a
// chain entry (resolving parent.* references in params_template, looking
// up the target template, submitting the child RunTemplateRequest, then
// transitioning the entry to `fired` / `failed`) lives in workflow_chain_fire.go
// alongside the parent-completion hook — both share the same per-entry
// firing primitive.

// chainEntryColumns is the canonical SELECT projection. All scanChainEntry
// callers MUST select these columns, in this order.
const chainEntryColumns = `
	chain_entry_id,
	parent_workflow_id,
	idx,
	template_name,
	template_version,
	params_template::text,
	when_expr,
	on_status,
	always_new,
	status,
	child_workflow_id,
	error_message,
	created_at,
	fired_at,
	last_fired_at
`

// rowScanner abstracts *sql.Row and *sql.Rows for a single scanChainEntry
// helper (no separate code paths).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanChainEntry(sc rowScanner) (*pb.ChainEntry, error) {
	var (
		e               pb.ChainEntry
		templateVersion sql.NullString
		childWorkflowID sql.NullInt32
		errorMessage    sql.NullString
		createdAt       sql.NullTime
		firedAt         sql.NullTime
		lastFiredAt     sql.NullTime
	)
	if err := sc.Scan(
		&e.ChainEntryId,
		&e.ParentWorkflowId,
		&e.Idx,
		&e.TemplateName,
		&templateVersion,
		&e.ParamsTemplateJson,
		&e.WhenExpr,
		&e.OnStatus,
		&e.AlwaysNew,
		&e.Status,
		&childWorkflowID,
		&errorMessage,
		&createdAt,
		&firedAt,
		&lastFiredAt,
	); err != nil {
		return nil, err
	}
	if templateVersion.Valid {
		v := templateVersion.String
		e.TemplateVersion = &v
	}
	if childWorkflowID.Valid {
		v := childWorkflowID.Int32
		e.ChildWorkflowId = &v
	}
	if errorMessage.Valid {
		v := errorMessage.String
		e.ErrorMessage = &v
	}
	if createdAt.Valid {
		e.CreatedAt = createdAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if firedAt.Valid {
		s := firedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		e.FiredAt = &s
	}
	if lastFiredAt.Valid {
		s := lastFiredAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		e.LastFiredAt = &s
	}
	return &e, nil
}

// normalizeOnStatus accepts the canonical lowercase form and rejects anything
// outside the closed enum {succeeded, always, failed}. Matches the CHECK
// constraint in migration 32.
func normalizeOnStatus(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "succeeded", "":
		return "succeeded", nil
	case "always":
		return "always", nil
	case "failed":
		return "failed", nil
	}
	return "", fmt.Errorf("invalid on_status %q (must be succeeded|always|failed)", s)
}

// validateParamsTemplate ensures the JSON parses as an object (the only
// shape the firing logic knows how to walk).
func validateParamsTemplate(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil // empty defaults to {} via the column default
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return fmt.Errorf("params_template_json must be a JSON object: %w", err)
	}
	return nil
}

// emitChainEntryEvent broadcasts a lifecycle change so the UI / CLI watchers
// see the new state without polling. The shape mirrors workflow status
// events: id + status.
func emitChainEntryEvent(entryID int32, parentWorkflowID int32, newStatus string) {
	ws.EmitWS("chain-entry", entryID, "status", struct {
		ChainEntryId     int32  `json:"chainEntryId"`
		ParentWorkflowId int32  `json:"parentWorkflowId"`
		Status           string `json:"status"`
	}{
		ChainEntryId:     entryID,
		ParentWorkflowId: parentWorkflowID,
		Status:           newStatus,
	})
}

// ===========================================================================
// RPCs
// ===========================================================================

// CreateChainEntries inserts one row per draft, assigning sequential `idx`
// values. Called once at parent-submit time by the runner (after the
// workflow row exists). Idempotent on the entry list: re-calling with the
// same workflow_id appends — the runner is expected to call exactly once.
func (s *taskQueueServer) CreateChainEntries(ctx context.Context, req *pb.CreateChainEntriesRequest) (*pb.ChainEntryList, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.WorkflowId == 0 {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}
	if len(req.Entries) == 0 {
		return &pb.ChainEntryList{Entries: []*pb.ChainEntry{}}, nil
	}

	// Verify the workflow exists before inserting children of it (the FK
	// would catch this too, but we get a cleaner error).
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM workflow WHERE workflow_id = $1)`,
		req.WorkflowId).Scan(&exists); err != nil {
		return nil, fmt.Errorf("workflow lookup failed: %w", err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "workflow %d not found", req.WorkflowId)
	}

	// Find the current max idx so re-calls append cleanly.
	var nextIdx int32
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(idx), -1) + 1 FROM workflow_chain_entry WHERE parent_workflow_id = $1`,
		req.WorkflowId).Scan(&nextIdx); err != nil {
		return nil, fmt.Errorf("idx lookup failed: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	out := make([]*pb.ChainEntry, 0, len(req.Entries))
	for i, draft := range req.Entries {
		if strings.TrimSpace(draft.TemplateName) == "" {
			return nil, status.Errorf(codes.InvalidArgument, "entry %d: template_name is required", i)
		}
		onStatus, err := normalizeOnStatus(draft.OnStatus)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "entry %d: %v", i, err)
		}
		paramsJSON := draft.ParamsTemplateJson
		if paramsJSON == "" {
			paramsJSON = "{}"
		}
		if err := validateParamsTemplate(paramsJSON); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "entry %d: %v", i, err)
		}
		whenExpr := strings.TrimSpace(draft.WhenExpr)
		if whenExpr == "" {
			whenExpr = "true"
		}

		var templateVersion sql.NullString
		if draft.TemplateVersion != nil && *draft.TemplateVersion != "" {
			templateVersion = sql.NullString{String: *draft.TemplateVersion, Valid: true}
		}

		row := tx.QueryRowContext(ctx, `
			INSERT INTO workflow_chain_entry (
				parent_workflow_id, idx,
				template_name, template_version,
				params_template, when_expr, on_status, always_new
			) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
			RETURNING `+chainEntryColumns,
			req.WorkflowId, nextIdx+int32(i),
			draft.TemplateName, templateVersion,
			paramsJSON, whenExpr, onStatus, draft.AlwaysNew,
		)
		entry, err := scanChainEntry(row)
		if err != nil {
			return nil, fmt.Errorf("insert entry %d: %w", i, err)
		}
		out = append(out, entry)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	for _, e := range out {
		emitChainEntryEvent(e.ChainEntryId, e.ParentWorkflowId, e.Status)
	}
	return &pb.ChainEntryList{Entries: out}, nil
}

// ListChainEntries returns entries filtered by parent workflow and/or
// lifecycle status. With no filters, returns all entries (capped sensibly).
func (s *taskQueueServer) ListChainEntries(ctx context.Context, req *pb.ListChainEntriesRequest) (*pb.ChainEntryList, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}

	var (
		clauses []string
		args    []any
	)
	if req.ParentWorkflowId != nil {
		args = append(args, *req.ParentWorkflowId)
		clauses = append(clauses, fmt.Sprintf("parent_workflow_id = $%d", len(args)))
	}
	if req.StatusFilter != nil && *req.StatusFilter != "" {
		args = append(args, *req.StatusFilter)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}

	q := `SELECT ` + chainEntryColumns + ` FROM workflow_chain_entry`
	if len(clauses) > 0 {
		q += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	q += ` ORDER BY parent_workflow_id, idx`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list chain entries: %w", err)
	}
	defer rows.Close()

	out := []*pb.ChainEntry{}
	for rows.Next() {
		e, err := scanChainEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chain entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chain entries: %w", err)
	}
	return &pb.ChainEntryList{Entries: out}, nil
}

// GetChainEntry returns one entry by id.
func (s *taskQueueServer) GetChainEntry(ctx context.Context, req *pb.ChainEntryId) (*pb.ChainEntry, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ChainEntryId == 0 {
		return nil, status.Error(codes.InvalidArgument, "chain_entry_id is required")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+chainEntryColumns+` FROM workflow_chain_entry WHERE chain_entry_id = $1`,
		req.ChainEntryId)
	e, err := scanChainEntry(row)
	if err == sql.ErrNoRows {
		return nil, status.Errorf(codes.NotFound, "chain entry %d not found", req.ChainEntryId)
	}
	if err != nil {
		return nil, fmt.Errorf("get chain entry: %w", err)
	}
	return e, nil
}

// SuspendChainEntry: pending → suspended. Any other source state is an
// error (resume from suspended, cancel from pending/suspended).
func (s *taskQueueServer) SuspendChainEntry(ctx context.Context, req *pb.ChainEntryId) (*pb.ChainEntry, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ChainEntryId == 0 {
		return nil, status.Error(codes.InvalidArgument, "chain_entry_id is required")
	}
	return s.transitionChainEntry(ctx, req.ChainEntryId, []string{"pending"}, "suspended")
}

// ResumeChainEntry: suspended → pending. If the parent has already reached
// a terminal status while the entry was suspended, the entry is immediately
// re-evaluated against the actual terminal status (this is the
// "I-suspended-to-fix-the-child, parent finished while I was fixing it,
// now resume → fire" path).
func (s *taskQueueServer) ResumeChainEntry(ctx context.Context, req *pb.ChainEntryId) (*pb.ChainEntry, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ChainEntryId == 0 {
		return nil, status.Error(codes.InvalidArgument, "chain_entry_id is required")
	}
	entry, err := s.transitionChainEntry(ctx, req.ChainEntryId, []string{"suspended"}, "pending")
	if err != nil {
		return nil, err
	}

	// If the parent has already reached a terminal status, evaluate the
	// entry immediately rather than leaving it stranded in `pending` (no
	// completion hook will fire again for an already-terminal parent).
	var parentStatus string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM workflow WHERE workflow_id = $1`,
		entry.ParentWorkflowId).Scan(&parentStatus); err == nil && (parentStatus == "S" || parentStatus == "F") {
		// fireChainEntry transitions the entry to its terminal state
		// (fired / skipped / failed) and updates the row. We re-read
		// after firing so the caller sees the post-fire state.
		go s.fireChainEntry(context.Background(), entry.ChainEntryId, parentStatus)
		// Best-effort re-read so the response reflects the firing if it
		// completed synchronously (it usually will). We swallow read
		// errors and fall back to returning the post-resume entry.
		if fresh, ferr := s.GetChainEntry(ctx, &pb.ChainEntryId{ChainEntryId: entry.ChainEntryId}); ferr == nil {
			return fresh, nil
		}
	}
	return entry, nil
}

// CancelChainEntry: pending|suspended → cancelled (terminal).
func (s *taskQueueServer) CancelChainEntry(ctx context.Context, req *pb.ChainEntryId) (*pb.ChainEntry, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ChainEntryId == 0 {
		return nil, status.Error(codes.InvalidArgument, "chain_entry_id is required")
	}
	return s.transitionChainEntry(ctx, req.ChainEntryId, []string{"pending", "suspended"}, "cancelled")
}

// EditChainEntry updates mutable fields (template_name, template_version,
// params_template, when_expr, on_status, always_new). Allowed only while
// status='suspended' — editing a pending entry races with the completion
// hook; editing a terminal entry is meaningless.
func (s *taskQueueServer) EditChainEntry(ctx context.Context, req *pb.EditChainEntryRequest) (*pb.ChainEntry, error) {
	if GetUserFromContext(ctx) == nil {
		return nil, status.Error(codes.PermissionDenied, "user authentication required")
	}
	if req.ChainEntryId == 0 {
		return nil, status.Error(codes.InvalidArgument, "chain_entry_id is required")
	}

	var (
		setClauses []string
		args       []any
	)
	if req.TemplateName != nil {
		if strings.TrimSpace(*req.TemplateName) == "" {
			return nil, status.Error(codes.InvalidArgument, "template_name cannot be empty")
		}
		args = append(args, *req.TemplateName)
		setClauses = append(setClauses, fmt.Sprintf("template_name = $%d", len(args)))
	}
	if req.TemplateVersion != nil {
		// Empty string clears the pin (resolve latest at fire time).
		if *req.TemplateVersion == "" {
			setClauses = append(setClauses, "template_version = NULL")
		} else {
			args = append(args, *req.TemplateVersion)
			setClauses = append(setClauses, fmt.Sprintf("template_version = $%d", len(args)))
		}
	}
	if req.ParamsTemplateJson != nil {
		if err := validateParamsTemplate(*req.ParamsTemplateJson); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		paramsJSON := *req.ParamsTemplateJson
		if strings.TrimSpace(paramsJSON) == "" {
			paramsJSON = "{}"
		}
		args = append(args, paramsJSON)
		setClauses = append(setClauses, fmt.Sprintf("params_template = $%d::jsonb", len(args)))
	}
	if req.WhenExpr != nil {
		whenExpr := strings.TrimSpace(*req.WhenExpr)
		if whenExpr == "" {
			whenExpr = "true"
		}
		args = append(args, whenExpr)
		setClauses = append(setClauses, fmt.Sprintf("when_expr = $%d", len(args)))
	}
	if req.OnStatus != nil {
		onStatus, err := normalizeOnStatus(*req.OnStatus)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		args = append(args, onStatus)
		setClauses = append(setClauses, fmt.Sprintf("on_status = $%d", len(args)))
	}
	if req.AlwaysNew != nil {
		args = append(args, *req.AlwaysNew)
		setClauses = append(setClauses, fmt.Sprintf("always_new = $%d", len(args)))
	}

	if len(setClauses) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no editable fields provided")
	}

	args = append(args, req.ChainEntryId)
	q := fmt.Sprintf(
		`UPDATE workflow_chain_entry SET %s
		  WHERE chain_entry_id = $%d AND status = 'suspended'
		  RETURNING %s`,
		strings.Join(setClauses, ", "),
		len(args),
		chainEntryColumns,
	)
	row := s.db.QueryRowContext(ctx, q, args...)
	entry, err := scanChainEntry(row)
	if err == sql.ErrNoRows {
		// Distinguish "not found" from "not suspended" with one extra
		// lookup so the operator sees the right error.
		var current string
		err2 := s.db.QueryRowContext(ctx,
			`SELECT status FROM workflow_chain_entry WHERE chain_entry_id = $1`,
			req.ChainEntryId).Scan(&current)
		if err2 == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "chain entry %d not found", req.ChainEntryId)
		}
		if err2 != nil {
			return nil, fmt.Errorf("edit chain entry: %w", err2)
		}
		return nil, status.Errorf(codes.FailedPrecondition,
			"chain entry %d is %s — only suspended entries can be edited", req.ChainEntryId, current)
	}
	if err != nil {
		return nil, fmt.Errorf("edit chain entry: %w", err)
	}
	emitChainEntryEvent(entry.ChainEntryId, entry.ParentWorkflowId, entry.Status)
	return entry, nil
}

// transitionChainEntry is the workhorse for the state-machine RPCs:
// updates status only if the current status is one of `fromStatuses`,
// then returns the new row. The single UPDATE-with-WHERE-and-RETURNING
// keeps the transition atomic without a separate read.
func (s *taskQueueServer) transitionChainEntry(
	ctx context.Context,
	entryID int32,
	fromStatuses []string,
	toStatus string,
) (*pb.ChainEntry, error) {
	// Build the IN list for the WHERE clause.
	placeholders := make([]string, len(fromStatuses))
	args := []any{toStatus}
	for i, st := range fromStatuses {
		placeholders[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, st)
	}
	args = append(args, entryID)
	q := fmt.Sprintf(
		`UPDATE workflow_chain_entry SET status = $1
		  WHERE chain_entry_id = $%d AND status IN (%s)
		  RETURNING %s`,
		len(args),
		strings.Join(placeholders, ", "),
		chainEntryColumns,
	)
	row := s.db.QueryRowContext(ctx, q, args...)
	entry, err := scanChainEntry(row)
	if err == sql.ErrNoRows {
		// Distinguish "not found" from "wrong state" with one extra
		// lookup so the operator sees the right error.
		var current string
		err2 := s.db.QueryRowContext(ctx,
			`SELECT status FROM workflow_chain_entry WHERE chain_entry_id = $1`,
			entryID).Scan(&current)
		if err2 == sql.ErrNoRows {
			return nil, status.Errorf(codes.NotFound, "chain entry %d not found", entryID)
		}
		if err2 != nil {
			return nil, fmt.Errorf("transition chain entry: %w", err2)
		}
		return nil, status.Errorf(codes.FailedPrecondition,
			"chain entry %d is %s — cannot transition to %s (allowed sources: %s)",
			entryID, current, toStatus, strings.Join(fromStatuses, ", "))
	}
	if err != nil {
		return nil, fmt.Errorf("transition chain entry: %w", err)
	}
	emitChainEntryEvent(entry.ChainEntryId, entry.ParentWorkflowId, entry.Status)
	return entry, nil
}

// fireChainEntry is implemented in workflow_chain_fire.go alongside the
// completion hook — both share the same per-entry firing primitive.
