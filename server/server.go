package server

import (
	"bufio"
	// "container/list"
	"context"
	"crypto/tls"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmtsciencedev/scitq2/server/config"
	"github.com/gmtsciencedev/scitq2/server/memory"
	"github.com/gmtsciencedev/scitq2/server/protofilter"
	"github.com/gmtsciencedev/scitq2/server/providers"
	"github.com/gmtsciencedev/scitq2/server/watchdog"

	"github.com/gmtsciencedev/scitq2/fetch"

	"github.com/gmtsciencedev/scitq2/server/recruitment"
	"github.com/golang-jwt/jwt"
	"github.com/hpcloud/tail"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

	"github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const defaultJobRetry = 3
const defaultJobTimeout = 10 * time.Minute
const defaultJobConcurrency = 10
const defaultJobQueueSize = 100
const DefaultRcloneConfig = "/etc/rclone.conf"

//go:embed migrations/*
var embeddedMigrations embed.FS

//go:embed certificates/*
var embeddedCertificates embed.FS

type taskQueueServer struct {
	pb.UnimplementedTaskQueueServer
	logRoot  string
	db       *sql.DB
	cfg      config.Config
	jobQueue chan Job
	//jobWG     sync.WaitGroup
	providers      map[uint32]providers.Provider
	providerConfig map[string]config.ProviderConfig
	semaphore      chan struct{} // Semaphore to limit concurrency
	assignTrigger  uint32
	qm             recruitment.QuotaManager
	watchdog       *watchdog.Watchdog

	stopWatchdog       chan struct{}
	workerWeightMemory *sync.Map // worker_id -> map[task_id]float64
	workerStats        *sync.Map
	sslCertificatePEM  string
}

func newTaskQueueServer(cfg config.Config, db *sql.DB, logRoot string) *taskQueueServer {
	workerWeightMemory, err := memory.LoadWeightMemory(context.Background(), db, "weight_memory")
	if err != nil {
		log.Printf("⚠️ Creating a new weight memory: %v", err)
		workerWeightMemory = &sync.Map{}
	}
	s := &taskQueueServer{
		db:                 db,
		cfg:                cfg,
		logRoot:            logRoot,
		jobQueue:           make(chan Job, defaultJobQueueSize),
		semaphore:          make(chan struct{}, defaultJobConcurrency),
		providers:          make(map[uint32]providers.Provider),
		providerConfig:     make(map[string]config.ProviderConfig),
		assignTrigger:      DefaultAssignTrigger, // buffered, avoids blocking
		workerWeightMemory: workerWeightMemory,
		stopWatchdog:       make(chan struct{}),
		workerStats:        &sync.Map{},
	}
	//go s.assignTasksLoop()
	go s.waitForAssignEvents()

	s.watchdog = watchdog.NewWatchdog(
		time.Duration(cfg.Scitq.IdleTimeout)*time.Second,
		time.Duration(cfg.Scitq.NewWorkerIdleTimeout)*time.Second,
		time.Duration(cfg.Scitq.OfflineTimeout)*time.Second,
		10*time.Second, // ticker interval
		func(workerID uint32, newStatus string) error {
			_, err := s.UpdateWorkerStatus(context.Background(), &pb.WorkerStatus{WorkerId: workerID, Status: newStatus})
			return err
		}, // callback
		func(workerID uint32) error {
			_, err := s.DeleteWorker(context.Background(), &pb.WorkerId{WorkerId: workerID})
			return err
		}, // callback
	)

	workers, err := FetchWorkersForWatchdog(context.Background(), s.db)
	if err != nil {
		log.Printf("⚠️ Failed to rebuild watchdog memory: %v", err)
	}
	s.watchdog.RebuildFromWorkers(workers)

	go s.watchdog.Run(s.stopWatchdog)

	return s
}

func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.TaskRequest) (*pb.TaskResponse, error) {
	var taskID int
	// Determine initial status: "W" if dependencies, otherwise "P"
	initialStatus := "P"
	if len(req.Dependency) > 0 {
		initialStatus = "W"
	} else if req.Status != "" {
		initialStatus = req.Status
	}

	err := s.db.QueryRow(
		`INSERT INTO task (command, shell, container, container_options, step_id, 
					input, resource, output, retry, is_final, uses_cache, 
					download_timeout, running_timeout, upload_timeout,  
					status, task_name, created_at) 
		VALUES ($1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, 
			$12, $13, $14, 
			$15, $16, NOW()) 
		RETURNING task_id`,
		req.Command, req.Shell, req.Container, req.ContainerOptions, req.StepId,
		req.Input, req.Resource, req.Output, req.Retry, req.IsFinal, req.UsesCache,
		req.DownloadTimeout, req.RunningTimeout, req.UploadTimeout,
		initialStatus, req.TaskName,
	).Scan(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	log.Printf("✅ Task %d submitted (Command: %s, Container: %s)", taskID, req.Command, req.Container)

	// Insert dependencies if any
	if len(req.Dependency) > 0 {
		stmt, err := s.db.PrepareContext(ctx, `
			INSERT INTO task_dependencies (dependent_task_id, prerequisite_task_id)
			VALUES ($1, $2)
		`)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare dependency insert: %w", err)
		}
		defer stmt.Close()

		for _, depID := range req.Dependency {
			if _, err := stmt.ExecContext(ctx, taskID, depID); err != nil {
				return nil, fmt.Errorf("failed to insert dependency (%d -> %d): %w", depID, taskID, err)
			}
		}
	}

	// Trigger assignment only if task is immediately runnable
	if initialStatus == "P" {
		s.triggerAssign()
	}

	return &pb.TaskResponse{TaskId: uint32(taskID)}, nil
}

func (s *taskQueueServer) GetRcloneConfig(ctx context.Context, req *emptypb.Empty) (*pb.RcloneConfig, error) {
	data, err := os.ReadFile(DefaultRcloneConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read rclone config: %w", err)
	}
	return &pb.RcloneConfig{Config: string(data)}, nil
}

func shouldTriggerAssignFor(status string) bool {
	switch status {
	case "P", "S", "F", "C", "R", "U", "V", "X":
		return true
	default:
		return false
	}
}

func (s *taskQueueServer) UpdateTaskStatus(ctx context.Context, req *pb.TaskStatusUpdate) (*pb.Ack, error) {
	var workerID sql.NullInt32
	err := s.db.QueryRowContext(ctx, `
        UPDATE task
        SET status = $1
        WHERE task_id = $2
        RETURNING worker_id
    `, req.NewStatus, req.TaskId).Scan(&workerID)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update task status: %w", err)
	}

	switch req.NewStatus {
	case "C":
		if !workerID.Valid {
			log.Printf("⚠️ warning: task %d accepted but worker_id is NULL", req.TaskId)
		} else {
			s.watchdog.TaskAccepted(uint32(workerID.Int32))
		}
	case "S", "F":
		if !workerID.Valid {
			log.Printf("⚠️ warning: task %d ended in %s but worker_id is NULL", req.TaskId, req.NewStatus)
		} else {
			s.watchdog.TaskFinished(uint32(workerID.Int32))
		}
	}

	// Push logic: on success, check dependent "W" tasks
	if req.NewStatus == "S" {
		// Find candidate dependent tasks
		rows, err := s.db.QueryContext(ctx, `
            SELECT DISTINCT d.dependent_task_id
            FROM task_dependencies d
            JOIN task t ON d.dependent_task_id = t.task_id
            WHERE d.prerequisite_task_id = $1
              AND t.status = 'W'
        `, req.TaskId)
		if err != nil {
			log.Printf("❌ failed to find dependent tasks: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var depTaskID int64
				if err := rows.Scan(&depTaskID); err != nil {
					log.Printf("⚠️ failed to scan dependent task: %v", err)
					continue
				}

				// Check if all prerequisites are now 'S'
				var allDone bool
				err = s.db.QueryRowContext(ctx, `
                    SELECT NOT EXISTS (
                        SELECT 1
                        FROM task_dependencies d
                        JOIN task t ON d.prerequisite_task_id = t.task_id
                        WHERE d.dependent_task_id = $1
                          AND t.status != 'S'
                    )
                `, depTaskID).Scan(&allDone)
				if err != nil {
					log.Printf("⚠️ failed to check dependencies for task %d: %v", depTaskID, err)
					continue
				}

				if allDone {
					// Promote to "P"
					res, err := s.db.ExecContext(ctx, `
                        UPDATE task
                        SET status = 'P'
                        WHERE task_id = $1 AND status = 'W'
                    `, depTaskID)
					if err != nil {
						log.Printf("⚠️ failed to promote task %d to 'P': %v", depTaskID, err)
						continue
					}
					n, _ := res.RowsAffected()
					if n > 0 {
						log.Printf("✅ task %d now pending (dependencies resolved)", depTaskID)
						s.triggerAssign()
					}
				}
			}
		}
	}

	if shouldTriggerAssignFor(req.NewStatus) {
		s.triggerAssign()
	}
	return &pb.Ack{Success: true}, nil
}

