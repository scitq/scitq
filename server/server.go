package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmtsciencedev/scitq2/server/config"
	"github.com/gmtsciencedev/scitq2/server/memory"
	"github.com/gmtsciencedev/scitq2/server/protofilter"
	"github.com/gmtsciencedev/scitq2/server/providers"

	"github.com/gmtsciencedev/scitq2/server/recruitment"
	"github.com/golang-jwt/jwt"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

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
	providers     map[uint32]providers.Provider
	semaphore     chan struct{} // Semaphore to limit concurrency
	assignTrigger chan struct{}
	qm            recruitment.QuotaManager

	workerWeightMemory *sync.Map // worker_id -> map[task_id]float64
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
		assignTrigger:      make(chan struct{}, 1), // buffered, avoids blocking
		workerWeightMemory: workerWeightMemory,
	}
	//go s.assignTasksLoop()
	go s.waitForAssignEvents()
	return s
}

func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.Task) (*pb.TaskResponse, error) {
	var taskID int
	err := s.db.QueryRow(
		`INSERT INTO task (command, shell, container, container_options, step_id, 
					input, resource, output, retry, is_final, uses_cache, 
					download_timeout, running_timeout, upload_timeout,  
					status, created_at) 
		VALUES ($1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, 
			$12, $13, $14,
			 'P', NOW()) 
		RETURNING task_id`,
		req.Command, req.Shell, req.Container, req.ContainerOptions, req.StepId,
		req.Input, req.Resource, req.Output, req.Retry, req.IsFinal, req.UsesCache,
		req.DownloadTimeout, req.RunningTimeout, req.UploadTimeout,
	).Scan(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	log.Printf("✅ Task %d submitted (Command: %s, Container: %s)", taskID, req.Command, req.Container)

	// **Trigger task assignment**
	s.triggerAssign()

	return &pb.TaskResponse{TaskId: uint32(taskID)}, nil
}

func (s *taskQueueServer) GetRcloneConfig(ctx context.Context, req *emptypb.Empty) (*pb.RcloneConfig, error) {
	data, err := os.ReadFile(DefaultRcloneConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to read rclone config: %w", err)
	}
	return &pb.RcloneConfig{Config: string(data)}, nil
}

func (s *taskQueueServer) waitForAssignEvents() {
	for range s.assignTrigger {
		s.assignPendingTasks() // this logic is your current assignTasksLoop(), minus the loop and sleep
	}
}

// **Helper function to get the minimum of two integers**
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *taskQueueServer) UpdateTaskStatus(ctx context.Context, req *pb.TaskStatusUpdate) (*pb.Ack, error) {
	_, err := s.db.Exec("UPDATE task SET status = $1 WHERE task_id = $2", req.NewStatus, req.TaskId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to update task status: %w", err)
	}

	// **Trigger task assignment**
	s.triggerAssign()

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

func (s *taskQueueServer) StreamTaskLogs(req *pb.TaskId, stream pb.TaskQueue_StreamTaskLogsServer) error {
	logPath := getLogPath(req.TaskId, "stdout", s.logRoot)
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		stream.Send(&pb.TaskLog{TaskId: req.TaskId, LogType: "stdout", LogText: scanner.Text()})
	}
	return nil
}

func (s *taskQueueServer) RegisterWorker(ctx context.Context, req *pb.WorkerInfo) (*pb.WorkerId, error) {
	var workerID uint32

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("⚠️ Failed to start transaction: %v", err)
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// **Check if worker already exists**
	err = tx.QueryRow(`SELECT worker_id FROM worker WHERE worker_name = $1`, req.Name).Scan(&workerID)
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

	// **Trigger task assignment**
	s.triggerAssign()

	return &pb.WorkerId{WorkerId: workerID}, nil
}

