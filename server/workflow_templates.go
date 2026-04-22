package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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

	ws "github.com/scitq/scitq/server/websocket"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/config"
	"google.golang.org/protobuf/proto"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

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
	if req.ModulePins != nil {
		sets = append(sets, fmt.Sprintf("module_pins = $%d::jsonb", i))
		args = append(args, *req.ModulePins)
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
	templateRunID int32, // only used for "run"
	authToken string, // extracted from context
) (stdout string, stderr string, exitCode int, err error) {
	// Extract --no-recruiters suffix if present
	noRecruiters := strings.HasSuffix(mode, "_no_recruiters")
	if noRecruiters {
		mode = strings.TrimSuffix(mode, "_no_recruiters")
	}

	var args []string
	switch mode {
	case "metadata":
		args = []string{scriptPath, "--metadata"}
	case "params":
		args = []string{scriptPath, "--params"}
	case "run":
		args = []string{scriptPath, "--values", paramJSON}
		if noRecruiters {
			args = append(args, "--no-recruiters")
		}
	case "yaml_params":
		args = []string{"-m", "scitq2.yaml_runner", scriptPath, "--params"}
	case "yaml_run":
		args = []string{"-m", "scitq2.yaml_runner", scriptPath, "--values", paramJSON}
		if noRecruiters {
			args = append(args, "--no-recruiters")
		}
	default:
		return "", "", -1, fmt.Errorf("unknown mode: %q", mode)
	}

	venvPython := filepath.Join(s.cfg.Scitq.ScriptVenv, "bin", "python")
	cmd := exec.CommandContext(ctx, venvPython, args...)

	// 🌍 Inject environment variables
	serverName := s.cfg.Scitq.ServerFQDN
	if serverName == "" {
		serverName = "localhost"
	}
	env := []string{
		fmt.Sprintf("SCITQ_SERVER=%s:%d", serverName, s.cfg.Scitq.Port),
		fmt.Sprintf("SCITQ_TOKEN=%s", authToken),
		fmt.Sprintf("SCITQ_SCRIPT_ROOT=%s", s.cfg.Scitq.ScriptRoot),
	}
	if templateRunID != 0 {
		env = append(env, fmt.Sprintf("SCITQ_TEMPLATE_RUN_ID=%d", templateRunID))
	}
	if len(s.sslCertificatePEM) > 0 {
		env = append(env, fmt.Sprintf("SCITQ_SSL_CERTIFICATE=%s", s.sslCertificatePEM))
	}
	cmd.Env = append(os.Environ(), env...)

	// 🔒 Drop privileges if configured
	if userName := s.cfg.Scitq.ScriptRunnerUser; userName != "" {
		currentUser, err := user.Current()
		if err != nil {
			return "", "", -1, fmt.Errorf("cannot get current user: %w", err)
		}

		if currentUser.Username != userName {
			if os.Geteuid() != 0 {
				log.Printf("⚠️ script_runner_user is set to a different user as the current running user and server is not run as root")
				return "", "", -1, fmt.Errorf("cannot change to user %q because the server is not run as root", userName)
			}
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

			// Build supplementary groups (at least 'docker')
			var supGroups []uint32
			if grp, err := user.LookupGroup("docker"); err == nil {
				if dockerGID, err := strconv.Atoi(grp.Gid); err == nil {
					supGroups = append(supGroups, uint32(dockerGID))
				} else {
					log.Printf("⚠️ cannot parse docker group GID: %v", err)
				}
			} else {
				log.Printf("⚠️ cannot find 'docker' group: %v", err)
			}

			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{
					Uid:    uint32(uid),
					Gid:    uint32(gid),
					Groups: supGroups,
				},
			}
		}
	}

	//// 🧪 Sanity check: can we exec the Python interpreter at all?
	//checkCmd := exec.Command(s.cfg.Scitq.ScriptInterpreter, "-c", "print('SCITQ_PYTHON_OK')")
	//checkCmd.Env = cmd.Env // Use same environment
	//checkCmd.SysProcAttr = cmd.SysProcAttr
	//checkOut, checkErr := checkCmd.CombinedOutput()
	//if checkErr != nil {
	//	return "", "", -1, fmt.Errorf("sanity check failed: cannot exec Python interpreter (%s): %v\nOutput: %s", s.cfg.Scitq.ScriptInterpreter, checkErr, string(checkOut))
	//}
	//log.Printf("Sanity check passes: %s", checkOut)

	if err := os.Chmod(scriptPath, 0o755); err != nil {
		log.Printf("⚠️ Failed to chmod +x on temp script: %v", err)
	}

	// 🖨️ Capture output
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	log.Printf("scriptRunner: launching %s with args %v as %s", cmd.Path, cmd.Args, s.cfg.Scitq.ScriptRunnerUser)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.Printf("scriptRunner: killed by context: %v", err)
			} else {
				log.Printf("scriptRunner: non-ExitError (%T): %v", err, err)
			}
			return "", "", -1, fmt.Errorf("scriptRunner: command failed to run: %w", err)
		}
	} else {
		exitCode = 0
	}

	return outBuf.String(), errBuf.String(), exitCode, nil
}

func transformParamSchema(rawJSON string, cfg config.Config) (string, error) {
	type Param struct {
		Name     string      `json:"name"`
		Type     string      `json:"type"`
		Required bool        `json:"required"`
		Default  interface{} `json:"default"`
		Choices  interface{} `json:"choices"` // can be null or []string
		Help     string      `json:"help"`
	}

	var params []Param
	if err := json.Unmarshal([]byte(rawJSON), &params); err != nil {
		return "", fmt.Errorf("failed to parse param schema: %w", err)
	}

	// Collect available provider:region strings
	var providerRegions []string
	for _, p := range cfg.GetProviders() {
		for _, region := range p.GetRegions() {
			providerRegions = append(providerRegions, fmt.Sprintf("%s:%s", p.GetName(), region))
		}
	}

	// Apply transformation
	for i, p := range params {
		if strings.EqualFold(p.Type, "provider_region") {
			params[i].Type = "str"
			params[i].Choices = providerRegions
		}
	}

	transformed, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode transformed param schema: %w", err)
	}
	return string(transformed), nil
}