func getLogPath(taskID uint32, logType string, logRoot string) string {
	dir := fmt.Sprintf("%s/%d", logRoot, taskID/1000)
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, fmt.Sprintf("%d_%s.log", taskID, logType))
}

func (s *taskQueueServer) SendTaskLogs(stream pb.TaskQueue_SendTaskLogsServer) error {
	for {
		logEntry, err := stream.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				return stream.SendAndClose(&pb.Ack{Success: true})
			}
			return err
		}
		logPath := getLogPath(logEntry.TaskId, logEntry.LogType, s.logRoot)
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		defer file.Close()
		fmt.Fprintln(file, logEntry.LogText)
	}
}

func (s *taskQueueServer) StreamTaskLogsErr(req *pb.TaskId, stream pb.TaskQueue_StreamTaskLogsErrServer) error {
	var status string
	fmt.Printf("Streaming logs for task %v\n", req.TaskId)
	logPath := getLogPath(req.TaskId, "stderr", s.logRoot)

	// Configure tail to follow the file in follow mode
	t, err := tail.TailFile(logPath, tail.Config{
		Follow:    true,  // keep reading new appended lines
		ReOpen:    true,  // reopen file automatically if rotated
		MustExist: false, // do not block if file does not exist yet
		Poll:      true,  // use polling instead of inotify for compatibility
	})
	if err != nil {
		return fmt.Errorf("failed to tail file: %w", err)
	}
	defer t.Cleanup()

	for {
		select {
		case line, ok := <-t.Lines:
			if !ok {
				fmt.Println("tail.Lines channel closed, stopping stream")
				return nil
			}

			// Send the log line to the client
			err := stream.Send(&pb.TaskLog{
				TaskId:  req.TaskId,
				LogType: "stderr",
				LogText: line.Text,
			})
			if err != nil {
				return err
			}

		case <-time.After(500 * time.Millisecond):
			// Periodically check if the task is finished
			err := s.db.QueryRow(`SELECT status FROM task WHERE task_id = $1`, req.TaskId).Scan(&status)
			if err != nil {
				return fmt.Errorf("failed to query task status: %w", err)
			}
			if status == "S" || status == "F" {
				fmt.Println("Task is finished (success or failed).")
				return nil
			}
		}
	}
}

func (s *taskQueueServer) StreamTaskLogsOutput(req *pb.TaskId, stream pb.TaskQueue_StreamTaskLogsOutputServer) error {
	var status string
	fmt.Printf("Streaming logs for task %v\n", req.TaskId)
	logPath := getLogPath(req.TaskId, "stdout", s.logRoot)

	// Configure tail to follow the file in follow mode
	t, err := tail.TailFile(logPath, tail.Config{
		Follow:    true,  // keep reading new appended lines
		ReOpen:    true,  // reopen file automatically if rotated
		MustExist: false, // do not block if file does not exist yet
		Poll:      true,  // use polling instead of inotify for compatibility
	})
	if err != nil {
		return fmt.Errorf("failed to tail file: %w", err)
	}
	defer t.Cleanup()

	for {
		select {
		case line, ok := <-t.Lines:
			if !ok {
				fmt.Println("tail.Lines channel closed, stopping stream")
				return nil
			}

			// Send the log line to the client
			err := stream.Send(&pb.TaskLog{
				TaskId:  req.TaskId,
				LogType: "stdout",
				LogText: line.Text,
			})
			if err != nil {
				return err
			}

		case <-time.After(500 * time.Millisecond):
			// Periodically check if the task is finished
			err := s.db.QueryRow(`SELECT status FROM task WHERE task_id = $1`, req.TaskId).Scan(&status)
			if err != nil {
				return fmt.Errorf("failed to query task status: %w", err)
			}
			if status == "S" || status == "F" {
				fmt.Println("Task is finished (success or failed).")
				return nil
			}
		}
	}
}

// totalLines = number of lines to return
// skipFromEnd = number of lines to skip from the end (for "load more")
func tailLines(path string, totalLines int, skipFromEnd int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read all lines (needed to know the end)
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Calculate the range to return
	end := len(lines) - skipFromEnd
	start := end - totalLines
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > len(lines) {
		end = len(lines)
	}

	return lines[start:end], nil
}

func (s *taskQueueServer) GetLogsChunk(ctx context.Context, req *pb.GetLogsRequest) (*pb.LogChunkList, error) {
	var result []*pb.LogChunk

	// Iterate over each requested task ID
	for _, taskId := range req.TaskIds {
		skip := 0
		// If a skip value is provided, use it to skip lines from the end of the log
		if req.SkipFromEnd != nil {
			skip = int(*req.SkipFromEnd)
		}

		// If the requested log type is stdout, fetch only stdout logs
		if req.LogType != nil && *req.LogType == "stdout" {
			stdoutPath := getLogPath(taskId, "stdout", s.logRoot)
			stdoutTail, _ := tailLines(stdoutPath, int(req.ChunkSize), skip) // Read last chunk of stdout log lines
			result = append(result, &pb.LogChunk{
				TaskId: taskId,
				Stdout: stdoutTail,
			})

			// If the requested log type is stderr, fetch only stderr logs
		} else if req.LogType != nil && *req.LogType == "stderr" {
			stderrPath := getLogPath(taskId, "stderr", s.logRoot)
			stderrTail, _ := tailLines(stderrPath, int(req.ChunkSize), skip) // Read last chunk of stderr log lines
			result = append(result, &pb.LogChunk{
				TaskId: taskId,
				Stderr: stderrTail,
			})

			// If no specific log type is requested, fetch both stdout and stderr logs
		} else {
			stdoutPath := getLogPath(taskId, "stdout", s.logRoot)
			stderrPath := getLogPath(taskId, "stderr", s.logRoot)
			stdoutTail, _ := tailLines(stdoutPath, int(req.ChunkSize), skip) // Read last chunk of stdout log lines
			stderrTail, _ := tailLines(stderrPath, int(req.ChunkSize), skip) // Read last chunk of stderr log lines
			result = append(result, &pb.LogChunk{
				TaskId: taskId,
				Stdout: stdoutTail,
				Stderr: stderrTail,
			})
		}
	}

	// Return the list of log chunks for all requested tasks
	return &pb.LogChunkList{Logs: result}, nil
}

func (s *taskQueueServer) RegisterWorker(ctx context.Context, req *pb.WorkerInfo) (*pb.WorkerId, error) {
	var workerID uint32
	var isPermanent bool

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// **Check if worker already exists**
	err = tx.QueryRow(`SELECT worker_id,is_permanent FROM worker WHERE worker_name = $1`, req.Name).Scan(&workerID, &isPermanent)
	if err == sql.ErrNoRows {
		// **Worker doesn't exist, create a new worker ID**
		err = tx.QueryRow(`INSERT INTO worker (worker_name, concurrency, status, last_ping) VALUES ($1, $2, 'R', NOW()) RETURNING worker_id`,
			req.Name, req.Concurrency).Scan(&workerID)
		if err != nil {
			log.Printf("⚠️ Failed to create worker: %v", err)
			return nil, fmt.Errorf("failed to create worker: %w", err)
		}
		log.Printf("✅ Registered new worker %s with ID %d", req.Name, workerID)
	} else if err != nil {
		log.Printf("⚠️ Failed to check existing worker: %v", err)
		return nil, fmt.Errorf("failed to check existing worker: %w", err)
	} else {
		// **Worker already exists, update concurrency & last ping**
		//_, err = tx.Exec(`UPDATE worker SET concurrency = $1, last_ping = NOW() WHERE worker_id = $2`, req.Concurrency, workerID)
		//if err != nil {
		//	log.Printf("⚠️ Failed to update worker %s: %v", req.Name, err)
		//	return nil, fmt.Errorf("failed to update worker: %w", err)
		//}
		_, err = tx.Exec(`UPDATE task SET status='F' WHERE status IN ('C','D','R','U') AND worker_id=$1`,
			workerID)
		if err != nil {
			return nil, fmt.Errorf("failed to fail tasks that were running when client %d crashed: %w", workerID, err)
		}
		_, err = tx.Exec(`UPDATE worker SET status='R' WHERE status IN ('O','I') AND worker_id=$1`,
			workerID)
		if err != nil {
			return nil, fmt.Errorf("failed to update worker %d status : %w", workerID, err)
		}

		log.Printf("✅ Worker %s already registered, sending back id %d", req.Name, workerID)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit worker registration: %v", err)
		return nil, fmt.Errorf("failed to commit worker registration: %w", err)
	}

	s.watchdog.WorkerRegistered(workerID, isPermanent)

	// **Trigger task assignment**
	s.triggerAssign()

	return &pb.WorkerId{WorkerId: workerID}, nil
}

