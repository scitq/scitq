package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// issueInternalSession registers a short-lived `scitq_user_session` row for
// the given user and returns the opaque token. Used by chain firing so the
// venv'd Python child can authenticate back as the parent's run_by user
// while RunTemplate's scriptRunner subprocess runs. The caller MUST delete
// the row when firing completes (defer the cleanup).
func issueInternalSession(ctx context.Context, db *sql.DB, userID int32, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	expiresAt := time.Now().Add(ttl)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scitq_user_session (user_id, session_id, expires_at)
		VALUES ($1, $2, $3)
	`, userID, token, expiresAt); err != nil {
		return "", err
	}
	return token, nil
}

// Workflow chain — firing primitive + completion hook.
// See specs/workflow_chain.md.
//
// fireChainEntry is the single per-entry firing primitive, called from
// two sources:
//   1) the parent-workflow-completion hook (evaluateChainEntriesForWorkflow,
//      kicked off from recomputeWorkflowStatus when a workflow reaches a
//      terminal status)
//   2) ResumeChainEntry, when an operator resumes a suspended entry and
//      the parent has *already* reached a terminal status while the entry
//      was paused.
//
// Both paths converge on fireChainEntry so the lifecycle transitions and
// the parent.* resolver only have one implementation.

// firingTimeout caps how long a single chain-entry firing can take —
// covers param resolution + template lookup + the child RunTemplate call.
// Generous because RunTemplate runs the venv'd python runner inline.
const firingTimeout = 5 * time.Minute

// chainOnStatusMatches: does the entry's `on_status` filter match the
// parent's actual terminal status?
func chainOnStatusMatches(onStatus, terminalStatus string) bool {
	switch onStatus {
	case "succeeded":
		return terminalStatus == "S"
	case "failed":
		return terminalStatus == "F"
	case "always":
		return terminalStatus == "S" || terminalStatus == "F"
	}
	return false
}

// isChainTruthy mirrors the YAML engine's falsy set (docs/usage/yaml-templates.md
// — empty, "false", "no", "none"). Trimmed + case-insensitive.
func isChainTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "no", "none", "0":
		return false
	}
	return true
}

// parentInfo collects everything the resolver needs about the parent
// workflow, loaded once per firing.
type parentInfo struct {
	WorkflowID      int32
	WorkflowName    string
	TemplateRunID   sql.NullInt32
	RunBy           sql.NullInt32
	RunByUsername   sql.NullString
	ParamValuesJSON sql.NullString
}

func (s *taskQueueServer) loadParentInfo(ctx context.Context, workflowID int32) (*parentInfo, error) {
	p := &parentInfo{WorkflowID: workflowID}
	err := s.db.QueryRowContext(ctx, `
		SELECT w.workflow_name,
		       tr.template_run_id,
		       tr.run_by,
		       u.username,
		       tr.param_values::text
		  FROM workflow w
		  LEFT JOIN template_run tr ON tr.workflow_id = w.workflow_id
		  LEFT JOIN scitq_user u ON u.user_id = tr.run_by
		 WHERE w.workflow_id = $1
		 ORDER BY tr.created_at DESC NULLS LAST
		 LIMIT 1
	`, workflowID).Scan(&p.WorkflowName, &p.TemplateRunID, &p.RunBy, &p.RunByUsername, &p.ParamValuesJSON)
	if err != nil {
		return nil, fmt.Errorf("load parent workflow %d: %w", workflowID, err)
	}
	return p, nil
}

// parentRefPattern matches {parent.X.Y.Z} references. The leading "parent."
// scoping keeps the resolver from accidentally touching other interpolation
// the runner uses elsewhere.
var parentRefPattern = regexp.MustCompile(`\{parent\.([a-zA-Z0-9_.]+)\}`)

// resolveParentRefs substitutes every {parent.X} in `expr` with its
// resolved value. Returns the first resolution error (so the operator
// sees the precise typo / missing key) — does NOT silently drop bad refs.
func resolveParentRefs(expr string, p *parentInfo) (string, error) {
	var resolveErr error
	out := parentRefPattern.ReplaceAllStringFunc(expr, func(match string) string {
		if resolveErr != nil {
			return match
		}
		ref := match[len("{parent.") : len(match)-1]
		v, err := resolveParentRef(ref, p)
		if err != nil {
			resolveErr = err
			return match
		}
		return v
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return out, nil
}

// resolveParentRef looks up one bare reference (no surrounding braces) in
// the parent's DB-resolved state. v1 supports the subset that's trivially
// available from existing columns; references requiring schema additions
// (`publish.<step>`, `workspace_root`, `tag`) return a clear "not in v1"
// error pointing at the workaround.
func resolveParentRef(ref string, p *parentInfo) (string, error) {
	if strings.HasPrefix(ref, "params.") {
		key := strings.TrimPrefix(ref, "params.")
		if !p.ParamValuesJSON.Valid || p.ParamValuesJSON.String == "" {
			return "", fmt.Errorf("parent.params.%s: parent workflow has no recorded params", key)
		}
		var paramsMap map[string]any
		if err := json.Unmarshal([]byte(p.ParamValuesJSON.String), &paramsMap); err != nil {
			return "", fmt.Errorf("parent.params: param_values is not a JSON object: %w", err)
		}
		v, ok := paramsMap[key]
		if !ok {
			return "", fmt.Errorf("parent.params.%s: key not present in parent's params", key)
		}
		return fmt.Sprintf("%v", v), nil
	}
	switch ref {
	case "workflow_id":
		return fmt.Sprintf("%d", p.WorkflowID), nil
	case "workflow_name":
		return p.WorkflowName, nil
	case "run_by":
		if !p.RunBy.Valid {
			return "", fmt.Errorf("parent.run_by: parent workflow has no recorded run_by user")
		}
		return fmt.Sprintf("%d", p.RunBy.Int32), nil
	case "run_by_username":
		if !p.RunByUsername.Valid {
			return "", fmt.Errorf("parent.run_by_username: parent workflow has no recorded run_by user")
		}
		return p.RunByUsername.String, nil
	}
	if strings.HasPrefix(ref, "publish.") || ref == "workspace_root" || ref == "tag" {
		return "", fmt.Errorf(
			"parent.%s is not supported in v1 — pass the URI explicitly via parent.params.X in your chain mapping (planned; see specs/workflow_chain.md)",
			ref)
	}
	return "", fmt.Errorf("unknown parent reference: parent.%s", ref)
}

// lookupTemplateID resolves (name, version) → workflow_template_id. If
// version is nil/empty, picks the most recently uploaded version of that
// name. Matches the behaviour of CLI `--template <name>` without a
// version pin.
func (s *taskQueueServer) lookupTemplateID(ctx context.Context, name string, version *string) (int32, error) {
	if version != nil && *version != "" {
		var id int32
		err := s.db.QueryRowContext(ctx,
			`SELECT workflow_template_id FROM workflow_template WHERE name = $1 AND version = $2`,
			name, *version).Scan(&id)
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("template %q version %q not found", name, *version)
		}
		if err != nil {
			return 0, fmt.Errorf("template lookup: %w", err)
		}
		return id, nil
	}
	var id int32
	err := s.db.QueryRowContext(ctx,
		`SELECT workflow_template_id FROM workflow_template
		  WHERE name = $1 AND NOT hidden
		  ORDER BY uploaded_at DESC LIMIT 1`,
		name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("template %q not found", name)
	}
	if err != nil {
		return 0, fmt.Errorf("template lookup: %w", err)
	}
	return id, nil
}

// markChainEntryTerminal flips the entry to a terminal state (skipped or
// failed), recording an error message when relevant. Idempotent against
// concurrent edits via the status-match in the WHERE clause.
func (s *taskQueueServer) markChainEntryTerminal(
	ctx context.Context,
	entryID, parentWorkflowID int32,
	newStatus, errMsg string,
) {
	var args []any
	q := `UPDATE workflow_chain_entry SET status = $1`
	args = append(args, newStatus)
	if errMsg != "" {
		args = append(args, errMsg)
		q += fmt.Sprintf(", error_message = $%d", len(args))
	}
	args = append(args, entryID)
	q += fmt.Sprintf(" WHERE chain_entry_id = $%d AND status = 'pending'", len(args))
	if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
		log.Printf("⚠️ chain entry %d: failed to mark %s: %v", entryID, newStatus, err)
		return
	}
	emitChainEntryEvent(entryID, parentWorkflowID, newStatus)
}

// fireChainEntry is the per-entry firing primitive. See file header for
// when it's called. Safe to call from a goroutine — own ctx with timeout.
//
// The first parameter (parentCtx) is informational only — used for nothing
// but log lineage. Internal work uses a fresh background context with
// timeout so a slow firing doesn't pin a request's lifecycle.
func (s *taskQueueServer) fireChainEntry(_ context.Context, entryID int32, parentStatus string) {
	ctx, cancel := context.WithTimeout(context.Background(), firingTimeout)
	defer cancel()

	// Load entry, gated on status to keep the firing idempotent against
	// concurrent operator actions (cancel, suspend, edit).
	var (
		parentWorkflowID int32
		templateName     string
		templateVersion  sql.NullString
		paramsJSON       string
		whenExpr         string
		onStatus         string
		alwaysNew        bool
		entryStatus      string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT parent_workflow_id, template_name, template_version,
		       params_template::text, when_expr, on_status, always_new, status
		  FROM workflow_chain_entry
		 WHERE chain_entry_id = $1
	`, entryID).Scan(
		&parentWorkflowID, &templateName, &templateVersion,
		&paramsJSON, &whenExpr, &onStatus, &alwaysNew, &entryStatus,
	)
	if err != nil {
		log.Printf("⚠️ chain entry %d: load failed: %v", entryID, err)
		return
	}

	// Only fire `pending` (first time) or `fired` (re-fire on parent
	// re-extend). Anything else means the operator has intervened — leave
	// it alone.
	isRefire := entryStatus == "fired"
	if entryStatus != "pending" && !isRefire {
		log.Printf("⏭ chain entry %d: status=%s, skipping fire", entryID, entryStatus)
		return
	}

	// on_status filter: parent's actual terminal status vs the entry's
	// configured trigger.
	if !chainOnStatusMatches(onStatus, parentStatus) {
		if !isRefire {
			s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "skipped",
				fmt.Sprintf("on_status=%s does not match parent terminal status=%s", onStatus, parentStatus))
		}
		return
	}

	// Resolve parent.* references in when_expr + params_template.
	parent, err := s.loadParentInfo(ctx, parentWorkflowID)
	if err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
			fmt.Sprintf("load parent info: %v", err))
		return
	}

	resolvedWhen, err := resolveParentRefs(whenExpr, parent)
	if err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
			fmt.Sprintf("resolve when_expr %q: %v", whenExpr, err))
		return
	}
	if !isChainTruthy(resolvedWhen) {
		if !isRefire {
			s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "skipped",
				fmt.Sprintf("when_expr %q resolved to %q (falsy)", whenExpr, resolvedWhen))
		}
		return
	}

	// Resolve params_template values. Each top-level value is a string
	// (potentially with {parent.X} refs) or a non-string passed through
	// untouched.
	var paramsMap map[string]any
	if err := json.Unmarshal([]byte(paramsJSON), &paramsMap); err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
			fmt.Sprintf("parse params_template: %v", err))
		return
	}
	resolvedParams := make(map[string]any, len(paramsMap))
	for k, v := range paramsMap {
		if sv, ok := v.(string); ok {
			r, err := resolveParentRefs(sv, parent)
			if err != nil {
				s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
					fmt.Sprintf("resolve params[%q]: %v", k, err))
				return
			}
			resolvedParams[k] = r
		} else {
			resolvedParams[k] = v
		}
	}
	resolvedParamsJSON, err := json.Marshal(resolvedParams)
	if err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
			fmt.Sprintf("marshal resolved params: %v", err))
		return
	}

	// Look up the target template.
	var versionPtr *string
	if templateVersion.Valid {
		v := templateVersion.String
		versionPtr = &v
	}
	templateID, err := s.lookupTemplateID(ctx, templateName, versionPtr)
	if err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed", err.Error())
		return
	}

	// Submit the child via RunTemplate, impersonating the parent's run_by
	// so the child carries the right ownership. A short-lived session token
	// is also injected via the context fallback so RunTemplate's
	// scriptRunner subprocess (which runs the venv'd Python child) can
	// authenticate back to the server as that user.
	childCtx := ctx
	if parent.RunBy.Valid {
		username := ""
		if parent.RunByUsername.Valid {
			username = parent.RunByUsername.String
		}
		childCtx = context.WithValue(ctx, userContextKey, &AuthenticatedUser{
			UserID:   int(parent.RunBy.Int32),
			Username: username,
			IsAdmin:  false,
		})
		sessionToken, sessErr := issueInternalSession(ctx, s.db, parent.RunBy.Int32, firingTimeout+1*time.Minute)
		if sessErr != nil {
			s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
				fmt.Sprintf("issue internal session: %v", sessErr))
			return
		}
		childCtx = context.WithValue(childCtx, tokenContextKey, sessionToken)
		defer func() {
			// Cleanup outside the firing ctx — best-effort, never blocks.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = s.db.ExecContext(cleanupCtx,
				`DELETE FROM scitq_user_session WHERE session_id = $1`, sessionToken)
		}()
	}

	// First fire → plain RunTemplate (no continue/extend). Re-fire of an
	// existing entry → extend the recorded child workflow directly, unless
	// always_new is set. Using the stored child_workflow_id avoids the
	// continue_last canonical-param search and gives deterministic
	// idempotency tied to the chain entry's own lineage.
	runReq := &pb.RunTemplateRequest{
		WorkflowTemplateId: templateID,
		ParamValuesJson:    string(resolvedParamsJSON),
	}
	if isRefire && !alwaysNew {
		var existingChild sql.NullInt32
		_ = s.db.QueryRowContext(ctx,
			`SELECT child_workflow_id FROM workflow_chain_entry WHERE chain_entry_id = $1`,
			entryID).Scan(&existingChild)
		if existingChild.Valid {
			runReq.ExtendWorkflowId = &existingChild.Int32
		}
	}
	result, err := s.RunTemplate(childCtx, runReq)
	if err != nil {
		s.markChainEntryTerminal(ctx, entryID, parentWorkflowID, "failed",
			fmt.Sprintf("RunTemplate(%s): %v", templateName, err))
		return
	}

	var childWorkflowID sql.NullInt32
	if result != nil && result.WorkflowId != nil {
		childWorkflowID = sql.NullInt32{Int32: *result.WorkflowId, Valid: true}
	}

	// Promote the entry. For first fire, set status=fired + fired_at +
	// last_fired_at + child_workflow_id. For re-fires (parent extended
	// and succeeded again), keep status=fired, just bump last_fired_at
	// and fill child_workflow_id if it was somehow blank.
	if isRefire {
		_, err = s.db.ExecContext(ctx, `
			UPDATE workflow_chain_entry
			   SET last_fired_at = NOW(),
			       child_workflow_id = COALESCE(child_workflow_id, $2)
			 WHERE chain_entry_id = $1
		`, entryID, childWorkflowID)
	} else {
		_, err = s.db.ExecContext(ctx, `
			UPDATE workflow_chain_entry
			   SET status = 'fired',
			       child_workflow_id = $2,
			       fired_at = NOW(),
			       last_fired_at = NOW()
			 WHERE chain_entry_id = $1 AND status = 'pending'
		`, entryID, childWorkflowID)
	}
	if err != nil {
		log.Printf("⚠️ chain entry %d: child workflow=%v submitted but entry-row update failed: %v",
			entryID, childWorkflowID.Int32, err)
		return
	}

	// Backward lineage on the child: only set if not already populated.
	// (`continue` semantics may reuse an existing child workflow that
	// already has a parent — don't overwrite.)
	if childWorkflowID.Valid {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE workflow
			   SET parent_workflow_id = $1
			 WHERE workflow_id = $2 AND parent_workflow_id IS NULL
		`, parentWorkflowID, childWorkflowID.Int32); err != nil {
			log.Printf("⚠️ chain entry %d: failed to set child.parent_workflow_id: %v", entryID, err)
		}
		if parent.TemplateRunID.Valid && result.TemplateRunId != 0 {
			if _, err := s.db.ExecContext(ctx, `
				UPDATE template_run
				   SET parent_template_run_id = $1
				 WHERE template_run_id = $2 AND parent_template_run_id IS NULL
			`, parent.TemplateRunID.Int32, result.TemplateRunId); err != nil {
				log.Printf("⚠️ chain entry %d: failed to set child.parent_template_run_id: %v", entryID, err)
			}
		}
	}

	emitChainEntryEvent(entryID, parentWorkflowID, "fired")
	log.Printf("🔗 chain entry %d fired: template=%s → workflow=%v (re-fire=%v)",
		entryID, templateName, childWorkflowID.Int32, isRefire)
}

// evaluateChainEntriesForWorkflow is called from the workflow-completion
// hook (recomputeWorkflowStatus) when a workflow transitions to a terminal
// status. Selects all entries that may need firing (`pending` for first
// fire, `fired` for re-fires on parent extension) and fires each.
//
// Run in a goroutine — does not block the recompute path.
func (s *taskQueueServer) evaluateChainEntriesForWorkflow(workflowID int32, terminalStatus string) {
	ctx, cancel := context.WithTimeout(context.Background(), firingTimeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT chain_entry_id
		  FROM workflow_chain_entry
		 WHERE parent_workflow_id = $1
		   AND status IN ('pending', 'fired')
		 ORDER BY idx
	`, workflowID)
	if err != nil {
		log.Printf("⚠️ chain eval: workflow %d: query failed: %v", workflowID, err)
		return
	}
	var entryIDs []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err == nil {
			entryIDs = append(entryIDs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("⚠️ chain eval: workflow %d: iterate failed: %v", workflowID, err)
	}

	if len(entryIDs) == 0 {
		return
	}
	log.Printf("🔗 chain eval: workflow %d → %s, evaluating %d entr(ies)", workflowID, terminalStatus, len(entryIDs))
	for _, id := range entryIDs {
		s.fireChainEntry(ctx, id, terminalStatus)
	}
}