// RegisterAdhocRun creates a template_run row for a local Python DSL run —
// i.e. the client invoked `python my_script.py` directly, without uploading
// a template first. workflow_template_id stays NULL; script_name and
// script_sha256 record the identity of the launching script, and
// module_pins_json captures the Python-side versioning knobs (scitq2 version,
// etc.). The returned template_run_id is what the client passes to
// UpdateTemplateRun once it has created the workflow — same flow as the
// server-launched templates.
func (s *taskQueueServer) RegisterAdhocRun(ctx context.Context, req *pb.RegisterAdhocRunRequest) (*pb.TemplateRun, error) {
	if req.ScriptName == "" {
		return nil, fmt.Errorf("script_name is required")
	}
	if req.ScriptSha256 == "" {
		return nil, fmt.Errorf("script_sha256 is required")
	}
	paramValues := req.ParamValuesJson
	if paramValues == "" {
		paramValues = "{}"
	}

	user := GetUserFromContext(ctx)
	var userId *uint32
	if user != nil {
		uid := uint32(user.UserID)
		userId = &uid
	}

	var modulePinsJSON sql.NullString
	if req.ModulePinsJson != "" {
		modulePinsJSON = sql.NullString{String: req.ModulePinsJson, Valid: true}
	}

	var templateRunID int32
	var createdAt time.Time
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO template_run
		   (workflow_template_id, param_values, run_by, created_at,
		    script_name, script_sha256, module_pins)
		 VALUES (NULL, $1, $2, NOW(), $3, $4, $5)
		 RETURNING template_run_id, created_at`,
		paramValues, userId, req.ScriptName, req.ScriptSha256, modulePinsJSON,
	).Scan(&templateRunID, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert ad-hoc template_run: %w", err)
	}

	return &pb.TemplateRun{
		TemplateRunId:   templateRunID,
		Status:          "P",
		CreatedAt:       createdAt.Format(time.RFC3339),
		ParamValuesJson: paramValues,
	}, nil
}

func (s *taskQueueServer) RunTemplate(ctx context.Context, req *pb.RunTemplateRequest) (*pb.TemplateRun, error) {
	// Wait for Python DSL environment to be ready (bootstrapped async at startup)
	if s.pythonReady != nil {
		<-s.pythonReady
	}

	if req.WorkflowTemplateId == 0 {
		return nil, fmt.Errorf("workflow_template_id is required")
	}
	if req.ParamValuesJson == "" {
		return nil, fmt.Errorf("param_values_json is required")
	}

	var templateRunId int32
	var createdAt time.Time
	user := GetUserFromContext(ctx)

	var userId *uint32
	if user != nil {
		uid := uint32(user.UserID)
		userId = &uid
	}

	err := s.db.QueryRowContext(ctx,
		`INSERT INTO template_run (workflow_template_id, param_values, run_by, created_at)
		 VALUES ($1, $2, $3, NOW()) RETURNING template_run_id, created_at`,
		req.WorkflowTemplateId, req.ParamValuesJson, userId,
	).Scan(&templateRunId, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert template_run: %w", err)
	}

	// 🏃 Actually run the script
	authToken := extractTokenFromContext(ctx)
	timeout := time.Duration(s.cfg.Scitq.GRPCDSLTimeout) * time.Second
	// Detach the subprocess from the client's RPC context — a client that gives
	// up waiting (e.g. MCP 60s timeout on a large template materialising thousands
	// of tasks) would otherwise kill the script mid-submission and leave a half-
	// built workflow in DB. Parent to the server's shutdown context instead, so
	// the only things that cancel the subprocess are real server shutdown or the
	// configured GRPCDSLTimeout.
	runCtx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	// Detect script type from stored path
	var scriptPath string
	err = s.db.QueryRowContext(ctx,
		`SELECT script_path FROM workflow_template WHERE workflow_template_id = $1`,
		req.WorkflowTemplateId,
	).Scan(&scriptPath)
	if err != nil {
		return nil, fmt.Errorf("template not found: %w", err)
	}
	runMode := "run"
	if strings.HasSuffix(scriptPath, ".yaml") || strings.HasSuffix(scriptPath, ".yml") {
		runMode = "yaml_run"
	}
	if req.NoRecruiters {
		runMode += "_no_recruiters"
	}
	stdout, stderr, exitCode, runErr := s.scriptRunner(
		runCtx,
		scriptPath,
		runMode,
		req.ParamValuesJson,
		templateRunId,
		authToken,
	)

	// Use a non-canceled context for the finalize/cleanup DB writes. If we fail
	// because the client disconnected, `ctx` is already Done — using it would
	// silently no-op the cleanup and leave orphaned partial state behind.
	finalizeCtx, finalizeCancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer finalizeCancel()

	if runErr != nil || exitCode != 0 {
		errMsg := fmt.Sprintf("script failed (exit=%d): \n %s \n %s", exitCode, stdout, stderr)
		log.Printf("⚠️ RunTemplate script error: %s", errMsg)

		_, _ = s.db.ExecContext(finalizeCtx, `
			UPDATE template_run SET error_message = $1, status = 'F' WHERE template_run_id = $2
		`, errMsg, templateRunId)
		_, _ = s.db.ExecContext(finalizeCtx, `
			DELETE FROM workflow WHERE workflow_id = (SELECT workflow_id FROM template_run WHERE template_run_id = $1)
		`, templateRunId)

		return &pb.TemplateRun{
			TemplateRunId:      templateRunId,
			WorkflowTemplateId: req.WorkflowTemplateId,
			CreatedAt:          createdAt.Format(time.RFC3339),
			ParamValuesJson:    req.ParamValuesJson,
			ErrorMessage:       proto.String(errMsg),
			Status:             "F",
		}, nil
	} else {
		var err error
		_, err = s.db.ExecContext(finalizeCtx, `
			UPDATE template_run SET status = 'S' WHERE template_run_id = $1
		`, templateRunId)
		if err != nil {
			log.Printf("⚠️ failed to update template_run status: %v", err)
		}
		var workflowID sql.NullInt32
		err = s.db.QueryRowContext(finalizeCtx, `
			SELECT workflow_id FROM template_run WHERE template_run_id = $1
		`, templateRunId).Scan(&workflowID)

		if err != nil {
			msg := fmt.Sprintf("⚠️ failed to load workflow_id for template_run: %v", err)
			log.Println(msg)
			_, _ = s.db.ExecContext(finalizeCtx, `
				UPDATE template_run SET status = 'F', error_message = $1 WHERE template_run_id = $2
			`, msg, templateRunId)

			return &pb.TemplateRun{
				TemplateRunId:      templateRunId,
				WorkflowTemplateId: req.WorkflowTemplateId,
				CreatedAt:          createdAt.Format(time.RFC3339),
				ParamValuesJson:    req.ParamValuesJson,
				Status:             "F",
				ErrorMessage:       proto.String(msg),
			}, nil
		}

		if !workflowID.Valid {
			msg := "⚠️ workflow_id is NULL — cannot activate tasks"
			log.Println(msg)
			_, _ = s.db.ExecContext(finalizeCtx, `
						UPDATE template_run SET status = 'F', error_message = $1 WHERE template_run_id = $2
					`, msg, templateRunId)

			return &pb.TemplateRun{
				TemplateRunId:      templateRunId,
				WorkflowTemplateId: req.WorkflowTemplateId,
				CreatedAt:          createdAt.Format(time.RFC3339),
				ParamValuesJson:    req.ParamValuesJson,
				Status:             "F",
				ErrorMessage:       proto.String(msg),
			}, nil
		}

		_, err = s.db.ExecContext(finalizeCtx, `
			UPDATE workflow SET status = 'R' WHERE workflow_id = $1
		`, workflowID.Int32)
		if err != nil {
			msg := fmt.Sprintf("⚠️ failed to update workflow status: %v", err)
			log.Println(msg)
			_, _ = s.db.ExecContext(finalizeCtx, `
				UPDATE template_run SET status = 'F', error_message = $1 WHERE template_run_id = $2
			`, msg, templateRunId)
			return &pb.TemplateRun{
				TemplateRunId:      templateRunId,
				WorkflowTemplateId: req.WorkflowTemplateId,
				CreatedAt:          createdAt.Format(time.RFC3339),
				ParamValuesJson:    req.ParamValuesJson,
				Status:             "F",
				ErrorMessage:       proto.String(msg),
			}, nil
		}
		ws.EmitWS("workflow", workflowID.Int32, "status", struct {
			WorkflowId int32  `json:"workflowId"`
			Status     string `json:"status"`
		}{
			WorkflowId: workflowID.Int32,
			Status:     "R",
		})
		s.triggerAssign()
	}

	log.Printf("✅ RunTemplate script completed successfully: %s", stdout)
	if stderr != "" {
		log.Printf("⚠️ Warning: %s", stderr)
	}

	// ✅ Script ran successfully (but workflow_id will be updated later by Python)
	resp := &pb.TemplateRun{
		TemplateRunId:      templateRunId,
		WorkflowTemplateId: req.WorkflowTemplateId,
		CreatedAt:          createdAt.Format(time.RFC3339),
		ParamValuesJson:    req.ParamValuesJson,
		Status:             "S",
	}
	// Only surface stderr when there's actual content — otherwise the CLI
	// prints an empty "Warning:" line on every successful run.
	if strings.TrimSpace(stderr) != "" {
		resp.ErrorMessage = proto.String(stderr)
	}
	return resp, nil
}

type ParamSpec struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
	Help     string   `json:"help"` // Or `json:"description"` if your field is named so
	Choices  []string `json:"choices,omitempty"`
}

// extractYAMLMetadata parses a YAML template and returns JSON metadata
// (name, version, description) without executing any code.
//
// Handles all three common `description:` shapes:
//
//	description: single-line value
//	description: "quoted value"
//	description: >             # folded block — newlines become spaces
//	  first line
//	  second line
//	description: |             # literal block — newlines preserved
//	  line one
//	  line two
func extractYAMLMetadata(content []byte) (string, error) {
	meta := map[string]string{}
	lines := strings.Split(string(content), "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Top-level fields start at column 0 (no indentation, not a comment,
		// not a list item).
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
			continue
		}
		for _, key := range []string{"name", "version", "description"} {
			if !strings.HasPrefix(line, key+":") {
				continue
			}
			val := strings.TrimSpace(strings.TrimPrefix(line, key+":"))

			// Block scalar indicators — consume indented continuation lines
			// until we hit a top-level line or EOF. `>` folds newlines to
			// spaces (except blank lines → newline), `|` keeps newlines.
			if val == ">" || val == "|" || strings.HasPrefix(val, ">-") || strings.HasPrefix(val, "|-") {
				folded := val == ">" || strings.HasPrefix(val, ">-")
				var parts []string
				for j := i + 1; j < len(lines); j++ {
					next := lines[j]
					if len(next) == 0 {
						// Blank line: in folded mode emits a hard newline,
						// in literal mode emits an empty line.
						parts = append(parts, "")
						continue
					}
					if next[0] != ' ' && next[0] != '\t' {
						// Hit a top-level key — block ended.
						break
					}
					parts = append(parts, strings.TrimSpace(next))
				}
				var body string
				if folded {
					// Fold: join non-empty runs with single spaces, preserve
					// blank-line boundaries as newlines.
					var sb strings.Builder
					prevBlank := false
					for idx, p := range parts {
						if p == "" {
							if !prevBlank && sb.Len() > 0 {
								sb.WriteByte('\n')
							}
							prevBlank = true
							continue
						}
						if sb.Len() > 0 && !prevBlank {
							sb.WriteByte(' ')
						}
						sb.WriteString(p)
						prevBlank = false
						_ = idx
					}
					body = sb.String()
				} else {
					body = strings.Join(parts, "\n")
				}
				meta[key] = strings.TrimSpace(body)
				break
			}

			// Regular single-line value.
			meta[key] = strings.Trim(val, `"'`)
			break
		}
	}

	if meta["name"] == "" {
		return "", fmt.Errorf("YAML template missing 'name' field")
	}
	if meta["version"] == "" {
		meta["version"] = "0.0.0"
	}
	jsonBytes, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func (s *taskQueueServer) UploadTemplate(ctx context.Context, req *pb.UploadTemplateRequest) (*pb.UploadTemplateResponse, error) {
	// Detect file type from filename
	isYAML := false
	if req.Filename != nil {
		ext := strings.ToLower(filepath.Ext(*req.Filename))
		isYAML = ext == ".yaml" || ext == ".yml"
	}

	// 1️⃣ Write script to temp file
	pattern := "scitq_template_*.py"
	if isYAML {
		pattern = "scitq_template_*.yaml"
	}
	tempScript, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp script file: %w", err)
	}
	defer os.Remove(tempScript.Name())

	if _, err := tempScript.Write(req.Script); err != nil {
		return nil, fmt.Errorf("failed to write script content: %w", err)
	}
	tempScript.Close()

	// 2️⃣ Extract metadata
	var stdoutMeta, stderrMeta string
	var exitCodeMeta int
	authToken := extractTokenFromContext(ctx)

	if isYAML {
		// YAML: extract metadata directly from the file content
		meta, err := extractYAMLMetadata(req.Script)
		if err != nil {
			return &pb.UploadTemplateResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to parse YAML metadata: %v", err),
			}, nil
		}
		stdoutMeta = meta
	} else {
		// Python: run with --metadata
		stdoutMeta, stderrMeta, exitCodeMeta, err = s.scriptRunner(
			ctx,
			tempScript.Name(),
			"metadata",
			"",
			0,
			authToken,
		)
	}
	if err != nil || exitCodeMeta != 0 {
		return &pb.UploadTemplateResponse{
			Success: false,
			Message: fmt.Sprintf("script metadata execution failed: %v\n%s", err, stderrMeta),
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
			Success: false,
			Message: fmt.Sprintf("invalid JSON output from --metadata: %v\n%s", err, stderrMeta),
		}, nil
	}

	if meta.Name == "" || meta.Version == "" {
		return &pb.UploadTemplateResponse{
			Success: false,
			Message: fmt.Sprintf("metadata must include 'name' and 'version'\n%s", stderrMeta),
		}, nil
	}

	// 4️⃣ Check if name/version exists
	var existingID int32
	err = s.db.QueryRowContext(ctx,
		`SELECT workflow_template_id FROM workflow_template WHERE name = $1 AND version = $2`,
		meta.Name, meta.Version,
	).Scan(&existingID)

	if err == nil && !req.Force {
		return &pb.UploadTemplateResponse{
			Success: false,
			Message: fmt.Sprintf("template %q version %q already exists\n%s", meta.Name, meta.Version, stderrMeta),
		}, nil
	}
	if err != sql.ErrNoRows && err != nil {
		return nil, fmt.Errorf("failed to check existing template: %w", err)
	}

	// 5️⃣ Extract params
	var stdoutParams, stderrParams string
	if isYAML {
		// YAML: use yaml_runner --params
		stdoutParams, stderrParams, exitCodeMeta, err = s.scriptRunner(
			ctx,
			tempScript.Name(),
			"yaml_params",
			"",
			0,
			authToken,
		)
	} else {
		stdoutParams, stderrParams, _, err = s.scriptRunner(
			ctx,
			tempScript.Name(),
			"params",
			"",
			0,
			authToken,
		)
	}
	if err != nil {
		return &pb.UploadTemplateResponse{
			Success: false,
			Message: fmt.Sprintf("params extraction failed: %v\n%s", err, stderrParams),
		}, nil
	}

	var paramList []ParamSpec
	if err := json.Unmarshal([]byte(stdoutParams), &paramList); err != nil {
		return &pb.UploadTemplateResponse{
			Success: false,
			Message: fmt.Sprintf("invalid JSON output from --params: %v\n%s\n%s", err, stdoutMeta, stderrMeta),
		}, nil
	}

	paramMap := make(map[string]ParamSpec)
	for _, p := range paramList {
		if p.Name == "" {
			return &pb.UploadTemplateResponse{
				Success: false,
				Message: fmt.Sprintf("invalid param: missing 'name'\n%s", stderrMeta),
			}, nil
		}
		paramMap[p.Name] = p
	}

	// Strict validation
	for name, param := range paramMap {
		if param.Type == "" {
			return &pb.UploadTemplateResponse{
				Success: false,
				Message: fmt.Sprintf("invalid param %q: missing 'type'\n%s", name, stderrMeta),
			}, nil
		}

		// Must be 'string', 'enum', 'int', 'float', etc. (accept 'list'?) or special type "provider_region"
		switch param.Type {
		case "str", "int", "float", "bool", "provider_region":
			// OK
		default:
			return &pb.UploadTemplateResponse{
				Success: false,
				Message: fmt.Sprintf("param %q has unknown type %q\n%s", name, param.Type, stderrMeta),
			}, nil
		}

	}

	success := true
	if stderrMeta != "" || stderrParams != "" {
		if !req.Force {
			success = false
		}
		log.Printf("📎 Template upload warnings:\n%s\n%s", stderrMeta, stderrParams)
	}

	if !success {
		return &pb.UploadTemplateResponse{
			Success: success,
			Message: strings.TrimSpace(stderrMeta + "\n" + stderrParams),
		}, fmt.Errorf("📎 Template upload warnings and no force:\n%s\n%s", stderrMeta, stderrParams)
	} else {
		// 6️⃣ Insert or replace into workflow_template
		var templateID int32
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
		ext := ".py"
		if isYAML {
			ext = ".yaml"
		}
		finalPath := filepath.Join(s.cfg.Scitq.ScriptRoot, fmt.Sprintf("%d%s", templateID, ext))
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

		processedParams, err := transformParamSchema(stdoutParams, s.cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to transform params: %w", err)
		}

		ws.EmitWS("template", templateID, "uploaded", struct {
			WorkflowTemplateId int32  `json:"workflowTemplateId"`
			Name               string `json:"name"`
			Version            string `json:"version"`
			Description        string `json:"description"`
			ParamJson          string `json:"paramJson"`
			UploadedAt         string `json:"uploadedAt"`
		}{
			WorkflowTemplateId: templateID,
			Name:               meta.Name,
			Version:            meta.Version,
			Description:        meta.Description,
			ParamJson:          processedParams,
			UploadedAt:         time.Now().Format(time.RFC3339),
		})

		return &pb.UploadTemplateResponse{
			Success:            success,
			Message:            strings.TrimSpace(stderrMeta + "\n" + stderrParams),
			WorkflowTemplateId: &templateID,
			Name:               &meta.Name,
			Version:            &meta.Version,
			Description:        &meta.Description,
			ParamJson:          &processedParams,
		}, nil
	}

}

