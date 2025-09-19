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

	ws "github.com/scitq/scitq/server/websocket"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/server/config"
	"google.golang.org/protobuf/proto"

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
	templateRunID int32, // only used for "run"
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
	serverName := s.cfg.Scitq.ServerFQDN
	if serverName == "" {
		serverName = "localhost"
	}
	env := []string{
		fmt.Sprintf("SCITQ_SERVER=%s:%d", serverName, s.cfg.Scitq.Port),
		fmt.Sprintf("SCITQ_TOKEN=%s", authToken),
	}
	if mode == "run" && templateRunID != 0 {
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

func (s *taskQueueServer) RunTemplate(ctx context.Context, req *pb.RunTemplateRequest) (*pb.TemplateRun, error) {
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
	stdout, stderr, exitCode, runErr := s.scriptRunner(
		ctx,
		filepath.Join(s.cfg.Scitq.ScriptRoot, fmt.Sprintf("%d.py", req.WorkflowTemplateId)),
		"run",               // mode
		req.ParamValuesJson, // paramJSON
		templateRunId,       // templateRunID
		authToken,
	)

	if runErr != nil || exitCode != 0 {
		errMsg := fmt.Sprintf("script failed (exit=%d): \n %s \n %s", exitCode, stdout, stderr)
		log.Printf("⚠️ RunTemplate script error: %s", errMsg)

		_, _ = s.db.ExecContext(ctx, `
			UPDATE template_run SET error_message = $1, status = 'F' WHERE template_run_id = $2
		`, errMsg, templateRunId)
		_, _ = s.db.ExecContext(ctx, `
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
		_, err = s.db.ExecContext(ctx, `
			UPDATE template_run SET status = 'S' WHERE template_run_id = $1
		`, templateRunId)
		if err != nil {
			log.Printf("⚠️ failed to update template_run status: %v", err)
		}
		var workflowID sql.NullInt32
		var tasksActivated int32

		err = s.db.QueryRowContext(ctx, `
				WITH run_info AS (
					SELECT workflow_id FROM template_run WHERE template_run_id = $1
				),
				updated AS (
					UPDATE task
					SET status = 'P'
					WHERE status = 'O'
						AND step_id IN (
						SELECT step_id FROM step WHERE workflow_id = (SELECT workflow_id FROM run_info)
						)
					RETURNING 1
				)
				SELECT (SELECT workflow_id FROM run_info), COUNT(*) FROM updated
			`, templateRunId).Scan(&workflowID, &tasksActivated)

		if err != nil {
			msg := fmt.Sprintf("⚠️ failed to update task statuses: %v", err)
			log.Println(msg)
			_, _ = s.db.ExecContext(ctx, `
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
			_, _ = s.db.ExecContext(ctx, `
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

		if tasksActivated == 0 {
			log.Printf("⚠️ No tasks activated for workflow_id=%d (template_run_id=%d)", workflowID.Int32, templateRunId)
		} else {
			log.Printf("✅ %d tasks activated for workflow_id=%d (template_run_id=%d)", tasksActivated, workflowID.Int32, templateRunId)
		}
	}

	log.Printf("✅ RunTemplate script completed successfully: %s", stdout)
	if stderr != "" {
		log.Printf("⚠️ Warning: %s", stderr)
	}

	// ✅ Script ran successfully (but workflow_id will be updated later by Python)
	return &pb.TemplateRun{
		TemplateRunId:      templateRunId,
		WorkflowTemplateId: req.WorkflowTemplateId,
		CreatedAt:          createdAt.Format(time.RFC3339),
		ParamValuesJson:    req.ParamValuesJson,
		Status:             "S",
		ErrorMessage:       proto.String(stderr),
	}, nil
}

type ParamSpec struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
	Help     string   `json:"help"` // Or `json:"description"` if your field is named so
	Choices  []string `json:"choices,omitempty"`
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
			Success: false,
			Message: fmt.Sprintf("script params execution failed: %v\n%s", err, stderrMeta),
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
			r.error_message
		FROM template_run r
		JOIN workflow_template t ON r.workflow_template_id = t.workflow_template_id
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
		var workflowID sql.NullInt32
		var errorMsg sql.NullString
		var createdAt time.Time

		if err := rows.Scan(&r.TemplateRunId, &r.WorkflowTemplateId, &r.TemplateName, &r.TemplateVersion,
			&r.WorkflowName, &r.RunBy, &r.RunByUsername, &r.Status, &workflowID, &createdAt, &r.ParamValuesJson, &errorMsg); err != nil {
			return nil, fmt.Errorf("failed to scan template run: %w", err)
		}

		r.CreatedAt = createdAt.Format(time.RFC3339)
		if workflowID.Valid {
			r.WorkflowId = proto.Int32(workflowID.Int32)
		}
		if errorMsg.Valid {
			r.ErrorMessage = proto.String(errorMsg.String)
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
