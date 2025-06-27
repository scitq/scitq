package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"golang.org/x/sys/unix"
)

func validateScriptConfig(root, interpreter string) error {
	// Ensure script_root exists
	fi, err := os.Stat(root)
	if os.IsNotExist(err) {
		log.Printf("⚠️ script_root %q does not exist, attempting to create it", root)
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("failed to create script_root %q: %w", root, err)
		}
	} else if err != nil {
		return fmt.Errorf("could not stat script_root %q: %w", root, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("script_root %q exists but is not a directory", root)
	}

	// Check writability
	testFile := filepath.Join(root, ".scitq_tmp_write_check")
	if err := os.WriteFile(testFile, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("script_root %q is not writable: %w", root, err)
	}
	_ = os.Remove(testFile) // clean up

	// Check interpreter exists
	if _, err := os.Stat(interpreter); err != nil {
		return fmt.Errorf("script_interpreter %q not found: %w", interpreter, err)
	}

	// Optionally check executable bit (UNIX only)
	if err := unix.Access(interpreter, unix.X_OK); err != nil {
		return fmt.Errorf("script_interpreter %q is not executable: %w", interpreter, err)
	}

	return nil
}

func (s *taskQueueServer) UpdateTemplateRun(ctx context.Context, req *pb.UpdateTemplateRunRequest) (*pb.Ack, error) {
	if req.TemplateRunId == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("template_run_id is required")
	}

	query := "UPDATE template_run SET"
	args := []any{}
	sets := []string{}
	i := 1

	if req.WorkflowId != nil {
		sets = append(sets, fmt.Sprintf("workflow_id = $%d", i))
		args = append(args, *req.WorkflowId)
		i++
	}
	if req.ErrorMessage != nil {
		sets = append(sets, fmt.Sprintf("error_message = $%d", i))
		args = append(args, *req.ErrorMessage)
		i++
	}

	if len(sets) == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("no fields to update")
	}

	query += " " + strings.Join(sets, ", ") + fmt.Sprintf(" WHERE template_run_id = $%d", i)
	args = append(args, req.TemplateRunId)

	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update template_run: %w", err)
	}

	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) scriptRunner(
	ctx context.Context,
	scriptPath string,
	mode string, // "metadata", "params", or "run"
	paramJSON string, // only used for "run"
	templateRunID uint32, // only used for "run"
	authToken string, // extracted from context
) (stdout string, stderr string, exitCode int, err error) {
	var args []string
	switch mode {
	case "metadata":
		args = []string{"--metadata"}
	case "params":
		args = []string{"--params"}
	case "run":
		args = []string{"--values", paramJSON}
	default:
		return "", "", -1, fmt.Errorf("unknown mode: %q", mode)
	}

	cmd := exec.CommandContext(ctx, s.cfg.Scitq.ScriptInterpreter, append([]string{scriptPath}, args...)...)

	// 🌍 Inject environment variables
	env := []string{
		fmt.Sprintf("SCITQ_SERVER=127.0.0.1:%d", s.cfg.Scitq.Port),
		fmt.Sprintf("SCITQ_TOKEN=%s", authToken),
	}
	if mode == "run" && templateRunID != 0 {
		env = append(env, fmt.Sprintf("SCITQ_TEMPLATE_RUN_ID=%d", templateRunID))
	}
	if len(s.sslCertificatePEM) > 0 {
		env = append(env, fmt.Sprintf("SCITQ_SSL_CERTIFICATE='%s'", s.sslCertificatePEM))
	}
	cmd.Env = append(os.Environ(), env...)

	// 🔒 Drop privileges if configured
	if userName := s.cfg.Scitq.ScriptRunnerUser; userName != "" {
		u, err := user.Lookup(userName)
		if err != nil {
			return "", "", -1, fmt.Errorf("failed to look up user %q: %w", userName, err)
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return "", "", -1, fmt.Errorf("invalid UID for user %q: %w", userName, err)
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return "", "", -1, fmt.Errorf("invalid GID for user %q: %w", userName, err)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(gid),
			},
		}
	}

	// 🖨️ Capture output
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, fmt.Errorf("scriptRunner: command failed to run: %w", err)
		}
	} else {
		exitCode = 0
	}

	return outBuf.String(), errBuf.String(), exitCode, nil
}

