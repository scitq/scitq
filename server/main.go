package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// Task and Worker structures for storage
type Task struct {
	ID        uint64
	WorkerID  *uint64
	Command   string
	Container string
	Status    string
	CreatedAt time.Time
}

type Worker struct {
	ID          uint64
	Name        string
	Concurrency int
}

// taskQueueServer implements the gRPC service.
type taskQueueServer struct {
	pb.UnimplementedTaskQueueServer
	db *bolt.DB
	mu sync.Mutex
}

// newTaskQueueServer creates a new server with the provided bbolt DB.
func newTaskQueueServer(db *bolt.DB) *taskQueueServer {
	s := &taskQueueServer{db: db}
	go s.assignTasksLoop() // Start the background task assignment loop
	return s
}

// Helper function to convert uint64 to an 8-byte big-endian slice.
func itob(v uint64) []byte {
	b := make([]byte, 8)
	for i := uint(0); i < 8; i++ {
		b[7-i] = byte(v >> (i * 8))
	}
	return b
}

// **TASK ASSIGNMENT LOOP**
func (s *taskQueueServer) assignTasksLoop() {
	for {
		time.Sleep(5 * time.Second) // Run every 5 seconds
		s.mu.Lock()                 // Ensure only one update runs at a time

		err := s.db.Update(func(tx *bolt.Tx) error {
			workersBucket := tx.Bucket([]byte("workers"))
			tasksPendingBucket := tx.Bucket([]byte("tasks_by_status/P"))
			tasksAssignedBucket, err := tx.CreateBucketIfNotExists([]byte("tasks_by_status/A"))
			if err != nil {
				return fmt.Errorf("failed to create tasks_by_status/A")
			}
			tasksBucket := tx.Bucket([]byte("tasks"))
			if workersBucket == nil || tasksPendingBucket == nil || tasksBucket == nil {
				return fmt.Errorf("required buckets not found")
			}

			// **1️⃣ Build Worker Map & Count Assigned Tasks**
			workerMap := make(map[uint64]*Worker)
			assignedCounts := make(map[uint64]int)

			c := workersBucket.Cursor()
			for wKey, wVal := c.First(); wKey != nil; wKey, wVal = c.Next() {
				var worker Worker
				if err := gob.NewDecoder(bytes.NewReader(wVal)).Decode(&worker); err != nil {
					log.Printf("Worker decode error: %v", err)
					continue
				}
				workerMap[worker.ID] = &worker
				assignedCounts[worker.ID] = 0
			}

			// **2️⃣ Count Assigned Tasks (A, C, R)**
			taskCursor := tasksBucket.Cursor()
			for tKey, tVal := taskCursor.First(); tKey != nil; tKey, tVal = taskCursor.Next() {
				var task Task
				if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
					log.Printf("Task decode error: %v", err)
					continue
				}
				if task.WorkerID != nil && (task.Status == "A" || task.Status == "C" || task.Status == "R") {
					assignedCounts[*task.WorkerID]++
				}
			}

			// **3️⃣ Assign Pending Tasks (P → A)**
			pendingCursor := tasksPendingBucket.Cursor()
			for tKey, tVal := pendingCursor.First(); tKey != nil; tKey, tVal = pendingCursor.Next() {
				var task Task
				if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
					log.Printf("Task decode error: %v", err)
					continue
				}
				for workerID, worker := range workerMap {
					if assignedCounts[workerID] < worker.Concurrency {
						task.Status = "A" // Assign task
						task.WorkerID = &workerID
						var buf bytes.Buffer
						if err := gob.NewEncoder(&buf).Encode(task); err != nil {
							log.Printf("Task encoding error: %v", err)
							continue
						}
						tasksBucket.Put(tKey, buf.Bytes())
						tasksAssignedBucket.Put(tKey, buf.Bytes()) // ✅ Move to tasks_by_status/A
						tasksPendingBucket.Delete(tKey)            // ❌ Remove from P

						log.Printf("✅ Assigned task %d to worker %d", task.ID, workerID)

						assignedCounts[workerID]++
						break // Move to next pending task
					}
				}
			}

			return nil
		})
		if err != nil {
			log.Printf("Task assignment loop error: %v", err)
		}

		s.mu.Unlock()
	}
}

