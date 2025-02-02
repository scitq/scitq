package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
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
			tasksBucket := tx.Bucket([]byte("tasks"))
			if workersBucket == nil || tasksBucket == nil {
				return nil // Buckets must exist
			}

			c := workersBucket.Cursor()
			for wKey, wVal := c.First(); wKey != nil; wKey, wVal = c.Next() {
				var worker Worker
				if err := gob.NewDecoder(bytes.NewReader(wVal)).Decode(&worker); err != nil {
					log.Printf("Worker decode error: %v", err)
					continue
				}

				// Count tasks assigned to this worker in "A", "C", or "R" states
				assignedTasks := 0
				taskCursor := tasksBucket.Cursor()
				for tKey, tVal := taskCursor.First(); tKey != nil; tKey, tVal = taskCursor.Next() {
					var task Task
					if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
						log.Printf("Task decode error: %v", err)
						continue
					}
					if task.WorkerID != nil && *task.WorkerID == worker.ID &&
						(task.Status == "A" || task.Status == "C" || task.Status == "R") {
						assignedTasks++
					}
				}

				if assignedTasks < worker.Concurrency {
					availableSlots := worker.Concurrency - assignedTasks

					// Assign up to availableSlots tasks from pending ("P") state
					taskCursor = tasksBucket.Cursor()
					for tKey, tVal := taskCursor.First(); tKey != nil; tKey, tVal = taskCursor.Next() {
						var task Task
						if err := gob.NewDecoder(bytes.NewReader(tVal)).Decode(&task); err != nil {
							log.Printf("Task decode error: %v", err)
							continue
						}
						if task.Status == "P" { // Pending tasks only
							task.Status = "A" // Assigned
							task.WorkerID = &worker.ID
							var buf bytes.Buffer
							if err := gob.NewEncoder(&buf).Encode(task); err != nil {
								log.Printf("Task encoding error: %v", err)
								continue
							}
							tasksBucket.Put(tKey, buf.Bytes())

							availableSlots--
							if availableSlots == 0 {
								break
							}
						}
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
func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.TaskRequest) (*pb.TaskResponse, error) {
	var taskID uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("tasks"))
		if b == nil {
			return nil
		}
		id, err := b.NextSequence()
		if err != nil {
			return err
		}
		taskID = id
		task := Task{
			ID:        id,
			Command:   req.Command,
			Container: req.Container,
			Status:    "P", // Pending
			CreatedAt: time.Now(),
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(task); err != nil {
			return err
		}
		return b.Put(itob(id), buf.Bytes())
	})
	if err != nil {
		return nil, err
	}
	return &pb.TaskResponse{TaskId: int32(taskID)}, nil
}

// RegisterWorker stores or updates a worker in bbolt using gob encoding.
func (s *taskQueueServer) RegisterWorker(ctx context.Context, req *pb.WorkerInfo) (*pb.Ack, error) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("workers"))
		if b == nil {
			return nil
		}
		key := []byte(req.Name)
		var worker Worker
		v := b.Get(key)
		if v != nil {
			if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&worker); err != nil {
				return err
			}
			worker.Concurrency = int(req.Concurrency)
		} else {
			id, err := b.NextSequence()
			if err != nil {
				return err
			}
			worker = Worker{
				ID:          id,
				Name:        req.Name,
				Concurrency: int(req.Concurrency),
			}
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(worker); err != nil {
			return err
		}
		return b.Put(key, buf.Bytes())
	})
	if err != nil {
		return nil, err
	}
	return &pb.Ack{Success: true}, nil
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

func (s *taskQueueServer) PingAndTakeNewTasks(ctx context.Context, req *pb.WorkerInfo) (*pb.TaskList, error) {
	var tasks []*pb.Task

	err := s.db.View(func(tx *bolt.Tx) error {

		workersBucket := tx.Bucket([]byte("workers"))
		if workersBucket == nil {
			return nil
		}
		key := string(req.Name)
		var worker Worker
		wc := workersBucket.Cursor()
		for wKey, wVal := wc.First(); wKey != nil; wKey, wVal = wc.Next() {
			if err := gob.NewDecoder(bytes.NewReader(wVal)).Decode(&worker); err != nil {
				log.Printf("Worker decode error: %v", err)
				continue
			}
			if worker.Name == key {
				break
			}
		}
		if worker.Name != key {
			return nil
		}

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
			if *task.WorkerID == worker.ID && task.Status == "A" {
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
		return nil, fmt.Errorf("failed to list tasks: %w", err)
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

// UpdateTaskStatus updates the status of a given task.
func (s *taskQueueServer) UpdateTaskStatus(ctx context.Context, req *pb.TaskStatusUpdate) (*pb.Ack, error) {
	taskID := req.TaskId
	newStatus := req.NewStatus

	err := s.db.Update(func(tx *bolt.Tx) error {
		tasksBucket := tx.Bucket([]byte("tasks"))
		if tasksBucket == nil {
			return fmt.Errorf("tasks bucket not found")
		}

		// Retrieve task from DB
		taskKey := itob(uint64(taskID))
		taskBytes := tasksBucket.Get(taskKey)
		if taskBytes == nil {
			return fmt.Errorf("task %d not found", taskID)
		}

		// Deserialize the task
		var task Task
		if err := gob.NewDecoder(bytes.NewReader(taskBytes)).Decode(&task); err != nil {
			return fmt.Errorf("failed to decode task %d: %w", taskID, err)
		}

		// Update task status
		task.Status = newStatus

		// Serialize back into DB
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(task); err != nil {
			return fmt.Errorf("failed to encode updated task %d: %w", taskID, err)
		}
		return tasksBucket.Put(taskKey, buf.Bytes())
	})

	if err != nil {
		log.Printf("⚠️ UpdateTaskStatus failed: %v", err)
		return &pb.Ack{Success: false}, err
	}

	log.Printf("✅ Task %d updated to status %s", taskID, newStatus)
	return &pb.Ack{Success: true}, nil
}