func (s *taskQueueServer) RunTemplate(ctx context.Context, req *pb.RunTemplateRequest) (*pb.TemplateRun, error) {
	if req.WorkflowTemplateId == 0 {
		return nil, fmt.Errorf("workflow_template_id is required")
	}
	if req.ParamValuesJson == "" {
		return nil, fmt.Errorf("param_values_json is required")
	}

	var templateRunId uint32
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx,
		`INSERT INTO template_run (workflow_template_id, param_values, created_at)
		 VALUES ($1, $2, NOW()) RETURNING template_run_id, created_at`,
		req.WorkflowTemplateId, req.ParamValuesJson,
	).Scan(&templateRunId, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert template_run: %w", err)
	}

	// 🏃 Actually run the script
	authToken := extractTokenFromContext(ctx)
	_, stderr, exitCode, runErr := s.scriptRunner(
		ctx,
		filepath.Join(s.cfg.Scitq.ScriptRoot, fmt.Sprintf("%d.py", req.WorkflowTemplateId)),
		"run",               // mode
		req.ParamValuesJson, // paramJSON
		templateRunId,       // templateRunID
		authToken,
	)

	if runErr != nil || exitCode != 0 {
		errMsg := fmt.Sprintf("script failed (exit=%d): %s", exitCode, stderr)
		log.Printf("⚠️ RunTemplate script error: %s", errMsg)

		_, _ = s.db.ExecContext(ctx, `
			UPDATE template_run SET error_message = $1 WHERE template_run_id = $2
		`, errMsg, templateRunId)

		return &pb.TemplateRun{
			TemplateRunId:      templateRunId,
			WorkflowTemplateId: req.WorkflowTemplateId,
			CreatedAt:          createdAt.Format(time.RFC3339),
			ParamValuesJson:    req.ParamValuesJson,
			ErrorMessage:       proto.String(errMsg),
		}, nil
	}

	// ✅ Script ran successfully (but workflow_id will be updated later by Python)
	return &pb.TemplateRun{
		TemplateRunId:      templateRunId,
		WorkflowTemplateId: req.WorkflowTemplateId,
		CreatedAt:          createdAt.Format(time.RFC3339),
		ParamValuesJson:    req.ParamValuesJson,
	}, nil
}

type ParamSpec struct {
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
	Choices     []string `json:"choices,omitempty"`
	Format      string   `json:"format,omitempty"`
}

func (s *taskQueueServer) UploadTemplate(ctx context.Context, req *pb.UploadTemplateRequest) (*pb.UploadTemplateResponse, error) {
	// 1️⃣ Write script to temp file
	tempScript, err := os.CreateTemp("", "scitq_template_*.py")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp script file: %w", err)
	}
	defer os.Remove(tempScript.Name())

	if _, err := tempScript.Write(req.Script); err != nil {
		return nil, fmt.Errorf("failed to write script content: %w", err)
	}
	tempScript.Close()

	// 2️⃣ Run with --metadata
	authToken := extractTokenFromContext(ctx)
	stdoutMeta, stderrMeta, exitCodeMeta, err := s.scriptRunner(
		ctx,
		tempScript.Name(),
		"metadata", // mode
		"",         // paramJSON not used for metadata
		0,          // script ID not used when path is explicit
		authToken,
	)
	if err != nil || exitCodeMeta != 0 {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  fmt.Sprintf("script metadata execution failed: %v", err),
			Warnings: stderrMeta,
		}, nil
	}

	// 3️⃣ Parse metadata JSON
	var meta struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(stdoutMeta), &meta); err != nil {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  fmt.Sprintf("invalid JSON output from --metadata: %v", err),
			Warnings: stderrMeta,
		}, nil
	}

	if meta.Name == "" || meta.Version == "" {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  "metadata must include 'name' and 'version'",
			Warnings: stderrMeta,
		}, nil
	}

	// 4️⃣ Check if name/version exists
	var existingID uint32
	err = s.db.QueryRowContext(ctx,
		`SELECT workflow_template_id FROM workflow_template WHERE name = $1 AND version = $2`,
		meta.Name, meta.Version,
	).Scan(&existingID)

	if err == nil && !req.Force {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  fmt.Sprintf("template %q version %q already exists", meta.Name, meta.Version),
			Warnings: stderrMeta,
		}, nil
	}
	if err != sql.ErrNoRows && err != nil {
		return nil, fmt.Errorf("failed to check existing template: %w", err)
	}

	// 5️⃣ Run with --params
	stdoutParams, stderrParams, exitCodeParams, err := s.scriptRunner(
		ctx,
		tempScript.Name(),
		"params", // mode
		"",       // paramJSON not used for params
		0,        // script ID not used when path is explicit
		authToken,
	)
	if err != nil || exitCodeParams != 0 {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  fmt.Sprintf("script params execution failed: %v", err),
			Warnings: stderrParams,
		}, nil
	}

	var paramMap map[string]ParamSpec
	if err := json.Unmarshal([]byte(stdoutParams), &paramMap); err != nil {
		return &pb.UploadTemplateResponse{
			Success:  false,
			Message:  fmt.Sprintf("invalid JSON output from --params: %v", err),
			Warnings: stderrParams,
		}, nil
	}

	// Strict validation
	for name, param := range paramMap {
		if param.Type == "" {
			return &pb.UploadTemplateResponse{
				Success:  false,
				Message:  fmt.Sprintf("invalid param %q: missing 'type'", name),
				Warnings: stderrParams,
			}, nil
		}

		// Must be 'string', 'enum', 'int', 'float', etc. (accept 'list'?)
		switch param.Type {
		case "string", "enum", "int", "float", "list":
			// OK
		default:
			return &pb.UploadTemplateResponse{
				Success:  false,
				Message:  fmt.Sprintf("param %q has unknown type %q", name, param.Type),
				Warnings: stderrParams,
			}, nil
		}

		if param.Type != "enum" && len(param.Choices) > 0 {
			return &pb.UploadTemplateResponse{
				Success:  false,
				Message:  fmt.Sprintf("param %q: 'choices' only allowed with type 'enum'", name),
				Warnings: stderrParams,
			}, nil
		}
		if param.Type != "string" && param.Format != "" {
			return &pb.UploadTemplateResponse{
				Success:  false,
				Message:  fmt.Sprintf("param %q: 'format' only allowed with type 'string'", name),
				Warnings: stderrParams,
			}, nil
		}
	}

	// 6️⃣ Insert or replace into workflow_template
	var templateID uint32
	if req.Force && existingID != 0 {
		_, err := s.db.ExecContext(ctx,
			`UPDATE workflow_template 
			 SET description = $1, script_path = $2, params_schema = $3, uploaded_at = NOW()
			 WHERE workflow_template_id = $4`,
			meta.Description, "", stdoutParams, existingID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to update existing template: %w", err)
		}
		templateID = existingID
	} else {
		err = s.db.QueryRowContext(ctx,
			`INSERT INTO workflow_template (name, version, description, script_path, params_schema, uploaded_at)
			 VALUES ($1, $2, $3, '', $4, NOW())
			 RETURNING workflow_template_id`,
			meta.Name, meta.Version, meta.Description, stdoutParams,
		).Scan(&templateID)
		if err != nil {
			return nil, fmt.Errorf("failed to insert new template: %w", err)
		}
	}

	// 7️⃣ Move file to final location
	finalPath := filepath.Join(s.cfg.Scitq.ScriptRoot, fmt.Sprintf("%d.py", templateID))
	if err := os.MkdirAll(s.cfg.Scitq.ScriptRoot, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create script_root dir: %w", err)
	}
	if err := os.Rename(tempScript.Name(), finalPath); err != nil {
		return nil, fmt.Errorf("failed to move script to final location: %w", err)
	}

	// 8️⃣ Update script_path
	_, err = s.db.ExecContext(ctx,
		`UPDATE workflow_template SET script_path = $1 WHERE workflow_template_id = $2`,
		finalPath, templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update script path: %w", err)
	}

	if stderrMeta != "" || stderrParams != "" {
		log.Printf("📎 Template upload warnings:\n%s\n%s", stderrMeta, stderrParams)
	}

	return &pb.UploadTemplateResponse{
		Success:            true,
		Message:            "template uploaded successfully",
		WorkflowTemplateId: templateID,
		Name:               meta.Name,
		Version:            meta.Version,
		Description:        meta.Description,
		Warnings:           strings.TrimSpace(stderrMeta + "\n" + stderrParams),
	}, nil
}