// FetchWorkersForWatchdog reads workers and builds WorkerInfo list
func FetchWorkersForWatchdog(ctx context.Context, db *sql.DB) ([]watchdog.WorkerInfo, error) {
	// Example: adapt fields if needed
	rows, err := db.QueryContext(ctx, `
        SELECT w.worker_id, w.status, w.is_permanent, 
               COALESCE(t.active_tasks, 0) as active_tasks, 
               (SELECT MAX(t2.modified_at) FROM task t2 where t2.worker_id=w.worker_id AND t2.status in ('F','S')) as last_not_idle
        FROM worker w
        LEFT JOIN (
            SELECT worker_id, COUNT(*) as active_tasks
            FROM task
            WHERE status IN ('C', 'R') -- Accepted or Running
            GROUP BY worker_id
        ) t ON w.worker_id = t.worker_id
    `)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch workers: %w", err)
	}
	defer rows.Close()

	var workers []watchdog.WorkerInfo

	for rows.Next() {
		var workerID uint32
		var status string
		var isPermanent bool
		var activeTasks int
		var lastNotIdle *time.Time
		var lastNotIdleProxy sql.NullTime

		err := rows.Scan(&workerID, &status, &isPermanent, &activeTasks, &lastNotIdleProxy)
		if err != nil {
			return nil, fmt.Errorf("failed to scan worker row: %w", err)
		}
		if lastNotIdleProxy.Valid {
			lastNotIdle = &lastNotIdleProxy.Time
		}

		workers = append(workers, watchdog.WorkerInfo{
			WorkerID:    workerID,
			Status:      status,
			IsPermanent: isPermanent,
			ActiveTasks: activeTasks,
			LastNotIdle: lastNotIdle,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over worker rows: %w", err)
	}

	return workers, nil
}

func (s *taskQueueServer) CreateWorker(ctx context.Context, req *pb.WorkerRequest) (*pb.WorkerIds, error) {
	var workerDetailsList []*pb.WorkerDetails
	var jobs []Job

	// Begin a new database transaction
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback() // Ensure rollback if commit not reached

	// Loop to create the requested number of workers
	for req.Number > 0 {
		req.Number--

		var workerID uint32
		var workerName string
		var providerID uint32
		var regionName string
		var flavorName string
		var providerName string
		var cpu int32
		var memory float32

		// Insert a new worker row and retrieve its details joined with related tables
		err := tx.QueryRow(`WITH insertquery AS (
			INSERT INTO worker (step_id, worker_name, concurrency, flavor_id, region_id, is_permanent)
			VALUES (NULLIF($1,0), $5 || 'Worker' || CURRVAL('worker_worker_id_seq'), $2, $3, $4, FALSE)
			RETURNING worker_id, worker_name, region_id, flavor_id
		)
		SELECT iq.worker_id, iq.worker_name, r.provider_id, p.provider_name, r.region_name, f.flavor_name, f.cpu, f.mem
		FROM insertquery iq
		JOIN region r ON iq.region_id = r.region_id
		JOIN flavor f ON iq.flavor_id = f.flavor_id
		JOIN provider p ON r.provider_id = p.provider_id`,
			req.StepId, req.Concurrency, req.FlavorId, req.RegionId, s.cfg.Scitq.ServerName).Scan(
			&workerID, &workerName, &providerID, &providerName, &regionName, &flavorName, &cpu, &memory)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to register worker [step:%d, concurrency:%d, flavor:%d, region:%d]: %w",
				req.StepId,
				req.Concurrency,
				req.FlavorId,
				req.RegionId,
				err,
			)
		}

		var jobID uint32
		// Insert a new job related to this worker and get its ID
		tx.QueryRow("INSERT INTO job (worker_id, flavor_id, region_id, retry) VALUES ($1, $2, $3, $4) RETURNING job_id",
			workerID, req.FlavorId, req.RegionId, defaultJobRetry).Scan(&jobID)

		// Register the resource launch in the queue manager
		s.qm.RegisterLaunch(regionName, providerName, cpu, memory)

		// Append the new job to the jobs list for further processing
		jobs = append(jobs, Job{
			JobID:      jobID,
			WorkerID:   workerID,
			WorkerName: workerName,
			ProviderID: providerID,
			Region:     regionName,
			Flavor:     flavorName,
			Action:     'C', // 'C' could mean Create or similar action
			Retry:      defaultJobRetry,
			Timeout:    defaultJobTimeout,
		})

		workerDetailsList = append(workerDetailsList, &pb.WorkerDetails{
			WorkerId:   workerID,
			WorkerName: workerName,
			JobId:      jobID,
		})

		// Commit the transaction after each worker is created successfully
		if err := tx.Commit(); err != nil {
			log.Printf("⚠️ Failed to commit worker registration: %v", err)
			return nil, fmt.Errorf("failed to commit worker registration: %w", err)
		}
	}

	// Add all created jobs to the job queue
	for _, job := range jobs {
		s.addJob(job)
	}

	// Trigger job assignment process
	s.triggerAssign()

	// Return the details of all created workers
	return &pb.WorkerIds{
		WorkersDetails: workerDetailsList,
	}, nil
}

func (s *taskQueueServer) GetWorkerStatuses(ctx context.Context, req *pb.WorkerStatusRequest) (*pb.WorkerStatusResponse, error) {
	var statuses []*pb.WorkerStatus

	for _, workerID := range req.WorkerIds {
		var status string

		err := s.db.QueryRow("SELECT status FROM worker WHERE worker_id = $1", workerID).Scan(&status)
		if err != nil {
			log.Printf("⚠️ Error retrieving status for worker_id %d: %v", workerID, err)
			status = "unknown"
		}

		statuses = append(statuses, &pb.WorkerStatus{
			WorkerId: workerID,
			Status:   status,
		})
	}

	return &pb.WorkerStatusResponse{Statuses: statuses}, nil
}