// SubmitTask stores a new task in bbolt using gob encoding.
// SubmitTask stores a new task in bbolt using gob encoding and registers it in tasks_by_status/P.
func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.TaskRequest) (*pb.TaskResponse, error) {
	var taskID uint64

	err := s.db.Update(func(tx *bolt.Tx) error {
		// Ensure main tasks bucket and pending task bucket exist
		tasksBucket, err := tx.CreateBucketIfNotExists([]byte("tasks"))
		tasksPendingBucket, err2 := tx.CreateBucketIfNotExists([]byte("tasks_by_status/P"))
		if err != nil || err2 != nil {
			return fmt.Errorf("failed to create required buckets")
		}

		// Generate new task ID
		id, err := tasksBucket.NextSequence()
		if err != nil {
			return err
		}
		taskID = id

		// Create task object
		task := Task{
			ID:        id,
			Command:   req.Command,
			Container: req.Container,
			Status:    "P", // Pending
			CreatedAt: time.Now(),
		}

		// Encode task
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(task); err != nil {
			return err
		}

		// Store task in `tasks`
		if err := tasksBucket.Put(itob(id), buf.Bytes()); err != nil {
			return err
		}

		// Store task in `tasks_by_status/P`
		if err := tasksPendingBucket.Put(itob(id), buf.Bytes()); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	return &pb.TaskResponse{TaskId: int32(taskID)}, nil
}

// RegisterWorker stores or updates a worker in bbolt using gob encoding.
func (s *taskQueueServer) RegisterWorker(ctx context.Context, req *pb.WorkerInfo) (*pb.WorkerId, error) {
	var workerID uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("workers"))
		if b == nil {
			return fmt.Errorf("workers bucket not found")
		}

		// Check if the worker already exists
		key := []byte(req.Name)
		existing := b.Get(key)
		var worker Worker

		if existing != nil {
			// Worker already exists, retrieve ID
			if err := gob.NewDecoder(bytes.NewReader(existing)).Decode(&worker); err != nil {
				return fmt.Errorf("failed to decode existing worker: %w", err)
			}
			workerID = worker.ID
			//worker.Concurrency = int(*req.Concurrency) // Update concurrency if changed
			return nil
		} else {
			// Assign a new unique worker ID
			id, err := b.NextSequence()
			if err != nil {
				return fmt.Errorf("failed to generate worker ID: %w", err)
			}
			var concurrency int32 = 0
			if req.Concurrency != nil && *req.Concurrency > 0 {
				concurrency = *req.Concurrency
			}
			worker = Worker{
				ID:          id,
				Name:        req.Name,
				Concurrency: int(concurrency),
			}
			workerID = id

			// Store the worker by name
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(worker); err != nil {
				return fmt.Errorf("failed to encode worker: %w", err)
			}

			return b.Put(key, buf.Bytes())

		}

	})

	if err != nil {
		log.Printf("⚠️ Failed to register worker: %v", err)
		return nil, err
	}

	log.Printf("✅ Worker %s registered with ID %d", req.Name, workerID)
	return &pb.WorkerId{WorkerId: int32(workerID)}, nil
}

