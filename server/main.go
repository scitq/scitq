package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

type taskQueueServer struct {
	pb.UnimplementedTaskQueueServer
	logRoot string
	db      *sql.DB
}

func newTaskQueueServer(db *sql.DB, logRoot string) *taskQueueServer {
	s := &taskQueueServer{db: db, logRoot: logRoot}
	go s.assignTasksLoop()
	return s
}

func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.TaskRequest) (*pb.TaskResponse, error) {
	var taskID int
	err := s.db.QueryRow(
		"INSERT INTO task (command, container, status, created_at) VALUES ($1, $2, 'P', NOW()) RETURNING task_id",
		req.Command, req.Container,
	).Scan(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	log.Printf("✅ Task %d submitted (Command: %s, Container: %s)", taskID, req.Command, req.Container)
	return &pb.TaskResponse{TaskId: uint32(taskID)}, nil
}

func (s *taskQueueServer) assignTasksLoop() {
	for {
		time.Sleep(5 * time.Second) // Run every 5 seconds

		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("⚠️ Failed to begin transaction: %v", err)
			continue
		}

		// **1️⃣ Count pending tasks**
		var pendingTaskCount int
		err = tx.QueryRow(`SELECT COUNT(*) FROM task WHERE status = 'P'`).Scan(&pendingTaskCount)
		if err != nil {
			log.Printf("⚠️ Failed to count pending tasks: %v", err)
			tx.Rollback()
			continue
		}

		// **🚨 If no pending tasks, skip this cycle**
		if pendingTaskCount == 0 {
			tx.Rollback()
			continue
		}

		// **2️⃣ Get workers & their assigned task count**
		rows, err := tx.Query(`
			SELECT w.worker_id, w.concurrency, COUNT(t.task_id) 
			FROM worker w
			LEFT JOIN task t ON t.worker_id = w.worker_id AND t.status IN ('A', 'C', 'R')
			GROUP BY w.worker_id, w.concurrency
		`)
		if err != nil {
			log.Printf("⚠️ Failed to fetch workers: %v", err)
			tx.Rollback()
			continue
		}

		workerSlots := make(map[uint32]int) // worker_id → available slots
		for rows.Next() {
			var workerID uint32
			var concurrency, assigned int
			if err := rows.Scan(&workerID, &concurrency, &assigned); err != nil {
				log.Printf("⚠️ Failed to scan worker row: %v", err)
				continue
			}
			if assigned < concurrency {
				workerSlots[workerID] = concurrency - assigned // Free slots
			}
		}
		rows.Close()

		// **🚨 If no worker has available slots, skip this cycle**
		if len(workerSlots) == 0 {
			tx.Rollback()
			continue
		}

		// **3️⃣ Assign tasks to workers with available slots**
		for workerID, slots := range workerSlots {
			if pendingTaskCount == 0 {
				break // 🚀 Stop if no more tasks left
			}

			if slots > 0 {
				// Assign only up to available pending tasks
				tasksToAssign := min(slots, pendingTaskCount)

				res, err := tx.Exec(`
					UPDATE task 
					SET status = 'A', worker_id = $1
					WHERE task_id IN (
						SELECT task_id FROM task WHERE status = 'P'
						ORDER BY created_at ASC 
						LIMIT $2
					)
				`, workerID, tasksToAssign)

				if err != nil {
					log.Printf("⚠️ Failed to assign tasks for worker %d: %v", workerID, err)
				} else {
					rowsAffected, _ := res.RowsAffected()
					log.Printf("✅ Assigned %d tasks to worker %d", rowsAffected, workerID)
					pendingTaskCount -= int(rowsAffected) // 🛑 Reduce remaining task count
				}
			}
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			log.Printf("⚠️ Failed to commit task assignment: %v", err)
		}
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
		err = tx.QueryRow(`INSERT INTO worker (worker_name, concurrency, last_ping) VALUES ($1, $2, NOW()) RETURNING worker_id`,
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
		_, err = tx.Exec(`UPDATE task SET status='F' WHERE status='C' AND worker_id=$1`,
			workerID)
		if err != nil {
			return nil, fmt.Errorf("failed to fail tasks that were running when client %d crashed: %w", workerID, err)
		}
		log.Printf("✅ Worker %s already registered, sending back id %d", req.Name, workerID)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("⚠️ Failed to commit worker registration: %v", err)
		return nil, fmt.Errorf("failed to commit worker registration: %w", err)
	}

	return &pb.WorkerId{WorkerId: workerID}, nil
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
	var tasks []*pb.Task
	var concurrency uint32

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
		SELECT task_id, command, container, status
		FROM task
		WHERE worker_id = $1 AND status = 'A'
	`, req.WorkerId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch assigned tasks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var task pb.Task
		if err := rows.Scan(&task.TaskId, &task.Command, &task.Container, &task.Status); err != nil {
			log.Printf("Task decode error: %v", err)
			continue
		}
		tasks = append(tasks, &task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating through tasks: %w", err)
	}

	return &pb.TaskListAndOther{
		Tasks:       tasks,
		Concurrency: concurrency,
	}, nil
}

func applyMigrations(db *sql.DB) error {
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithDatabaseInstance("file://migrations", "postgres", driver)
	if err != nil {
		return err
	}

	err = m.Up() // Apply all migrations
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	log.Println("✅ Database migrated successfully!")
	return nil
}

func serve(dbURL string, logRoot string, port int) error {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close()

	// Apply migrations on startup
	if err := applyMigrations(db); err != nil {
		return fmt.Errorf("migration error: %v", err)
	}

	log.Println("Server started successfully!")

	creds, err := credentials.NewServerTLSFromFile("server.pem", "server.key")
	if err != nil {
		return fmt.Errorf("failed to load TLS credentials: %v", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	pb.RegisterTaskQueueServer(grpcServer, newTaskQueueServer(db, logRoot))

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}
	log.Println("Server listening on port 50051...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}

	return nil
}

func main() {
	defaultDBURL := "postgres://scitq_user:dsofposiudipopipII9@localhost/scitq2?sslmode=disable"
	defaultLogRoot := "log"
	defaultPort := 50051

	if err := serve(defaultDBURL, defaultLogRoot, defaultPort); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