func (s *taskQueueServer) UpdateWorker(ctx context.Context, req *pb.WorkerUpdateRequest) (*pb.Ack, error) {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	query := "UPDATE worker SET "
	args := []interface{}{}
	sets := []string{}

	i := 1

	if req.ProviderId != nil {
		sets = append(sets, fmt.Sprintf("provider_id=$%d", i))
		args = append(args, req.GetProviderId())
		i++
	}
	if req.FlavorId != nil {
		sets = append(sets, fmt.Sprintf("flavor_id=$%d", i))
		args = append(args, req.GetFlavorId())
		i++
	}
	if req.RegionId != nil {
		sets = append(sets, fmt.Sprintf("region_id=$%d", i))
		args = append(args, req.GetRegionId())
		i++
	}
	if req.Concurrency != nil {
		sets = append(sets, fmt.Sprintf("concurrency=$%d", i))
		args = append(args, req.GetConcurrency())
		i++
	}
	if req.Prefetch != nil {
		sets = append(sets, fmt.Sprintf("prefetch=$%d", i))
		args = append(args, req.GetPrefetch())
		i++
	}
	if req.StepId != nil {
		sets = append(sets, fmt.Sprintf("step_id=$%d", i))
		args = append(args, req.GetStepId())
		i++
	}

	if len(sets) == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("no fields provided to update")
	}

	query += strings.Join(sets, ", ") + fmt.Sprintf(" WHERE worker_id=$%d", i)
	args = append(args, req.GetWorkerId())

	_, err = tx.Exec(query, args...)
	if err != nil {
		log.Printf("⚠️ Failed to execute update: %v", err)
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update worker: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit transaction: %v", err)
		return &pb.Ack{Success: false}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	s.triggerAssign()
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) DeleteWorker(ctx context.Context, req *pb.WorkerId) (*pb.JobId, error) {
	var job Job

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	var workerName string
	var providerId int
	var regionId int
	var is_permanent bool
	var statusStr string
	err = tx.QueryRow(`SELECT w.worker_name, r.provider_id, r.region_id, w.is_permanent, w.status FROM worker w
	JOIN region r ON w.region_id=r.region_id
	WHERE w.worker_id=$1`, req.WorkerId).Scan(&workerName, &providerId, &regionId, &is_permanent, &statusStr)
	status := rune(statusStr[0])
	if err != nil {
		return nil, fmt.Errorf("failed to find worker %d: %w", req.WorkerId, err)
	}

	if !is_permanent {
		var jobId uint32
		err = tx.QueryRow("INSERT INTO job (worker_id,action,region_id,retry) VALUES ($1,'D',$2,$3) RETURNING job_id",
			req.WorkerId, regionId, defaultJobRetry).Scan(&jobId)
		if err != nil {
			return nil, fmt.Errorf("failed to create job for worker %d: %w", req.WorkerId, err)
		}
		job = Job{
			JobID:      jobId,
			WorkerID:   req.WorkerId,
			WorkerName: workerName,
			ProviderID: uint32(providerId),
			Action:     'D',
			Retry:      defaultJobRetry,
			Timeout:    defaultJobTimeout,
		}

		s.addJob(job)

	} else {
		if status == 'O' || status == 'I' {
			_, err = tx.Exec("DELETE FROM worker WHERE worker_id=$1", req.WorkerId)
			if err != nil {
				return nil, fmt.Errorf("failed to delete worker %d: %w", req.WorkerId, err)
			}
		} else {
			return nil, fmt.Errorf("will not delete permanent worker %d with status %c", req.WorkerId, status)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit worker deletion: %v", err)
		return nil, fmt.Errorf("failed to commit worker deletion: %w", err)
	}

	return &pb.JobId{JobId: job.JobID}, nil
}

func (s *taskQueueServer) ListJobs(ctx context.Context, req *pb.ListJobsRequest) (*pb.JobsList, error) {
	var jobs []*pb.Job

	query := `
		SELECT 
			job_id,
			status,
			COALESCE(flavor_id, 0) AS flavor_id,
			retry,
			COALESCE(worker_id, 0) AS worker_id,
			action,
			created_at,
			modified_at,
			progression,
			COALESCE(log, '')  -- pour éviter les NULL
		FROM job
		ORDER BY job_id;
	`

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(query)
	if err != nil {
		log.Printf("⚠️ Failed to list jobs: %v", err)
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var job pb.Job
		err := rows.Scan(
			&job.JobId,
			&job.Status,
			&job.FlavorId,
			&job.Retry,
			&job.WorkerId,
			&job.Action,
			&job.CreatedAt,
			&job.ModifiedAt,
			&job.Progression,
			&job.Log,
		)
		if err != nil {
			log.Printf("⚠️ Failed to scan job: %v", err)
			continue
		}
		jobs = append(jobs, &job)
	}

	if err := rows.Err(); err != nil {
		log.Printf("⚠️ Error iterating jobs: %v", err)
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit job listing: %v", err)
		return nil, fmt.Errorf("failed to commit job listing: %w", err)
	}

	return &pb.JobsList{Jobs: jobs}, nil
}

func (s *taskQueueServer) GetJobStatuses(ctx context.Context, req *pb.JobStatusRequest) (*pb.JobStatusResponse, error) {
	var statuses []*pb.JobStatus

	for _, jobID := range req.JobIds {
		var status string
		var progression uint32

		err := s.db.QueryRow("SELECT status, progression FROM job WHERE job_id = $1", jobID).Scan(&status, &progression)
		if err != nil {
			log.Printf("⚠️ Error retrieving status for worker_id %d: %v", jobID, err)
			status = "unknown"
		}

		statuses = append(statuses, &pb.JobStatus{
			JobId:       jobID,
			Status:      status,
			Progression: progression,
		})
	}

	return &pb.JobStatusResponse{Statuses: statuses}, nil
}

func (s *taskQueueServer) DeleteJob(ctx context.Context, req *pb.JobId) (*pb.Ack, error) {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM job
		WHERE job_id = $1
	`, req.JobId)

	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete job: %w", err)
	}

	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) UpdateWorkerStatus(ctx context.Context, req *pb.WorkerStatus) (*pb.Ack, error) {
	_, err := s.db.Exec("UPDATE worker SET status = $1 WHERE worker_id = $2", req.Status, req.WorkerId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update worker status: %w", err)
	}
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.TaskList, error) {
	var tasks []*pb.Task
	var rows *sql.Rows
	var err error

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// **Filter by status and worker if provided**
	if req.StatusFilter != nil && *req.StatusFilter != "" && req.WorkerIdFilter != nil {
		rows, err = tx.Query(`SELECT task_id, task_name, command, container, status, worker_id, step_id FROM task WHERE status = $1 AND worker_id = $2 ORDER BY task_id`, *req.StatusFilter, *req.WorkerIdFilter)
	} else if req.StatusFilter != nil && *req.StatusFilter != "" {
		rows, err = tx.Query(`SELECT task_id, task_name, command, container, status, worker_id, step_id FROM task WHERE status = $1 ORDER BY task_id`, *req.StatusFilter)
	} else if req.WorkerIdFilter != nil {
		rows, err = tx.Query(`SELECT task_id, task_name, command, container, status, worker_id, step_id FROM task WHERE worker_id = $1 ORDER BY task_id`, *req.WorkerIdFilter)
	} else {
		rows, err = tx.Query(`SELECT task_id, task_name, command, container, status, worker_id, step_id FROM task ORDER BY task_id`)
	}

	if err != nil {
		log.Printf("⚠️ Failed to list tasks: %v", err)
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var task pb.Task
		var taskName sql.NullString
		err := rows.Scan(&task.TaskId, &taskName, &task.Command, &task.Container, &task.Status, &task.WorkerId, &task.StepId)
		if err != nil {
			log.Printf("⚠️ Failed to scan task: %v", err)
			continue
		}
		if taskName.Valid {
			task.TaskName = &taskName.String
		}
		tasks = append(tasks, &task)
	}

	if err := rows.Err(); err != nil {
		log.Printf("⚠️ Error iterating tasks: %v", err)
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit task listing: %v", err)
		return nil, fmt.Errorf("failed to commit task listing: %w", err)
	}

	return &pb.TaskList{Tasks: tasks}, nil
}

func (s *taskQueueServer) PingAndTakeNewTasks(ctx context.Context, req *pb.PingAndGetNewTasksRequest) (*pb.TaskListAndOther, error) {
	var (
		tasks          []*pb.Task
		concurrency    uint32
		input          pq.StringArray
		resource       pq.StringArray
		shell          sql.NullString
		taskUpdateList = make(map[uint32]*pb.TaskUpdate)
		activeTaskIDs  []uint32
	)

	// 1️⃣ Fetch concurrency
	err := s.db.QueryRow(`
		SELECT concurrency FROM worker WHERE worker_id = $1
	`, req.WorkerId).Scan(&concurrency)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("worker ID %d not found", req.WorkerId)
		}
		return nil, fmt.Errorf("failed to fetch worker concurrency for worker %d: %w", req.WorkerId, err)
	}

	// 2️⃣ Fetch tasks (assigned and running)
	rows, err := s.db.Query(`
		SELECT task_id, command, shell, container, container_options,
			input, resource, output, retry, is_final, uses_cache,
			download_timeout, running_timeout, upload_timeout,
			status
		FROM task
		WHERE worker_id = $1
		  AND COALESCE(step_id, 0) = COALESCE((SELECT step_id FROM worker WHERE worker_id = $1), 0)
		  AND status IN ('A', 'C', 'D', 'R', 'U', 'V')
	`, req.WorkerId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch assigned/running tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var task pb.Task
		var status string

		if err := rows.Scan(&task.TaskId, &task.Command, &shell, &task.Container, &task.ContainerOptions,
			&input, &resource, &task.Output, &task.Retry, &task.IsFinal, &task.UsesCache,
			&task.DownloadTimeout, &task.RunningTimeout, &task.UploadTimeout, &status); err != nil {
			log.Printf("⚠️ Task decode error: %v", err)
			continue
		}
		task.Input = []string(input)
		task.Resource = []string(resource)
		if shell.Valid {
			task.Shell = proto.String(shell.String)
		}

		if status == "A" {
			// Only send full task if assignable
			tasks = append(tasks, &task)
		} else {
			// Just track active running task IDs
			activeTaskIDs = append(activeTaskIDs, task.TaskId)
		}

		// Add weight if available (for both assigned and active tasks)
		if val, ok := s.workerWeightMemory.Load(req.WorkerId); ok {
			taskMap := val.(*sync.Map)
			if weightVal, ok := taskMap.Load(task.TaskId); ok {
				weight, ok := weightVal.(float64)
				if ok {
					taskUpdateList[task.TaskId] = &pb.TaskUpdate{Weight: weight}
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating through tasks: %w", err)
	}

	// Clean up the worker's weight memory
	if val, ok := s.workerWeightMemory.Load(req.WorkerId); ok {
		taskMap := val.(*sync.Map)
		activeSet := make(map[uint32]struct{}, len(activeTaskIDs))
		for _, id := range activeTaskIDs {
			activeSet[id] = struct{}{}
		}
		for _, task := range tasks {
			activeSet[task.TaskId] = struct{}{}
		}

		var toDelete []uint32
		taskMap.Range(func(taskIDRaw, _ any) bool {
			taskID := taskIDRaw.(uint32)
			if _, stillActive := activeSet[taskID]; !stillActive {
				toDelete = append(toDelete, taskID)
			}
			return true
		})
		for _, taskID := range toDelete {
			taskMap.Delete(taskID)
			log.Printf("⚠️ Worker %d: cleaned task %d from weight memory (no longer active)", req.WorkerId, taskID)
		}
	}

	s.watchdog.WorkerPinged(req.WorkerId)

	if req.Stats != nil {
		s.workerStats.Store(req.WorkerId, req.Stats)
	} else {
		log.Printf("⚠️ Worker %d did not send stats", req.WorkerId)
	}

	return &pb.TaskListAndOther{
		Tasks:       tasks,
		ActiveTasks: activeTaskIDs,
		Concurrency: concurrency,
		Updates:     &pb.TaskUpdateList{Updates: taskUpdateList},
	}, nil
}

func (s *taskQueueServer) ListWorkers(ctx context.Context, req *pb.ListWorkersRequest) (*pb.WorkersList, error) {
	var workers []*pb.Worker
	var rows *sql.Rows
	var err error

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// **Filter by status if provided**
	rows, err = tx.Query(`SELECT 
		w.worker_id, 
		worker_name, 
		concurrency, 
		prefetch, 
		w.status, 
		COALESCE(w.ipv4::text, '') AS ipv4, 
		COALESCE(w.ipv6::text, '') AS ipv6, 
		COALESCE(r.region_name, ''), 
		COALESCE(p.provider_name||'.'||p.config_name, ''),
		COALESCE(f.flavor_name, '')
	FROM worker w
	LEFT JOIN region r ON r.region_id=w.region_id
	LEFT JOIN provider p ON r.provider_id=p.provider_id
	LEFT JOIN flavor f ON f.flavor_id=w.flavor_id
	ORDER BY worker_id`)

	if err != nil {
		log.Printf("⚠️ Failed to list workers: %v", err)
		return nil, fmt.Errorf("failed to list workers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var worker pb.Worker
		err := rows.Scan(&worker.WorkerId, &worker.Name, &worker.Concurrency, &worker.Prefetch, &worker.Status,
			&worker.Ipv4, &worker.Ipv6, &worker.Region, &worker.Provider, &worker.Flavor)
		if err != nil {
			log.Printf("⚠️ Failed to scan task: %v", err)
			continue
		}
		workers = append(workers, &worker)
	}

	if err := rows.Err(); err != nil {
		log.Printf("⚠️ Error iterating workers: %v", err)
		return nil, fmt.Errorf("error iterating workers: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit worker listing: %v", err)
		return nil, fmt.Errorf("failed to commit worker listing: %w", err)
	}

	return &pb.WorkersList{Workers: workers}, nil
}

func (s *taskQueueServer) ListFlavors(ctx context.Context, req *pb.ListFlavorsRequest) (*pb.FlavorsList, error) {
	var flavors []*pb.Flavor

	conditions, err := protofilter.ParseProtofilter(req.Filter)
	if err != nil {
		log.Printf("⚠️ Failed to parse filter: %v", err)
		return nil, fmt.Errorf("failed to parse filter: %w", err)
	}

	baseQuery := `
	SELECT 
		f.flavor_id,
		f.provider_id,
		f.flavor_name,
		p.provider_name||'.'||p.config_name as provider,
		f.cpu,
		f.mem,
		f.disk,
		f.bandwidth,
		f.gpu,
		f.gpumem,
		f.has_gpu,
		f.has_quick_disks,
		r.region_id,
		r.region_name,
		fr.eviction,
		fr.cost
	FROM flavor f
	JOIN flavor_region fr ON f.flavor_id = fr.flavor_id
	JOIN region r ON fr.region_id = r.region_id
	JOIN provider p ON p.provider_id = f.provider_id`

	if len(conditions) > 0 {
		baseQuery += "\nWHERE " + strings.Join(conditions, " AND ")
	}
	baseQuery += fmt.Sprintf("\nORDER BY fr.cost LIMIT %d;", req.Limit)

	//log.Printf("Final query: %s", baseQuery)

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(baseQuery)
	if err != nil {
		log.Printf("⚠️ Failed to list flavors: %v", err)
		return nil, fmt.Errorf("failed to list flavors: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var flavor pb.Flavor
		err := rows.Scan(
			&flavor.FlavorId,
			&flavor.ProviderId,
			&flavor.FlavorName,
			&flavor.Provider,
			&flavor.Cpu,
			&flavor.Mem,
			&flavor.Disk,
			&flavor.Bandwidth,
			&flavor.Gpu,
			&flavor.Gpumem,
			&flavor.HasGpu,
			&flavor.HasQuickDisks,
			&flavor.RegionId,
			&flavor.Region,
			&flavor.Eviction,
			&flavor.Cost,
		)
		if err != nil {
			log.Printf("⚠️ Failed to scan flavor: %v", err)
			continue
		}
		flavors = append(flavors, &flavor)
	}

	if err := rows.Err(); err != nil {
		log.Printf("⚠️ Error iterating flavors: %v", err)
		return nil, fmt.Errorf("error iterating flavors: %w", err)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit flavor listing: %v", err)
		return nil, fmt.Errorf("failed to commit flavor listing: %w", err)
	}

	return &pb.FlavorsList{Flavors: flavors}, nil
}

func generateJWT(userID uint32, username, secret string) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"exp":      time.Now().Add(365 * 24 * time.Hour).Unix(), // ~ immortal
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func CheckPassword(plaintext, hashed string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plaintext)) == nil
}

func (s *taskQueueServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	// Example: Basic username/password validation (replace this with proper DB lookup and hashing)
	var userId uint32
	var hashedPassword string

	log.Printf("Login attempt for user %s", req.Username)
	err := s.db.QueryRow(`SELECT user_id, password FROM scitq_user WHERE username = $1`, req.Username).Scan(&userId, &hashedPassword)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
	}

	if !CheckPassword(req.Password, hashedPassword) {
		return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
	}

	// Generate token
	tokenStr, err := generateJWT(userId, req.Username, s.cfg.Scitq.JwtSecret)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate token")
	}

	// Store token in the database
	log.Printf("Storing token for user %d", userId)
	_, err = s.db.Exec(`INSERT INTO scitq_user_session (user_id, session_id, expires_at) VALUES ($1, $2, now()+'365 days')`, userId, tokenStr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to store token: %v", err)
	}

	return &pb.LoginResponse{Token: tokenStr}, nil
}

// NewLogin returns an HTTP handler function to process login requests.
func NewLogin(s *taskQueueServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var loginRequest pb.LoginRequest

		// Decode the JSON login request body
		if err := json.NewDecoder(r.Body).Decode(&loginRequest); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Call the login method on the server with the request context and login details
		loginResponse, err := s.Login(r.Context(), &loginRequest)
		if err != nil {
			http.Error(w, "Authentication failed", http.StatusUnauthorized)
			return
		}

		// Extract the JWT token string from the login response
		tokenStr := loginResponse.GetToken()

		// Set a secure HTTP-only cookie with the session token
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    tokenStr,
			HttpOnly: true,
			Secure:   false, // Set to true in production with HTTPS
			SameSite: http.SameSiteLaxMode,
			MaxAge:   3600 * 24, // 1 day expiration
		})

		w.WriteHeader(http.StatusOK)
	}
}

// fetchCookie returns an HTTP handler to retrieve the session token cookie.
func fetchCookie() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Retrieve the session_token cookie
		cookie, err := r.Cookie("session_token")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Respond with the raw token value in JSON format
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"token": cookie.Value,
		})
	}
}

// Logout invalidates the user session by deleting it from the database.
func (s *taskQueueServer) Logout(ctx context.Context, req *pb.Token) (*pb.Ack, error) {
	// Delete the session corresponding to the provided token
	_, err := s.db.Exec("DELETE FROM scitq_user_session WHERE session_id = $1", req.Token)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete session with token %s: %w", req.Token, err)
	}

	return &pb.Ack{Success: true}, nil
}

// LogoutHandler clears the session_token cookie to log out the user.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	// Overwrite the cookie with expired max age to remove it from client
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/", // Must match original cookie path
		HttpOnly: true,
		Secure:   false, // Set to true with HTTPS
		MaxAge:   -1,    // Immediate deletion
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusOK)
}

// parseJWT verifies and parses the JWT token string using the given secret key.
func parseJWT(tokenStr string, secretKey string) (jwt.MapClaims, error) {
	// Parse the JWT token with validation of the signing method
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// Ensure the signing method is HMAC-SHA256
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secretKey), nil
	})

	// Return error if token parsing fails
	if err != nil {
		log.Printf("Failed to parse token: %v", err)
		return nil, err
	}

	// Assert claims type and token validity
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		log.Printf("Invalid token")
		return nil, fmt.Errorf("invalid token")
	}

	return claims, nil
}

// fetchWorkerTokenHandler returns an HTTP handler that provides the worker token.
func fetchWorkerTokenHandler(s *taskQueueServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("➡️ Calling /WorkerToken")

		var tokenStr string

		// Attempt to retrieve the session token from cookie
		cookie, err := r.Cookie("session_token")
		if err == nil && cookie.Value != "" {
			tokenStr = cookie.Value
			log.Printf("✅ Token retrieved from cookie")
		} else {
			// If no cookie, try to get token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
				log.Printf("✅ Token retrieved from Authorization header")
			} else {
				log.Println("❌ No token provided (neither cookie nor header)")
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// Verify and parse the JWT token
		claims, err := parseJWT(tokenStr, s.cfg.Scitq.JwtSecret)
		if err != nil {
			log.Println("❌ Invalid or expired JWT token")
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}
		log.Printf("✅ JWT decoded: %v", claims)

		// Retrieve the pre-configured worker token from server config
		workerToken := s.cfg.Scitq.WorkerToken
		log.Printf("✅ Worker token retrieved: %s", workerToken)

		// Respond with the worker token as JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"worker_token": workerToken})
	}
}

// CreateUser creates a new user in the system, requires admin privileges.
func (s *taskQueueServer) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.UserId, error) {
	// Extract user from context for logging and permission check
	user := GetUserFromContext(ctx)

	if user != nil {
		log.Printf("User '%s' (ID: %d) admin status: %v", user.Username, user.UserID, user.IsAdmin)
	} else {
		log.Println("No user found in context")
	}

	// Verify admin privileges
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}

	// Hash the password using bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}

	var userID uint32
	// Insert new user record into database
	err = s.db.QueryRow(
		`INSERT INTO scitq_user (username, password, email, is_admin)
		 VALUES ($1, $2, $3, $4) RETURNING user_id`,
		req.Username, hashedPassword, req.Email, req.IsAdmin,
	).Scan(&userID)

	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") {
			return nil, status.Error(codes.AlreadyExists, "username already exists")
		}
		return nil, status.Error(codes.Internal, "failed to create user")
	}

	// Return the newly created user's ID
	return &pb.UserId{UserId: userID}, nil
}

// ListUsers returns a list of all users in the system.
func (s *taskQueueServer) ListUsers(ctx context.Context, _ *emptypb.Empty) (*pb.UsersList, error) {
	rows, err := s.db.Query("SELECT user_id, username, email, is_admin FROM scitq_user")
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []*pb.User
	for rows.Next() {
		var user pb.User
		if err := rows.Scan(&user.UserId, &user.Username, &user.Email, &user.IsAdmin); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, &user)
	}

	return &pb.UsersList{Users: users}, nil
}

// DeleteUser deletes a user and all their associated sessions from the system.
// Requires admin privileges.
func (s *taskQueueServer) DeleteUser(ctx context.Context, req *pb.UserId) (*pb.Ack, error) {
	// Check if the caller has admin privileges
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}

	// Delete all sessions linked to the specified user ID
	_, err := s.db.Exec("DELETE FROM scitq_user_session WHERE user_id = $1", req.UserId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete sessions for user %d: %w", req.UserId, err)
	}

	// Delete the user record from the database
	_, err = s.db.Exec("DELETE FROM scitq_user WHERE user_id = $1", req.UserId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete user %d: %w", req.UserId, err)
	}

	// Return success acknowledgement
	return &pb.Ack{Success: true}, nil
}

func joinWithComma(fields []string) string {
	result := ""
	for i, field := range fields {
		if i > 0 {
			result += ", "
		}
		result += field
	}
	return result
}

func (s *taskQueueServer) UpdateUser(ctx context.Context, req *pb.User) (*pb.Ack, error) {
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}

	query := "UPDATE scitq_user SET"
	args := []interface{}{}
	set := []string{}
	index := 1

	if req.Username != nil {
		set = append(set, fmt.Sprintf("username=$%d", index))
		args = append(args, *req.Username)
		index++
	}
	if req.Email != nil {
		set = append(set, fmt.Sprintf("email=$%d", index))
		args = append(args, *req.Email)
		index++
	}
	if req.IsAdmin != nil {
		set = append(set, fmt.Sprintf("is_admin=$%d", index))
		args = append(args, *req.IsAdmin)
		index++
	}

	if len(set) == 0 {
		return &pb.Ack{Success: false}, status.Error(codes.InvalidArgument, "no fields to update")
	}

	query += " " + fmt.Sprintf("%s WHERE user_id=$%d",
		joinWithComma(set), index)
	args = append(args, req.UserId)

	_, err := s.db.Exec(query, args...)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update user %d: %w", req.UserId, err)
	}
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) ChangePassword(ctx context.Context, req *pb.ChangePasswordRequest) (*pb.Ack, error) {
	var currentHash string
	err := s.db.QueryRow("SELECT password FROM scitq_user WHERE username=$1", req.Username).Scan(&currentHash)
	if err != nil || !CheckPassword(req.OldPassword, currentHash) {
		return &pb.Ack{Success: false}, fmt.Errorf("incorrect credentials")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to hash new password: %w", err)
	}

	_, err = s.db.Exec("UPDATE scitq_user SET password=$1 WHERE username=$2", hash, req.Username)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update password: %w", err)
	}

	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) ListRecruiters(ctx context.Context, req *pb.RecruiterFilter) (*pb.RecruiterList, error) {
	query := `SELECT step_id, rank, protofilter,
		worker_concurrency, worker_prefetch, maximum_workers, rounds, timeout
		FROM recruiter
		ORDER BY step_id, rank`

	args := []interface{}{}
	if req.StepId != nil {
		query += " WHERE step_id = $1"
		args = append(args, *req.StepId)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query recruiters: %w", err)
	}
	defer rows.Close()

	var recruiters []*pb.Recruiter
	for rows.Next() {
		var recruiter pb.Recruiter
		if err := rows.Scan(
			&recruiter.StepId, &recruiter.Rank, &recruiter.Protofilter,
			&recruiter.Concurrency, &recruiter.Prefetch, &recruiter.MaxWorkers, &recruiter.Rounds, &recruiter.Timeout); err != nil {
			return nil, fmt.Errorf("failed to scan recruiter: %w", err)
		}
		recruiters = append(recruiters, &recruiter)
	}

	return &pb.RecruiterList{Recruiters: recruiters}, nil
}

func (s *taskQueueServer) CreateRecruiter(ctx context.Context, req *pb.Recruiter) (*pb.Ack, error) {
	var err error
	// Insert with embedded subqueries for provider_id and region_id
	if req.MaxWorkers == nil {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO recruiter (
				step_id, rank, protofilter,
				worker_concurrency, worker_prefetch, rounds, timeout
			) VALUES (
				$1, $2, $3,
				$4, $5, $6, $7
			)
		`,
			req.StepId, req.Rank, req.Protofilter,
			req.Concurrency, req.Prefetch, req.Rounds, req.Timeout,
		)
	} else {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO recruiter (
				step_id, rank, protofilter,
				worker_concurrency, worker_prefetch, maximum_workers, rounds, timeout
			) VALUES (
				$1, $2, $3,
				$4, $5, $6, $7, $8
			)
		`,
			req.StepId, req.Rank, req.Protofilter,
			req.Concurrency, req.Prefetch, *req.MaxWorkers, req.Rounds, req.Timeout,
		)
	}

	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to insert recruiter: %w", err)
	}

	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) DeleteRecruiter(ctx context.Context, req *pb.RecruiterId) (*pb.Ack, error) {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM recruiter
		WHERE step_id = $1 AND rank = $2
	`, req.StepId, req.Rank)

	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete recruiter: %w", err)
	}

	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) UpdateRecruiter(ctx context.Context, req *pb.RecruiterUpdate) (*pb.Ack, error) {
	// Base of the update query
	q := "UPDATE recruiter SET "
	args := []any{}
	clauses := []string{}

	if req.Protofilter != nil {
		clauses = append(clauses, fmt.Sprintf("protofilter = $%d", len(args)+1))
		args = append(args, *req.Protofilter)
	}
	if req.Concurrency != nil {
		clauses = append(clauses, fmt.Sprintf("worker_concurrency = $%d", len(args)+1))
		args = append(args, *req.Concurrency)
	}
	if req.Prefetch != nil {
		clauses = append(clauses, fmt.Sprintf("worker_prefetch = $%d", len(args)+1))
		args = append(args, *req.Prefetch)
	}
	if req.MaxWorkers != nil {
		clauses = append(clauses, fmt.Sprintf("maximum_workers = $%d", len(args)+1))
		args = append(args, *req.MaxWorkers)
	}
	if req.Rounds != nil {
		clauses = append(clauses, fmt.Sprintf("rounds = $%d", len(args)+1))
		args = append(args, *req.Rounds)
	}
	if req.Timeout != nil {
		clauses = append(clauses, fmt.Sprintf("timeout = $%d", len(args)+1))
		args = append(args, *req.Timeout)
	}

	if len(clauses) == 0 {
		return &pb.Ack{Success: false}, fmt.Errorf("no fields to update")
	}

	// Finalize query with WHERE clause
	q += strings.Join(clauses, ", ") + fmt.Sprintf(" WHERE step_id = $%d AND rank = $%d", len(args)+1, len(args)+2)
	args = append(args, req.StepId, req.Rank)

	_, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update recruiter: %w", err)
	}
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) ListWorkflows(ctx context.Context, req *pb.WorkflowFilter) (*pb.WorkflowList, error) {
	query := `SELECT workflow_id, workflow_name, run_strategy, maximum_workers FROM workflow`
	var rows *sql.Rows
	var err error
	if req.NameLike != nil {
		rows, err = s.db.Query(query+" WHERE workflow_name ILIKE $1", req.NameLike)
	} else {
		rows, err = s.db.Query(query)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query workflows: %w", err)
	}
	defer rows.Close()

	var workflows []*pb.Workflow
	for rows.Next() {
		var wf pb.Workflow
		if err := rows.Scan(&wf.WorkflowId, &wf.Name, &wf.RunStrategy, &wf.MaximumWorkers); err != nil {
			return nil, fmt.Errorf("failed to scan workflow: %w", err)
		}
		workflows = append(workflows, &wf)
	}
	return &pb.WorkflowList{Workflows: workflows}, nil
}