func (s *taskQueueServer) ListTemplates(ctx context.Context, _ *emptypb.Empty) (*pb.TemplateList, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workflow_template_id, name, version, description, uploaded_at, uploaded_by
		FROM workflow_template
		ORDER BY uploaded_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query templates: %w", err)
	}
	defer rows.Close()

	var templates []*pb.Template

	for rows.Next() {
		var t pb.Template
		var uploadedBy sql.NullInt32
		var uploadedAt time.Time

		if err := rows.Scan(&t.WorkflowTemplateId, &t.Name, &t.Version, &t.Description, &uploadedAt, &uploadedBy); err != nil {
			return nil, fmt.Errorf("failed to scan template: %w", err)
		}

		t.UploadedAt = uploadedAt.Format(time.RFC3339)
		if uploadedBy.Valid {
			t.UploadedBy = proto.Uint32(uint32(uploadedBy.Int32))
		}
		templates = append(templates, &t)
	}

	return &pb.TemplateList{Templates: templates}, nil
}

func (s *taskQueueServer) ListTemplateRuns(ctx context.Context, req *pb.TemplateRunFilter) (*pb.TemplateRunList, error) {
	query := `
		SELECT template_run_id, workflow_template_id, workflow_id, created_at, param_values, error_message
		FROM template_run
	`
	args := []interface{}{}
	if req.WorkflowTemplateId != nil {
		query += " WHERE workflow_template_id = $1"
		args = append(args, *req.WorkflowTemplateId)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query template runs: %w", err)
	}
	defer rows.Close()

	var runs []*pb.TemplateRun

	for rows.Next() {
		var r pb.TemplateRun
		var workflowID sql.NullInt32
		var errorMsg sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&r.TemplateRunId, &r.WorkflowTemplateId, &workflowID, &createdAt, &r.ParamValuesJson, &errorMsg); err != nil {
			return nil, fmt.Errorf("failed to scan template run: %w", err)
		}

		r.CreatedAt = createdAt.Format(time.RFC3339)
		if workflowID.Valid {
			r.WorkflowId = proto.Uint32(uint32(workflowID.Int32))
		}
		if errorMsg.Valid {
			r.ErrorMessage = proto.String(errorMsg.String)
		}
		runs = append(runs, &r)
	}

	return &pb.TemplateRunList{Runs: runs}, nil
}