func main() {
	// Open bbolt database.
	db, err := bolt.Open("my.db", 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		log.Fatalf("failed to open bbolt db: %v", err)
	}
	defer db.Close()

	// Ensure necessary buckets exist.
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("tasks"))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte("workers"))
		return err
	})
	if err != nil {
		log.Fatalf("failed to create buckets: %v", err)
	}

	// Load TLS credentials.
	creds, err := credentials.NewServerTLSFromFile("server.pem", "server.key")
	if err != nil {
		log.Fatalf("failed to load TLS credentials: %v", err)
	}

	// Create gRPC server with TLS.
	grpcServer := grpc.NewServer(grpc.Creds(creds))
	pb.RegisterTaskQueueServer(grpcServer, newTaskQueueServer(db))

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	log.Println("Server listening on port 50051...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *taskQueueServer) PingAndTakeNewTasks(ctx context.Context, req *pb.WorkerId) (*pb.TaskList, error) {
	var tasks []*pb.Task

	err := s.db.View(func(tx *bolt.Tx) error {
		tasksBucket := tx.Bucket([]byte("tasks_by_status/A"))
		if tasksBucket == nil {
			return nil // No assigned tasks
		}

		c := tasksBucket.Cursor()
		for tKey, tVal := c.First(); tKey != nil; tKey, tVal = c.Next() {
			var task Task
			if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
				log.Printf("Task decode error: %v", err)
				continue
			}

			// Match tasks assigned to this worker ID
			if task.WorkerID != nil && *task.WorkerID == uint64(req.WorkerId) {
				tasks = append(tasks, &pb.Task{
					TaskId:    int32(task.ID),
					Command:   task.Command,
					Container: task.Container,
					Status:    task.Status,
				})
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch tasks: %w", err)
	}

	return &pb.TaskList{Tasks: tasks}, nil
}

func (s *taskQueueServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.TaskList, error) {
	var tasks []*pb.Task
	statusFilter := req.StatusFilter

	err := s.db.View(func(tx *bolt.Tx) error {
		tasksBucket := tx.Bucket([]byte("tasks"))
		if tasksBucket == nil {
			return nil
		}

		c := tasksBucket.Cursor()
		for tKey, tVal := c.First(); tKey != nil; tKey, tVal = c.Next() {
			var task Task
			if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
				log.Printf("Task decode error: %v", err)
				continue
			}

			// Apply status filter if provided
			if statusFilter != nil && *statusFilter != "" && task.Status != *statusFilter {
				continue
			}

			tasks = append(tasks, &pb.Task{
				TaskId:    int32(task.ID),
				Command:   task.Command,
				Container: task.Container,
				Status:    task.Status,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return &pb.TaskList{Tasks: tasks}, nil
}

// UpdateTaskStatus now updates both the task and its indexed status.
func (s *taskQueueServer) UpdateTaskStatus(ctx context.Context, req *pb.TaskStatusUpdate) (*pb.Ack, error) {
	taskID := req.TaskId
	newStatus := req.NewStatus

	err := s.db.Update(func(tx *bolt.Tx) error {
		tasksBucket := tx.Bucket([]byte("tasks"))
		if tasksBucket == nil {
			return fmt.Errorf("❌ tasks bucket not found")
		}

		// Fetch task
		taskKey := itob(uint64(taskID))
		taskBytes := tasksBucket.Get(taskKey)
		if taskBytes == nil {
			return fmt.Errorf("❌ task %d not found", taskID)
		}

		// Deserialize task
		var task Task
		if err := gob.NewDecoder(bytes.NewReader(taskBytes)).Decode(&task); err != nil {
			return fmt.Errorf("❌ failed to decode task %d: %w", taskID, err)
		}

		// **1️⃣ Remove Task from Old Status Bucket**
		if task.Status != "" {
			oldStatusBucket := tx.Bucket([]byte(fmt.Sprintf("tasks_by_status/%s", task.Status)))
			if oldStatusBucket != nil {
				if err := oldStatusBucket.Delete(taskKey); err != nil {
					log.Printf("⚠️ Failed to remove task %d from old status %s: %v", taskID, task.Status, err)
				} else {
					log.Printf("✅ Removed task %d from tasks_by_status/%s", taskID, task.Status)
				}
			}
		}

		// **2️⃣ Update Status**
		oldStatus := task.Status
		task.Status = newStatus

		// **3️⃣ Store Updated Task in `tasks`**
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(task); err != nil {
			return fmt.Errorf("❌ failed to encode updated task %d: %w", taskID, err)
		}
		if err := tasksBucket.Put(taskKey, buf.Bytes()); err != nil {
			return fmt.Errorf("❌ failed to store updated task %d: %w", taskID, err)
		}

		// **4️⃣ Store in New Status Bucket (Auto-Create)**
		newStatusBucket, err := tx.CreateBucketIfNotExists([]byte(fmt.Sprintf("tasks_by_status/%s", newStatus)))
		if err != nil {
			return fmt.Errorf("❌ failed to create status bucket %s: %w", newStatus, err)
		}

		if err := newStatusBucket.Put(taskKey, buf.Bytes()); err != nil {
			return fmt.Errorf("❌ failed to store task %d in new status bucket %s: %w", taskID, newStatus, err)
		}

		log.Printf("🔄 Task %d transitioned from %s → %s", taskID, oldStatus, newStatus)

		return nil
	})

	if err != nil {
		log.Printf("⚠️ UpdateTaskStatus failed: %v", err)
		return &pb.Ack{Success: false}, err
	}

	log.Printf("✅ Task %d updated to status %s", taskID, newStatus)
	return &pb.Ack{Success: true}, nil
}

func (s *taskQueueServer) SendTaskLogs(stream pb.TaskQueue_SendTaskLogsServer) error {
	for {
		logEntry, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&pb.Ack{Success: true})
		}
		if err != nil {
			log.Printf("❌ Failed to receive log entry: %v", err)
			return err
		}

		// Store log entry in bbolt under logs/{task_id}
		err = s.db.Update(func(tx *bolt.Tx) error {
			logBucket, err := tx.CreateBucketIfNotExists([]byte(fmt.Sprintf("logs/%d", logEntry.TaskId)))
			if err != nil {
				return fmt.Errorf("failed to create log bucket: %w", err)
			}

			logKey := itob(uint64(time.Now().UnixNano())) // Unique timestamp key
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(logEntry); err != nil {
				return fmt.Errorf("failed to encode log entry: %w", err)
			}
			return logBucket.Put(logKey, buf.Bytes())
		})

		if err != nil {
			log.Printf("❌ Failed to store log entry for task %d: %v", logEntry.TaskId, err)
		}
	}
}

func (s *taskQueueServer) StreamTaskLogs(req *pb.TaskId, stream pb.TaskQueue_StreamTaskLogsServer) error {
	taskID := req.TaskId
	err := s.db.View(func(tx *bolt.Tx) error {
		logBucket := tx.Bucket([]byte(fmt.Sprintf("logs/%d", taskID)))
		if logBucket == nil {
			return fmt.Errorf("no logs found for task %d", taskID)
		}

		c := logBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var logEntry pb.TaskLog
			if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&logEntry); err != nil {
				log.Printf("❌ Log decode error for task %d: %v", taskID, err)
				continue
			}

			if err := stream.Send(&logEntry); err != nil {
				log.Printf("❌ Failed to send log entry for task %d: %v", taskID, err)
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("❌ Failed to stream logs for task %d: %v", taskID, err)
		return err
	}

	return nil
}