func (s *taskQueueServer) ListTemplates(ctx context.Context, req *pb.TemplateFilter) (*pb.TemplateList, error) {
	var args []any
	var clauses []string

	query := `
		SELECT workflow_template_id, name, version, description, params_schema, uploaded_at, uploaded_by
		FROM workflow_template
	`

	// Handle exact ID
	if req.WorkflowTemplateId != nil {
		clauses = append(clauses, "workflow_template_id = $1")
		args = append(args, *req.WorkflowTemplateId)
	} else {
		if req.Name != nil {
			clauses = append(clauses, fmt.Sprintf("name LIKE $%d::TEXT", len(args)+1))
			args = append(args, *req.Name)
		}
		if req.Version != nil && *req.Version != "latest" {
			clauses = append(clauses, fmt.Sprintf("version LIKE $%d::TEXT", len(args)+1))
			args = append(args, *req.Version)
		}
	}

	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}

	query += " ORDER BY uploaded_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query templates: %w", err)
	}
	defer rows.Close()

	var templates []*pb.Template
	for rows.Next() {
		var t pb.Template
		var uploadedBy sql.NullInt32
		var uploadedAt time.Time
		var rawParams sql.NullString

		if err := rows.Scan(&t.WorkflowTemplateId, &t.Name, &t.Version, &t.Description, &rawParams, &uploadedAt, &uploadedBy); err != nil {
			return nil, fmt.Errorf("failed to scan template: %w", err)
		}

		t.UploadedAt = uploadedAt.Format(time.RFC3339)
		if uploadedBy.Valid {
			t.UploadedBy = proto.Int32(uploadedBy.Int32)
		}
		if rawParams.Valid {
			t.ParamJson, err = transformParamSchema(rawParams.String, s.cfg)
			if err != nil {
				return nil, fmt.Errorf("template %s (id: %d) params could not be transformed %s -> %v",
					t.Name, t.WorkflowTemplateId, rawParams.String, err)
			}
		}
		templates = append(templates, &t)
	}

	// Handle `version = "latest"` logic (after sorting)
	if req.Version != nil && *req.Version == "latest" {
		latest := make(map[string]*pb.Template) // name → latest version
		for _, t := range templates {
			if existing, ok := latest[t.Name]; !ok {
				latest[t.Name] = t
			} else {
				if t.UploadedAt > existing.UploadedAt {
					latest[t.Name] = t
				}
			}
		}
		var latestList []*pb.Template
		for _, t := range latest {
			latestList = append(latestList, t)
		}
		templates = latestList
	}

	return &pb.TemplateList{Templates: templates}, nil
}