func (s *taskQueueServer) CreateWorkflow(ctx context.Context, req *pb.WorkflowRequest) (*pb.WorkflowId, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("workflow name is required")
	}

	if req.RunStrategy == nil || *req.RunStrategy == "" {
		defaultRunStrategy := "B"
		req.RunStrategy = &defaultRunStrategy
	}

	var workflowID uint32
	var err error
	if req.MaximumWorkers == nil {
		err = s.db.QueryRow(`
			INSERT INTO workflow (workflow_name, run_strategy)
			VALUES ($1, $2)
			RETURNING workflow_id
		`, req.Name, req.RunStrategy).Scan(&workflowID)
	} else {
		err = s.db.QueryRow(`
		INSERT INTO workflow (workflow_name, run_strategy, maximum_workers)
		VALUES ($1, $2, $3)
		RETURNING workflow_id
	`, req.Name, req.RunStrategy, *req.MaximumWorkers).Scan(&workflowID)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to insert workflow: %w", err)
	}

	return &pb.WorkflowId{WorkflowId: workflowID}, nil
}

func (s *taskQueueServer) DeleteWorkflow(ctx context.Context, req *pb.WorkflowId) (*pb.Ack, error) {
	_, err := s.db.Exec(`DELETE FROM workflow WHERE workflow_id = $1`, req.WorkflowId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete workflow: %w", err)
	}
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) ListSteps(ctx context.Context, req *pb.WorkflowId) (*pb.StepList, error) {
	rows, err := s.db.Query(`SELECT step_id, workflow_name, step_name FROM step s 
		JOIN workflow w ON w.workflow_id=s.workflow_id WHERE s.workflow_id = $1`, req.WorkflowId)
	if err != nil {
		return nil, fmt.Errorf("failed to query steps: %w", err)
	}
	defer rows.Close()

	var steps []*pb.Step
	for rows.Next() {
		var st pb.Step
		if err := rows.Scan(&st.StepId, &st.WorkflowName, &st.Name); err != nil {
			return nil, fmt.Errorf("failed to scan step: %w", err)
		}
		steps = append(steps, &st)
	}
	return &pb.StepList{Steps: steps}, nil
}