func (s *taskQueueServer) CreateWorker(ctx context.Context, req *pb.WorkerRequest) (*pb.WorkerIds, error) {
	var workerIDs []uint32
	var jobs []Job

	{
		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("⚠️ Failed to start transaction: %v", err)
			return nil, fmt.Errorf("failed to start transaction: %w", err)
		}
		defer tx.Rollback()

		for req.Number > 0 {
			req.Number--
			var workerID uint32
			var workerName string
			var providerId uint32
			var regionName string
			var flavorName string
			var providerName string
			var cpu int32
			var mem float32
			err := tx.QueryRow(`WITH insertquery AS (
  INSERT INTO worker (step_id, worker_name, concurrency, flavor_id, region_id, is_permanent)
  VALUES (NULLIF($1,0), $5 || 'Worker' || CURRVAL('worker_worker_id_seq'), $2, $3, $4, FALSE)
  RETURNING worker_id,worker_name,region_id, flavor_id
)
SELECT iq.worker_id, iq.worker_name, r.provider_id, p.provider_name, r.region_name, f.flavor_name, f.cpu, f.mem
FROM insertquery iq
JOIN region r ON iq.region_id = r.region_id
JOIN flavor f ON iq.flavor_id = f.flavor_id
JOIN provider p ON r.provider_id = p.provider_id`,
				req.StepId, req.Concurrency, req.FlavorId, req.RegionId, s.cfg.Scitq.ServerName).Scan(
				&workerID, &workerName, &providerId, &providerName, &regionName, &flavorName, &cpu, &mem)
			if err != nil {
				return nil, fmt.Errorf("failed to register worker: %w", err)
			}
			workerIDs = append(workerIDs, workerID)

			var jobID uint32
			tx.QueryRow("INSERT INTO job (worker_id,flavor_id,region_id,retry) VALUES ($1,$2,$3,$4) RETURNING job_id",
				workerID, req.FlavorId, req.RegionId, defaultJobRetry).Scan(&jobID)
			s.qm.RegisterLaunch(regionName, providerName, cpu, mem)
			jobs = append(jobs, Job{
				JobID:      jobID,
				WorkerID:   workerID,
				WorkerName: workerName,
				ProviderID: providerId,
				Region:     regionName,
				Flavor:     flavorName,
				Action:     'C',
				Retry:      defaultJobRetry,
				Timeout:    defaultJobTimeout,
			})

			if err := tx.Commit(); err != nil {
				log.Printf("⚠️ Failed to commit worker registration: %v", err)
				return nil, fmt.Errorf("failed to commit worker registration: %w", err)
			}
		}
	}
	// TODO launch the jobs
	for _, job := range jobs {
		s.addJob(job)
	}

	// **Trigger task assignment**
	s.triggerAssign()

	return &pb.WorkerIds{WorkerIds: workerIDs}, nil
}