func (s *taskQueueServer) ListTemplateRuns(ctx context.Context, req *pb.TemplateRunFilter) (*pb.TemplateRunList, error) {
	// LEFT JOIN on workflow_template so ad-hoc runs (workflow_template_id
	// IS NULL) still show up — their identity is carried by script_name /
	// script_sha256 instead of template name/version.
	query := `
		SELECT
			r.template_run_id,
			r.workflow_template_id,
			t.name AS template_name,
			t.version AS template_version,
			w.workflow_name,
			r.run_by,
			u.username,
			r.status,
			r.workflow_id,
			r.created_at,
			r.param_values,
			r.error_message,
			r.script_name,
			r.script_sha256
		FROM template_run r
		LEFT JOIN workflow_template t ON r.workflow_template_id = t.workflow_template_id
		LEFT JOIN workflow w ON r.workflow_id = w.workflow_id
		LEFT JOIN scitq_user u ON r.run_by = u.user_id
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
		var workflowTemplateID sql.NullInt32
		var workflowID sql.NullInt32
		var errorMsg sql.NullString
		var scriptName, scriptSha sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&r.TemplateRunId, &workflowTemplateID, &r.TemplateName, &r.TemplateVersion,
			&r.WorkflowName, &r.RunBy, &r.RunByUsername, &r.Status, &workflowID, &createdAt,
			&r.ParamValuesJson, &errorMsg, &scriptName, &scriptSha); err != nil {
			return nil, fmt.Errorf("failed to scan template run: %w", err)
		}

		if workflowTemplateID.Valid {
			r.WorkflowTemplateId = workflowTemplateID.Int32
		}
		r.CreatedAt = createdAt.Format(time.RFC3339)
		if workflowID.Valid {
			r.WorkflowId = proto.Int32(workflowID.Int32)
		}
		if errorMsg.Valid {
			r.ErrorMessage = proto.String(errorMsg.String)
		}
		if scriptName.Valid {
			r.ScriptName = proto.String(scriptName.String)
		}
		if scriptSha.Valid {
			r.ScriptSha256 = proto.String(scriptSha.String)
		}
		runs = append(runs, &r)
	}

	return &pb.TemplateRunList{Runs: runs}, nil
}

func (s *taskQueueServer) DeleteTemplateRun(ctx context.Context, req *pb.DeleteTemplateRunRequest) (*pb.Ack, error) {
	if req.GetTemplateRunId() == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("template_run_id is required")
	}

	const query = `DELETE FROM template_run WHERE template_run_id = $1`

	result, err := s.db.ExecContext(ctx, query, req.TemplateRunId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete template_run: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("could not determine if delete succeeded: %w", err)
	}
	if rows == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("template_run %d not found", req.TemplateRunId)
	}

	return &pb.Ack{Success: true}, nil
}

// DownloadTemplate returns the script content of a template.
func (s *taskQueueServer) DownloadTemplate(ctx context.Context, req *pb.DownloadTemplateRequest) (*pb.FileContent, error) {
	var scriptPath string

	if req.WorkflowTemplateId != nil && *req.WorkflowTemplateId != 0 {
		err := s.db.QueryRowContext(ctx,
			`SELECT script_path FROM workflow_template WHERE workflow_template_id = $1`,
			*req.WorkflowTemplateId).Scan(&scriptPath)
		if err != nil {
			return nil, fmt.Errorf("template not found: %w", err)
		}
	} else if req.Name != nil {
		query := `SELECT script_path FROM workflow_template WHERE name = $1`
		args := []any{*req.Name}
		if req.Version != nil && *req.Version != "latest" {
			query += ` AND version = $2`
			args = append(args, *req.Version)
		} else {
			query += ` ORDER BY uploaded_at DESC LIMIT 1`
		}
		err := s.db.QueryRowContext(ctx, query, args...).Scan(&scriptPath)
		if err != nil {
			return nil, fmt.Errorf("template not found: %w", err)
		}
	} else {
		return nil, fmt.Errorf("either workflow_template_id or name is required")
	}

	content, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file: %w", err)
	}
	return &pb.FileContent{
		Filename: filepath.Base(scriptPath),
		Content:  content,
	}, nil
}

// ---------------------------------------------------------------------------
// Module library (versioned YAML modules store — see specs/module_library.md)
// ---------------------------------------------------------------------------

// UpgradeBundledModules seeds/updates the `module` table from the bundled
// YAML modules shipped with the installed scitq2_modules Python package.
// See specs/module_library.md for rules. This is the server-side backend
// of `scitq module upgrade`.
//
// The server does not walk the Python site-packages directly; instead, it
// invokes `python -m scitq2.yaml_runner --list-bundled` inside the
// configured venv, which prints one JSON object per bundled module:
//
//	{"path", "version", "description", "content_sha256", "content_base64"}
//
// The server then matches each entry against the `module` table and applies
// seeding logic, returning a human report plus summary counters. Dry-run
// by default; `apply=true` commits the writes.
func (s *taskQueueServer) UpgradeBundledModules(ctx context.Context, req *pb.UpgradeBundledModulesRequest) (*pb.UpgradeBundledModulesResponse, error) {
	if u := GetUserFromContext(ctx); u == nil || !u.IsAdmin {
		return nil, fmt.Errorf("admin privileges required to upgrade bundled modules")
	}

	venvPython := filepath.Join(s.cfg.Scitq.ScriptVenv, "bin", "python")
	cmd := exec.CommandContext(ctx, venvPython, "-m", "scitq2.yaml_runner", "--list-bundled")
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate bundled modules: %w", err)
	}

	type bundledEntry struct {
		Path          string `json:"path"`
		Version       any    `json:"version"`
		Description   any    `json:"description"`
		ContentSha256 string `json:"content_sha256"`
		ContentBase64 string `json:"content_base64"`
	}

	var reportLines []string
	var inserted, forksPreserved, conflicts, skipped, upToDate int32

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e bundledEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			reportLines = append(reportLines, fmt.Sprintf("? (unparseable line) parse-error  %v", err))
			continue
		}
		versionStr, _ := e.Version.(string)
		descStr, _ := e.Description.(string)
		if versionStr == "" {
			skipped++
			reportLines = append(reportLines, fmt.Sprintf("%-40s (no version)         skipped  add `version: \"x.y.z\"` to the YAML to seed", e.Path))
			continue
		}
		if versionStr == "latest" {
			skipped++
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s skipped  reserved version alias", e.Path, versionStr))
			continue
		}

		content, err := base64.StdEncoding.DecodeString(e.ContentBase64)
		if err != nil {
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s error    %v", e.Path, versionStr, err))
			continue
		}

		var existingOrigin sql.NullString
		var existingSha sql.NullString
		err = s.db.QueryRowContext(ctx,
			`SELECT origin, content_sha FROM module WHERE path=$1 AND version=$2`,
			e.Path, versionStr).Scan(&existingOrigin, &existingSha)
		if err == sql.ErrNoRows {
			if req.Apply {
				_, err = s.db.ExecContext(ctx, `
					INSERT INTO module (path, version, content, content_sha, origin, bundled_sha, description)
					VALUES ($1, $2, $3, $4, 'B', $4, $5)
				`, e.Path, versionStr, content, e.ContentSha256, descStr)
				if err != nil {
					reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s error    %v", e.Path, versionStr, err))
					continue
				}
			}
			inserted++
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s bundled  new", e.Path, versionStr))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to check module %s@%s: %w", e.Path, versionStr, err)
		}

		switch existingOrigin.String {
		case "F":
			forksPreserved++
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s forked   keep (local edits detected)", e.Path, versionStr))
		case "B":
			if existingSha.String == e.ContentSha256 {
				upToDate++
				reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s bundled  up-to-date", e.Path, versionStr))
			} else {
				conflicts++
				reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s CONFLICT same (path,version) re-shipped with different bytes (packaging bug? not overwritten)", e.Path, versionStr))
			}
		case "L":
			conflicts++
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s CONFLICT local upload collides with a bundled version number (not overwritten)", e.Path, versionStr))
		default:
			reportLines = append(reportLines, fmt.Sprintf("%-40s %-20s ?        unexpected origin=%s", e.Path, versionStr, existingOrigin.String))
		}
	}

	// Footer
	footer := fmt.Sprintf("\n%d inserted, %d forks preserved, %d conflicts, %d skipped, %d up-to-date.",
		inserted, forksPreserved, conflicts, skipped, upToDate)
	if !req.Apply {
		footer += "\n(dry-run — re-run with --apply to write)"
	}

	return &pb.UpgradeBundledModulesResponse{
		Report:         strings.Join(reportLines, "\n") + footer,
		Inserted:       inserted,
		ForksPreserved: forksPreserved,
		Conflicts:      conflicts,
		Skipped:        skipped,
		UpToDate:       upToDate,
	}, nil
}