func (s *taskQueueServer) CreateStep(ctx context.Context, req *pb.StepRequest) (*pb.StepId, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("step name is required")
	}

	if req.WorkflowId != nil && *req.WorkflowId != 0 {
		var stepID uint32
		err := s.db.QueryRow(`
			INSERT INTO step (step_name, workflow_id)
			VALUES ($1, $2)
			RETURNING step_id
		`, req.Name, *req.WorkflowId).Scan(&stepID)
		if err != nil {
			return nil, fmt.Errorf("failed to insert step with workflow id %d: %w", *req.WorkflowId, err)
		}
		return &pb.StepId{StepId: stepID}, nil

	} else if req.WorkflowName != nil {
		var stepID uint32
		err := s.db.QueryRow(`
			WITH wf AS (
				SELECT w.workflow_id FROM workflow w WHERE w.workflow_name = $1
			)
			INSERT INTO step (step_name, workflow_id)
			SELECT $2, wf.workflow_id FROM wf
			RETURNING step_id
		`, *req.WorkflowName, req.Name).Scan(&stepID)

		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow with name %q not found", *req.WorkflowName)
		} else if err != nil {
			return nil, fmt.Errorf("failed to query workflow %q: %w", *req.WorkflowName, err)
		}
		return &pb.StepId{StepId: stepID}, nil
	}

	return nil, fmt.Errorf("either workflow_id or workflow_name must be provided")
}