func (s *taskQueueServer) DeleteWorker(ctx context.Context, req *pb.WorkerId) (*pb.Ack, error) {
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
		return &pb.Ack{Success: false}, fmt.Errorf("failed to find worker %d: %w", req.WorkerId, err)
	}

	if !is_permanent {
		var jobId uint32
		err = tx.QueryRow("INSERT INTO job (worker_id,action,region_id,retry) VALUES ($1,'D',$2,$3) RETURNING job_id",
			req.WorkerId, regionId, defaultJobRetry).Scan(&jobId)
		if err != nil {
			return &pb.Ack{Success: false}, fmt.Errorf("failed to create job for worker %d: %w", req.WorkerId, err)
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
				return &pb.Ack{Success: false}, fmt.Errorf("failed to delete worker %d: %w", req.WorkerId, err)
			}
		} else {
			return &pb.Ack{Success: false}, fmt.Errorf("will not delete permanent worker %d with status %c", req.WorkerId, status)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit worker deletion: %v", err)
		return &pb.Ack{Success: false}, fmt.Errorf("failed to commit worker deletion: %w", err)
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

	// **Filter by status if provided**
	if req.StatusFilter != nil && *req.StatusFilter != "" {
		rows, err = tx.Query(`SELECT task_id, command, container, status FROM task WHERE status = $1 ORDER BY task_id`, *req.StatusFilter)
	} else {
		rows, err = tx.Query(`SELECT task_id, command, container, status FROM task ORDER BY task_id`)
	}

	if err != nil {
		log.Printf("⚠️ Failed to list tasks: %v", err)
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var task pb.Task
		err := rows.Scan(&task.TaskId, &task.Command, &task.Container, &task.Status)
		if err != nil {
			log.Printf("⚠️ Failed to scan task: %v", err)
			continue
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

func (s *taskQueueServer) PingAndTakeNewTasks(ctx context.Context, req *pb.WorkerId) (*pb.TaskListAndOther, error) {
	var (
		tasks       []*pb.Task
		concurrency uint32
		input       pq.StringArray
		resource    pq.StringArray
		shell       sql.NullString
	)

	err := s.db.QueryRow(`
		SELECT concurrency FROM worker WHERE worker_id = $1
	`, req.WorkerId).Scan(&concurrency)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("worker ID %d not found", req.WorkerId)
		}
		return nil, fmt.Errorf("failed to fetch worker concurrency for worker %d: %w", req.WorkerId, err)
	}

	rows, err := s.db.Query(`
		SELECT task_id, command, shell, container, container_options,
			input, resource, output, retry, is_final, uses_cache, 
			download_timeout, running_timeout, upload_timeout,  
			status
		FROM task
		WHERE worker_id = $1 AND status = 'A' AND coalesce(task.step_id,0)=coalesce((SELECT step_id FROM worker WHERE worker_id=$1),0)
	`, req.WorkerId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch assigned tasks: %w", err)
	}
	defer rows.Close()

	taskUpdateList := make(map[uint32]*pb.TaskUpdate)

	for rows.Next() {
		var task pb.Task
		if err := rows.Scan(&task.TaskId, &task.Command, &shell, &task.Container, &task.ContainerOptions,
			&input, &resource, &task.Output, &task.Retry, &task.IsFinal, &task.UsesCache,
			&task.DownloadTimeout, &task.RunningTimeout, &task.UploadTimeout, &task.Status); err != nil {
			log.Printf("⚠️ Task decode error: %v", err)
			continue
		}
		task.Input = []string(input)
		task.Resource = []string(resource)
		if shell.Valid {
			task.Shell = proto.String(shell.String)
		}
		tasks = append(tasks, &task)

		// Add weight if available
		if val, ok := s.workerWeightMemory.Load(int(req.WorkerId)); ok {
			taskMap := val.(*sync.Map)
			if weightVal, ok := taskMap.Load(task.TaskId); ok {
				weight, ok := weightVal.(float64)
				if ok {
					taskUpdateList[*task.TaskId] = &pb.TaskUpdate{Weight: weight}
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating through tasks: %w", err)
	}

	return &pb.TaskListAndOther{
		Tasks:       tasks,
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

func (s *taskQueueServer) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.Ack, error) {
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}

	hashedPw, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}

	_, err = s.db.Exec(`INSERT INTO scitq_user (username, password, email, is_admin) VALUES ($1, $2, $3, $4)`,
		req.Username, hashedPw, req.Email, req.IsAdmin)
	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") {
			return nil, status.Error(codes.AlreadyExists, "username already exists")
		}
		return nil, status.Error(codes.Internal, "failed to create user")
	}

	return &pb.Ack{Success: true}, nil
}

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

func (s *taskQueueServer) DeleteUser(ctx context.Context, req *pb.UserId) (*pb.Ack, error) {
	if !IsAdmin(ctx) {
		return nil, status.Error(codes.PermissionDenied, "admin privileges required")
	}
	_, err := s.db.Exec("DELETE FROM scitq_user WHERE user_id=$1", req.UserId)
	if err != nil {
		return &pb.Ack{Success: false}, fmt.Errorf("failed to delete user %d: %w", req.UserId, err)
	}
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

	_, err = s.db.Exec("UPDATE scitq_user SET password=$1 WHERE username=$2", hash, req.NewPassword)
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

	// Insert with embedded subqueries for provider_id and region_id
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recruiter (
			step_id, rank, protofilter,
			worker_concurrency, worker_prefetch, maximum_workers, rounds, timeout
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9, $10
		)
	`,
		req.StepId, req.Rank, req.Protofilter,
		req.Concurrency, req.Prefetch, req.MaxWorkers, req.Rounds, req.Timeout,
	)

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
	err := s.db.QueryRow(`
		INSERT INTO workflow (workflow_name, run_strategy, maximum_workers)
		VALUES ($1, $2, $3)
		RETURNING workflow_id
	`, req.Name, req.RunStrategy, req.MaximumWorkers).Scan(&workflowID)

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

func LoadEmbeddedCertificates() (tls.Certificate, error) {

	var serverCert tls.Certificate
	// Read server certificate & key from embedded files
	serverCertPEM, err := embeddedCertificates.ReadFile("certificates/server.pem")
	if err != nil {
		return serverCert, fmt.Errorf("failed to read embedded server.pem: %w", err)
	}

	serverKeyPEM, err := embeddedCertificates.ReadFile("certificates/server.key")
	if err != nil {
		return serverCert, fmt.Errorf("failed to read embedded server.key: %w", err)
	}

	// Load the certificate
	serverCert, err = tls.X509KeyPair(serverCertPEM, serverKeyPEM)

	return serverCert, err
}

func Serve(cfg config.Config) error {
	db, err := sql.Open("pgx", cfg.Scitq.DBURL)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close()

	// Apply migrations on startup
	if err := applyMigrations(db); err != nil {
		return fmt.Errorf("migration error: %v", err)
	}

	log.Println("Server started successfully!")
	checkAdminUser(db)

	var creds credentials.TransportCredentials
	if cfg.Scitq.CertificateKey == "" || cfg.Scitq.CertificatePem == "" {
		log.Printf("Using embedded certificates")
		serverCert, err := LoadEmbeddedCertificates()
		if err != nil {
			return fmt.Errorf("failed to load embedded TLS credentials: %v", err)
		}

		// ✅ Use `credentials.NewServerTLSFromCert()` instead of `NewServerTLSFromFile()`
		creds = credentials.NewServerTLSFromCert(&serverCert)
	} else {
		creds, err = credentials.NewServerTLSFromFile(cfg.Scitq.CertificatePem, cfg.Scitq.CertificateKey)
		if err != nil {
			return fmt.Errorf("failed to load TLS credentials: %v", err)
		}
	}

	grpcServer := grpc.NewServer(
		grpc.Creds(creds),
		grpc.UnaryInterceptor(workerAuthInterceptor(cfg.Scitq.WorkerToken, db)),
	)
	s := newTaskQueueServer(cfg, db, cfg.Scitq.LogRoot)

	// initialize the quota manager
	s.qm = *recruitment.NewQuotaManager(&cfg)
	recruitment.StartRecruiterLoop(context.Background(), s.db, &s.qm, s, cfg.Scitq.RecruitmentInterval, s.workerWeightMemory)

	s.checkProviders()
	s.startJobQueue()
	pb.RegisterTaskQueueServer(grpcServer, s)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Scitq.Port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	// **Trigger task assignment**
	s.triggerAssign()

	log.Println("Server listening on port 50051...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

	return nil
}