// GetModuleOrigin returns provenance metadata for a single module row.
// Accepts `path` with an optional `version`; empty version resolves to
// the highest-ordered row at that path.
func (s *taskQueueServer) GetModuleOrigin(ctx context.Context, req *pb.ModuleOriginRequest) (*pb.ModuleOriginResponse, error) {
	path, err := normalizeModulePath(req.Path)
	if err != nil {
		return nil, err
	}

	var (
		version, originCode, contentSha string
		bundledSha, description         sql.NullString
		uploadedAt                      time.Time
		uploadedBy                      sql.NullInt32
	)
	if req.Version == "" || req.Version == "latest" {
		err = s.db.QueryRowContext(ctx, `
			SELECT version, origin, content_sha, bundled_sha, description, uploaded_at, uploaded_by
			  FROM module
			 WHERE path = $1
			 ORDER BY version DESC
			 LIMIT 1
		`, path).Scan(&version, &originCode, &contentSha, &bundledSha, &description, &uploadedAt, &uploadedBy)
	} else {
		version = req.Version
		err = s.db.QueryRowContext(ctx, `
			SELECT origin, content_sha, bundled_sha, description, uploaded_at, uploaded_by
			  FROM module
			 WHERE path = $1 AND version = $2
		`, path, req.Version).Scan(&originCode, &contentSha, &bundledSha, &description, &uploadedAt, &uploadedBy)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("module %q not found", req.Path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch module origin: %w", err)
	}

	// Resolve uploaded_by → username (best-effort; empty on failure).
	var username string
	if uploadedBy.Valid {
		_ = s.db.QueryRowContext(ctx, `SELECT username FROM scitq_user WHERE user_id = $1`, uploadedBy.Int32).Scan(&username)
	}

	// "fork is outdated" heuristic: a forked row whose path has a newer
	// bundled row with different bundled_sha256 from ours is a candidate
	// for review.
	forkOutdated := false
	if originCode == "F" && bundledSha.Valid {
		var latestBundledSha sql.NullString
		_ = s.db.QueryRowContext(ctx, `
			SELECT content_sha FROM module
			 WHERE path = $1 AND origin = 'B' AND version > $2
			 ORDER BY version DESC LIMIT 1
		`, path, version).Scan(&latestBundledSha)
		if latestBundledSha.Valid && latestBundledSha.String != bundledSha.String {
			forkOutdated = true
		}
	}

	originLabel := map[string]string{"B": "bundled", "L": "local", "F": "forked"}[originCode]

	return &pb.ModuleOriginResponse{
		Path:            path,
		Version:         version,
		Origin:          originLabel,
		ContentSha256:   contentSha,
		BundledSha256:   bundledSha.String,
		Description:     description.String,
		UploadedAt:      uploadedAt.UTC().Format(time.RFC3339),
		UploadedBy:      username,
		ForkIsOutdated:  forkOutdated,
	}, nil
}

// ForkModule clones a source module row to a new (path, new_version) row
// with origin='F'. Typical flow: admin forks a bundled module so they can
// `download` + edit + `upload --force` with a site-specific change, keeping
// the bundled row intact for future `module upgrade` reconciliation.
func (s *taskQueueServer) ForkModule(ctx context.Context, req *pb.ForkModuleRequest) (*pb.Ack, error) {
	if u := GetUserFromContext(ctx); u == nil || !u.IsAdmin {
		return nil, fmt.Errorf("admin privileges required to fork a module")
	}
	path, err := normalizeModulePath(req.SourcePath)
	if err != nil {
		return nil, err
	}
	if req.NewVersion == "" {
		return nil, fmt.Errorf("new_version is required")
	}
	if req.NewVersion == "latest" {
		return nil, fmt.Errorf("'latest' is a reserved version alias")
	}

	// Load source row
	var srcVersion, srcContentSha string
	var srcContent []byte
	var srcDescription sql.NullString
	if req.SourceVersion == "" || req.SourceVersion == "latest" {
		err = s.db.QueryRowContext(ctx, `
			SELECT version, content, content_sha, description FROM module
			 WHERE path = $1 ORDER BY version DESC LIMIT 1
		`, path).Scan(&srcVersion, &srcContent, &srcContentSha, &srcDescription)
	} else {
		srcVersion = req.SourceVersion
		err = s.db.QueryRowContext(ctx, `
			SELECT content, content_sha, description FROM module
			 WHERE path = $1 AND version = $2
		`, path, req.SourceVersion).Scan(&srcContent, &srcContentSha, &srcDescription)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("source module %s@%s not found", path, req.SourceVersion)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load source module: %w", err)
	}

	var uploadedBy *uint32
	if u := GetUserFromContext(ctx); u != nil {
		uid := uint32(u.UserID)
		uploadedBy = &uid
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO module (path, version, content, content_sha, origin, bundled_sha, description, uploaded_by)
		VALUES ($1, $2, $3, $4, 'F', $4, $5, $6)
	`, path, req.NewVersion, srcContent, srcContentSha, srcDescription, uploadedBy)
	if err != nil {
		// Most likely: unique violation on (path, new_version) — surface cleanly.
		return nil, fmt.Errorf("failed to create fork %s@%s: %w", path, req.NewVersion, err)
	}
	log.Printf("📦 Module forked: %s@%s → %s@%s (origin=F)", path, srcVersion, path, req.NewVersion)
	return &pb.Ack{Success: true}, nil
}

// importLegacyOnDiskModules migrates pre-library private modules living as
// flat files in `{script_root}/modules/*.yaml` into the `module` table. Runs
// once at server startup; any module already present in the DB is left
// alone, so repeated invocations are idempotent and safe to leave wired
// after migration is complete.
//
// Legacy modules had no version field; we import them as `0.0.0` so they
// stay addressable. Subsequent uploads should bump to a real version.
func (s *taskQueueServer) importLegacyOnDiskModules() {
	modulesDir := filepath.Join(s.cfg.Scitq.ScriptRoot, "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("⚠️ module migration: cannot read %s: %v", modulesDir, err)
		return
	}
	imported := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path, err := normalizeModulePath(name)
		if err != nil {
			log.Printf("⚠️ module migration: skipping %q (%v)", name, err)
			continue
		}

		fullPath := filepath.Join(modulesDir, name)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("⚠️ module migration: cannot read %s: %v", fullPath, err)
			continue
		}
		version := extractModuleVersion(content)
		if version == "" {
			version = "0.0.0"
		}
		if version == "latest" {
			log.Printf("⚠️ module migration: skipping %q (has reserved version 'latest')", name)
			continue
		}

		// Skip if already present (idempotent).
		var exists int
		err = s.db.QueryRow(`SELECT 1 FROM module WHERE path=$1 AND version=$2`, path, version).Scan(&exists)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			log.Printf("⚠️ module migration: check failed for %s@%s: %v", path, version, err)
			continue
		}

		sha := sha256.Sum256(content)
		shaHex := hex.EncodeToString(sha[:])
		description := extractModuleDescription(content)
		if _, err := s.db.Exec(`
			INSERT INTO module (path, version, content, content_sha, origin, description)
			VALUES ($1, $2, $3, $4, 'L', $5)
		`, path, version, content, shaHex, description); err != nil {
			log.Printf("⚠️ module migration: insert failed for %s@%s: %v", path, version, err)
			continue
		}
		imported++
	}
	if imported > 0 {
		log.Printf("📦 Migrated %d legacy on-disk module(s) into the module table", imported)
	}
}


// normalizeModulePath validates and canonicalises the namespace path of a
// module. The on-the-wire `filename` field may be `my_aligner`,
// `my_aligner.yaml`, `genetic/my_aligner`, or `genetic/my_aligner.yaml`. We
// strip the extension and reject traversal / absolute paths / empty segments.
func normalizeModulePath(filename string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	name := filename
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(name, ext) {
			name = strings.TrimSuffix(name, ext)
			break
		}
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("module path must be relative (got %q)", filename)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid module path %q", filename)
		}
	}
	return name, nil
}

// parseModulePathVersion parses a reference of the form `path` or
// `path@version`. An empty version string means "latest".
func parseModulePathVersion(ref string) (string, string, error) {
	path := ref
	version := ""
	if idx := strings.LastIndex(ref, "@"); idx >= 0 {
		path = ref[:idx]
		version = ref[idx+1:]
	}
	norm, err := normalizeModulePath(path)
	if err != nil {
		return "", "", err
	}
	return norm, version, nil
}

// extractModuleVersion pulls the `version:` field from the YAML content.
// Keeps this deliberately simple (regex on the top-level key) instead of
// hauling in a YAML parser — modules with an inline `version:` inside a
// nested block would be unusual and can be addressed later.
func extractModuleVersion(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "version:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
			v = strings.Trim(v, `"'`)
			return v
		}
	}
	return ""
}

// extractModuleDescription does the same for `description:` (best-effort,
// single-line only; multi-line descriptions just yield "").
func extractModuleDescription(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "description:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))
			v = strings.Trim(v, `"'`)
			if strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") {
				return "" // block scalar — skip for now
			}
			return v
		}
	}
	return ""
}

// UploadModule stores a YAML module in the DB-backed module library.
//
// Backward-compat notes:
//   - `filename` now accepts slashes (e.g. `genetic/fastp` or
//     `genetic/fastp.yaml`). Flat names (the pre-library behaviour) keep
//     working unchanged.
//   - The YAML content MUST include a top-level `version:` field. Legacy
//     uploads without it are rejected with a clear error — the caller can
//     add `version: "0.0.1"` to their YAML and retry.
//   - `force=true` overwrites an existing (path, version) row in place;
//     `force=false` rejects duplicates. This mirrors the old "refuse
//     without --force" semantic, now scoped to (path, version).
func (s *taskQueueServer) UploadModule(ctx context.Context, req *pb.UploadModuleRequest) (*pb.Ack, error) {
	path, err := normalizeModulePath(req.Filename)
	if err != nil {
		return nil, err
	}
	if len(req.Content) == 0 {
		return nil, fmt.Errorf("module content is empty")
	}
	version := extractModuleVersion(req.Content)
	if version == "" {
		return nil, fmt.Errorf("module %q has no top-level 'version:' field — required by the module library", path)
	}
	if version == "latest" {
		return nil, fmt.Errorf("'latest' is a reserved version alias; use a concrete version string")
	}

	sha := sha256.Sum256(req.Content)
	shaHex := hex.EncodeToString(sha[:])
	description := extractModuleDescription(req.Content)

	var uploadedBy *uint32
	if u := GetUserFromContext(ctx); u != nil {
		uid := uint32(u.UserID)
		uploadedBy = &uid
	}

	// Existing row?
	var existingOrigin sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT origin FROM module WHERE path = $1 AND version = $2`,
		path, version).Scan(&existingOrigin)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to check existing module: %w", err)
	}
	if err == nil {
		if !req.Force {
			return nil, fmt.Errorf("module %q version %q already exists (use --force to overwrite)", path, version)
		}
		// Overwriting: preserve 'B' as 'F' to reflect local edit of a bundled row.
		newOrigin := existingOrigin.String
		if newOrigin == "B" {
			newOrigin = "F"
		} else if newOrigin == "" {
			newOrigin = "L"
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE module
			   SET content=$1, content_sha=$2, origin=$3, uploaded_at=NOW(),
			       uploaded_by=$4, description=$5
			 WHERE path=$6 AND version=$7
		`, req.Content, shaHex, newOrigin, uploadedBy, description, path, version)
		if err != nil {
			return nil, fmt.Errorf("failed to update module %q@%q: %w", path, version, err)
		}
		log.Printf("📦 Module updated: %s@%s (%d bytes, origin=%s)", path, version, len(req.Content), newOrigin)
		return &pb.Ack{Success: true}, nil
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO module (path, version, content, content_sha, origin, uploaded_by, description)
		VALUES ($1, $2, $3, $4, 'L', $5, $6)
	`, path, version, req.Content, shaHex, uploadedBy, description)
	if err != nil {
		return nil, fmt.Errorf("failed to insert module %q@%q: %w", path, version, err)
	}
	log.Printf("📦 Module uploaded: %s@%s (%d bytes, origin=L)", path, version, len(req.Content))
	return &pb.Ack{Success: true}, nil
}

// ListModules returns every (path, version) row. Populates both the legacy
// flat `modules` string list and the structured `entries`. Old clients
// reading only `modules` keep working unchanged.
func (s *taskQueueServer) ListModules(ctx context.Context, _ *emptypb.Empty) (*pb.ModuleList, error) {
	return s.listModulesCore(ctx, "", false)
}

// ListModulesFiltered is the v2 surface taking an optional path filter and
// a latest-only flag. Backs `scitq module list --versions X` and `--latest`.
func (s *taskQueueServer) ListModulesFiltered(ctx context.Context, req *pb.ModuleListFilter) (*pb.ModuleList, error) {
	pathFilter := ""
	if req.Path != nil {
		p, err := normalizeModulePath(*req.Path)
		if err != nil {
			return nil, err
		}
		pathFilter = p
	}
	latestOnly := false
	if req.LatestOnly != nil {
		latestOnly = *req.LatestOnly
	}
	return s.listModulesCore(ctx, pathFilter, latestOnly)
}

func (s *taskQueueServer) listModulesCore(ctx context.Context, pathFilter string, latestOnly bool) (*pb.ModuleList, error) {
	query := `
		SELECT path, version, origin, COALESCE(description, '')
		  FROM module
	`
	args := []any{}
	if pathFilter != "" {
		query += " WHERE path = $1"
		args = append(args, pathFilter)
	}
	query += " ORDER BY path, version"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list modules: %w", err)
	}
	defer rows.Close()

	originLabel := map[string]string{"B": "bundled", "L": "local", "F": "forked"}
	var entries []*pb.ModuleEntry
	var names []string
	for rows.Next() {
		var path, version, origin, description string
		if err := rows.Scan(&path, &version, &origin, &description); err != nil {
			return nil, fmt.Errorf("failed to scan module row: %w", err)
		}
		entries = append(entries, &pb.ModuleEntry{
			Path:        path,
			Version:     version,
			Origin:      originLabel[origin],
			Description: description,
		})
		names = append(names, path+"@"+version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// latest_only: keep only the highest-ordered version per path (rows are
	// already sorted ascending, so the last occurrence of each path wins).
	if latestOnly {
		keep := make(map[string]int)
		for i, e := range entries {
			keep[e.Path] = i
		}
		newEntries := make([]*pb.ModuleEntry, 0, len(keep))
		newNames := make([]string, 0, len(keep))
		for i, e := range entries {
			if keep[e.Path] == i {
				newEntries = append(newEntries, e)
				newNames = append(newNames, names[i])
			}
		}
		entries = newEntries
		names = newNames
	}

	return &pb.ModuleList{Modules: names, Entries: entries}, nil
}

// DownloadModule fetches module bytes from the library. Accepts:
//   - `genetic/fastp@1.2.0` — path + exact version
//   - `genetic/fastp@latest` or `genetic/fastp` — highest version
//   - bare `fastp` — legacy flat path, highest version
func (s *taskQueueServer) DownloadModule(ctx context.Context, req *pb.DownloadModuleRequest) (*pb.FileContent, error) {
	path, version, err := parseModulePathVersion(req.Filename)
	if err != nil {
		return nil, err
	}

	var content []byte
	var resolvedVersion string
	if version == "" || version == "latest" {
		err = s.db.QueryRowContext(ctx, `
			SELECT content, version FROM module
			 WHERE path = $1
			 ORDER BY version DESC
			 LIMIT 1
		`, path).Scan(&content, &resolvedVersion)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT content, version FROM module
			 WHERE path = $1 AND version = $2
		`, path, version).Scan(&content, &resolvedVersion)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("module %q not found", req.Filename)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load module %q: %w", req.Filename, err)
	}
	return &pb.FileContent{
		Filename: path + "@" + resolvedVersion,
		Content:  content,
	}, nil
}