func (s *taskQueueServer) DeleteStep(ctx context.Context, req *pb.StepId) (*pb.Ack, error) {
	_, err := s.db.Exec(`DELETE FROM step WHERE step_id = $1`, req.StepId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete step: %w", err)
	}
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) GetWorkerStats(ctx context.Context, req *pb.GetWorkerStatsRequest) (*pb.GetWorkerStatsResponse, error) {
	resp := &pb.GetWorkerStatsResponse{
		WorkerStats: make(map[uint32]*pb.WorkerStats),
	}

	for _, workerID := range req.WorkerIds {
		if v, ok := s.workerStats.Load(workerID); ok {
			if stats, ok := v.(*pb.WorkerStats); ok {
				resp.WorkerStats[workerID] = stats
			}
		}
	}

	return resp, nil
}

func (s *taskQueueServer) FetchList(ctx context.Context, req *pb.FetchListRequest) (*pb.FetchListResponse, error) {
	files, err := fetch.List(DefaultRcloneConfig, req.Uri)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fetch list failed: %v", err)
	}

	return &pb.FetchListResponse{Files: files}, nil
}

func (s *taskQueueServer) GetWorkspaceRoot(ctx context.Context, req *taskqueuepb.WorkspaceRootRequest) (*taskqueuepb.WorkspaceRootResponse, error) {
	providerName := req.GetProvider()
	region := req.GetRegion()

	provider, ok := s.providerConfig[providerName]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown provider: %q", providerName)
	}

	root, ok := provider.GetWorkspaceRoot(region)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no workspace root for region %q in provider %q", region, providerName)
	}

	return &taskqueuepb.WorkspaceRootResponse{
		RootUri: root,
	}, nil
}

func (s *taskQueueServer) RegisterSpecifications(ctx context.Context, req *taskqueuepb.ResourceSpec) (*taskqueuepb.Ack, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Step 1: get the worker
	var currentFlavorId sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT flavor_id FROM worker WHERE worker_id = $1`, req.WorkerId).Scan(&currentFlavorId)
	if err == sql.ErrNoRows {
		return &taskqueuepb.Ack{Success: false}, fmt.Errorf("worker %s not found", req.WorkerId)
	} else if err != nil {
		return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to fetch worker: %w", err)
	}

	// Step 2: get the local provider_id
	var providerId uint32
	err = tx.QueryRowContext(ctx, `SELECT provider_id FROM provider WHERE provider_name = 'local' AND config_name = 'local'`).Scan(&providerId)
	if err != nil {
		return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to get local provider: %w", err)
	}

	// Step 3: if no flavor, create one and attach to worker
	if !currentFlavorId.Valid {
		var newFlavorId uint32
		err = tx.QueryRowContext(ctx, `
			INSERT INTO flavor (provider_id, flavor_name, cpu, mem, disk)
			VALUES ($1, (SELECT worker_name FROM worker WHERE worker_id=$2), $3, $4, $5)
			RETURNING flavor_id
		`, providerId, req.WorkerId, req.Cpu, req.Mem, req.Disk).Scan(&newFlavorId)
		if err != nil {
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to create flavor: %w", err)
		}

		// Fetch region_id for 'local' region of local provider
		var localRegionId uint32
		err = tx.QueryRowContext(ctx, `
			SELECT region_id FROM region WHERE region_name = 'local' AND provider_id = $1
		`, providerId).Scan(&localRegionId)
		if err != nil {
			log.Printf("failed to get local region_id: %v", err)
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to get local region_id: %w", err)
		}

		// Now insert flavor_region with known region_id
		_, err = tx.ExecContext(ctx, `
			INSERT INTO flavor_region (flavor_id, region_id, cost)
			VALUES ($1, $2, 0.0)
			ON CONFLICT DO NOTHING
		`, newFlavorId, localRegionId)
		if err != nil {
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to create flavor region: %w", err)
		}

		// Now add the new region to the worker
		_, err = tx.ExecContext(ctx, `
			UPDATE worker SET region_id=$1,recyclable_scope='G' WHERE worker_id=$2
		`, localRegionId, req.WorkerId)
		if err != nil {
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to associate worker with flavor region: %w", err)
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE worker SET flavor_id = $1 WHERE worker_id = $2
		`, newFlavorId, req.WorkerId)
		if err != nil {
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to update worker flavor: %w", err)
		}

		log.Printf("✅ Assigned new local flavor %d to worker %s", newFlavorId, req.WorkerId)
	} else {
		// Step 4: check if the flavor belongs to local provider
		var existingProviderId uint32
		var existingCpu int32
		var existingMem, existingDisk float32

		err = tx.QueryRowContext(ctx, `
			SELECT provider_id, cpu, mem, disk FROM flavor WHERE flavor_id = $1
		`, currentFlavorId.Int64).Scan(&existingProviderId, &existingCpu, &existingMem, &existingDisk)
		if err != nil {
			return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to fetch existing flavor: %w", err)
		}

		if existingProviderId != providerId {
			log.Printf("⚠️ Worker %s has non-local flavor %d, skipping update", req.WorkerId, currentFlavorId.Int64)
			// Optional: compare and log mismatch
		} else if existingCpu != req.Cpu || existingMem != req.Mem || existingDisk != req.Disk {
			_, err = tx.ExecContext(ctx, `
				UPDATE flavor SET cpu = $1, mem = $2, disk = $3 WHERE flavor_id = $4
			`, req.Cpu, req.Mem, req.Disk, currentFlavorId.Int64)
			if err != nil {
				return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to update local flavor: %w", err)
			}
			log.Printf("🔄 Updated local flavor %d for worker %s", currentFlavorId.Int64, req.WorkerId)
		}
	}

	if err := tx.Commit(); err != nil {
		return &taskqueuepb.Ack{Success: false}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &taskqueuepb.Ack{Success: true}, nil
}

func applyMigrations(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}

	var m *migrate.Migrate
	var migrate_err error
	sourceDriver, err := iofs.New(embeddedMigrations, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create embedded migration source: %w", err)
	}
	m, migrate_err = migrate.NewWithInstance("iofs", sourceDriver, "postgres", driver)

	if migrate_err != nil {
		return migrate_err
	}

	err = m.Up() // Apply all migrations
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	log.Println("✅ Database migrated successfully!")
	return nil
}

func LoadEmbeddedCertificates() (tls.Certificate, string, error) {

	var serverCert tls.Certificate
	// Read server certificate & key from embedded files
	serverCertPEM, err := embeddedCertificates.ReadFile("certificates/server.pem")
	if err != nil {
		return serverCert, "", fmt.Errorf("failed to read embedded server.pem: %w", err)
	}

	serverKeyPEM, err := embeddedCertificates.ReadFile("certificates/server.key")
	if err != nil {
		return serverCert, string(serverCertPEM), fmt.Errorf("failed to read embedded server.key: %w", err)
	}

	// Load the certificate
	serverCert, err = tls.X509KeyPair(serverCertPEM, serverKeyPEM)

	return serverCert, string(serverCertPEM), err
}

func (s *taskQueueServer) Shutdown() {
	close(s.stopWatchdog)
	// Other cleanup if needed later
}

func Serve(cfg config.Config) error {
	// Validate the configuration before proceeding
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Open a PostgreSQL connection using pgx driver
	db, err := sql.Open("pgx", cfg.Scitq.DBURL)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close()

	// Check workflow template directory
	if err := validateScriptConfig(cfg.Scitq.ScriptRoot, cfg.Scitq.ScriptInterpreter); err != nil {
		return fmt.Errorf("invalid script config: %w", err)
	}

	// Create the main server instance
	s := newTaskQueueServer(cfg, db, cfg.Scitq.LogRoot)

	// Configure database connection pool settings for concurrency
	db.SetMaxOpenConns(cfg.Scitq.MaxDBConcurrency * 2)
	db.SetMaxIdleConns(cfg.Scitq.MaxDBConcurrency * 2)
	db.SetConnMaxLifetime(30 * time.Minute)

	// 🕵️‍♂️ Periodically log database connection stats every 10 seconds
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			stats := db.Stats()
			log.Printf("🔎 DB connections - Open: %d, InUse: %d, Idle: %d, WaitCount: %d, WaitDuration: %s",
				stats.OpenConnections,
				stats.InUse,
				stats.Idle,
				stats.WaitCount,
				stats.WaitDuration,
			)
		}
	}()

	// Apply any database migrations needed at startup
	if err := applyMigrations(db); err != nil {
		return fmt.Errorf("migration error: %v", err)
	}

	log.Println("✅ Server started successfully!")
	checkAdminUser(db) // Ensure there is at least one admin user

	var creds credentials.TransportCredentials

	// Load TLS certificates - embedded or from configured files
	if cfg.Scitq.CertificateKey == "" || cfg.Scitq.CertificatePem == "" {
		log.Printf("🔐 Using embedded TLS certificates")
		serverCert, certPEMString, err := LoadEmbeddedCertificates()
		if err != nil {
			return fmt.Errorf("failed to load embedded TLS credentials: %v", err)
		}
		creds = credentials.NewServerTLSFromCert(&serverCert)
		s.sslCertificatePEM = certPEMString
	} else {
		certPEMData, err := os.ReadFile(cfg.Scitq.CertificatePem)
		if err != nil {
			log.Fatalf("failed to read certificate file: %v", err)
		}
		s.sslCertificatePEM = string(certPEMData)
		creds, err = credentials.NewServerTLSFromFile(cfg.Scitq.CertificatePem, cfg.Scitq.CertificateKey)
		if err != nil {
			return fmt.Errorf("failed to load TLS credentials: %v", err)
		}
	}

	// Create gRPC server with TLS and authentication interceptor
	grpcServer := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(workerAuthInterceptor(cfg.Scitq.WorkerToken, db)),
		grpc.MaxConcurrentStreams(uint32(cfg.Scitq.MaxDBConcurrency)),
	)

	// Start the gRPC server in a goroutine
	go func() error {
		defer s.Shutdown()

		// Initialize quota manager for worker recruitment and scheduling
		s.qm = *recruitment.NewQuotaManager(&cfg)
		recruitment.StartRecruiterLoop(context.Background(), s.db, &s.qm, s, cfg.Scitq.RecruitmentInterval, s.workerWeightMemory)

		err = s.checkProviders() // Verify available providers
		if err != nil {
			return fmt.Errorf("failed to check providers: %v", err)
		}
		s.startJobQueue()                         // Start processing jobs queue
		pb.RegisterTaskQueueServer(grpcServer, s) // Register gRPC service

		// Listen on configured TCP port
		lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Scitq.Port))
		if err != nil {
			return fmt.Errorf("failed to listen: %v", err)
		}

		// Trigger initial task assignment to workers
		s.triggerAssign()

		log.Printf("🚀 Server listening on port %d...", cfg.Scitq.Port)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
		return nil
	}()

	// Wrap the gRPC server with grpc-web for browser compatibility and CORS handling
	grpcWebServer := grpcweb.WrapServer(grpcServer, grpcweb.WithOriginFunc(func(origin string) bool {
		log.Printf("🌐 CORS check: origin = %s", origin)
		// Allow only local dev origin - adjust as needed
		return origin == "http://localhost:5173"
	}))

	// Setup HTTP multiplexer for REST and grpc-web endpoints
	mux := http.NewServeMux()
	mux.HandleFunc("/login", NewLogin(s))
	mux.Handle("/fetchCookie", fetchCookie())
	mux.HandleFunc("/logout", LogoutHandler)
	mux.HandleFunc("/WorkerToken", fetchWorkerTokenHandler(s))
	// Uncomment and adjust the next line to serve frontend static files
	// mux.Handle("/", http.FileServer(http.Dir("path/to/frontend/dist")))

	// Logging middleware + CORS headers for HTTP server
	loggedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("📩 Received request: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, x-grpc-web, x-user-agent, grpc-timeout, Authorization")

		// Handle preflight OPTIONS requests quickly
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Delegate request either to grpc-web or REST mux
		if grpcWebServer.IsGrpcWebRequest(r) || grpcWebServer.IsAcceptableGrpcCorsRequest(r) {
			grpcWebServer.ServeHTTP(w, r)
		} else {
			mux.ServeHTTP(w, r)
		}

		duration := time.Since(start)
		log.Printf("✅ Handled request: %s %s in %v", r.Method, r.URL.Path, duration)
	})

	// Start HTTP server to serve grpc-web and REST endpoints
	httpServer := http.Server{
		Addr:    ":8081",
		Handler: loggedHandler,
	}

	log.Println("🌍 gRPC-Web HTTP server listening on :8081")
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}

	return nil
}
