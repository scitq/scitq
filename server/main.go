package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
)

// Helper function to convert uint64 to an 8-byte big-endian slice.
func itob(v uint32) []byte {
	b := make([]byte, 4) // Now 4 bytes instead of 8
	for i := uint(0); i < 4; i++ {
		b[3-i] = byte(v >> (i * 8)) // Store bytes in big-endian order
	}
	return b
}

func bytesToUint32(b []byte) uint32 {
	var v uint32
	if len(b) < 4 {
		return 0 // Safety check in case of an unexpected length
	}
	for i := uint(0); i < 4; i++ {
		v |= uint32(b[3-i]) << (i * 8) // Convert back from big-endian
	}
	return v
}

// getNextID retrieves the next sequential ID from BadgerDB
func getNextID(txn *badger.Txn, key []byte) (uint32, error) {
	// Retrieve current value
	item, err := txn.Get(key)
	if err != nil {
		if err == badger.ErrKeyNotFound {
			// First time: Start at 1
			err := txn.Set(key, itob(1))
			if err != nil {
				return 0, err
			}
			return 1, nil
		}
		return 0, err
	}

	// Decode current counter value
	var id uint32
	err = item.Value(func(val []byte) error { // 🔥 Explicitly return an error
		id = bytesToUint32(val)
		return nil // ✅ No error here
	})
	if err != nil {
		return 0, err
	}

	// Increment & update
	id++
	err = txn.Set(key, itob(id))
	if err != nil {
		return 0, err
	}

	return id, nil
}

// Task and Worker structures for storage
type Task struct {
	ID        uint32
	WorkerID  *uint32
	Command   string
	Container string
	Status    string
	CreatedAt time.Time
}

type Worker struct {
	ID          uint32
	Name        string
	Concurrency int
}

// taskQueueServer implements the gRPC service.
type taskQueueServer struct {
	pb.UnimplementedTaskQueueServer
	db *badger.DB
	mu sync.Mutex
}

// newTaskQueueServer initializes the BadgerDB-based task queue server.
func newTaskQueueServer(db *badger.DB) *taskQueueServer {
	s := &taskQueueServer{db: db}
	go s.assignTasksLoop() // Start task assignment loop
	return s
}

// encode encodes an object into bytes
func encode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(v)
	return buf.Bytes(), err
}

// decode decodes bytes into an object
func decode(data []byte, v interface{}) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// **TASK ASSIGNMENT LOOP**
func (s *taskQueueServer) assignTasksLoop() {
	for {
		time.Sleep(5 * time.Second) // Run every 5 seconds
		s.mu.Lock()

		err := s.db.Update(func(txn *badger.Txn) error {
			workers := make(map[uint32]*Worker)
			assignedCounts := make(map[uint32]int)

			// **1️⃣ Load all workers**
			it := txn.NewIterator(badger.DefaultIteratorOptions)
			defer it.Close()

			for it.Seek([]byte("workers/")); it.ValidForPrefix([]byte("workers/")); it.Next() {
				item := it.Item()
				err := item.Value(func(val []byte) error {
					var worker Worker
					err := decode(val, &worker)
					if err == nil {
						workers[worker.ID] = &worker
						assignedCounts[worker.ID] = 0 // Initialize assigned task count
					}
					return err
				})
				if err != nil {
					log.Printf("⚠️ Worker decode error: %v", err)
				}
			}

			// **2️⃣ Count assigned tasks (A, C, R) per worker**
			statusPrefixes := []string{"A", "C", "R"} // Active task states
			for _, status := range statusPrefixes {
				prefix := []byte(fmt.Sprintf("tasks_by_status/%s/", status))
				it.Seek(prefix)
				for it.ValidForPrefix(prefix) {
					taskIDStr := string(it.Item().Key()[len(prefix):]) // Extract TaskID from Key
					taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
					if err != nil {
						log.Printf("⚠️ Failed to parse task ID from index: %v", err)
						it.Next()
						continue
					}

					// Fetch task details
					taskKey := []byte(fmt.Sprintf("tasks/%d", taskID))
					taskItem, err := txn.Get(taskKey)
					if err != nil {
						log.Printf("⚠️ Task ID %d found in index but missing in tasks/", taskID)
						it.Next()
						continue
					}

					err = taskItem.Value(func(val []byte) error {
						var task Task
						if err := decode(val, &task); err != nil {
							log.Printf("⚠️ Task decode error: %v", err)
							return nil
						}
						if task.WorkerID != nil {
							assignedCounts[*task.WorkerID]++ // ✅ Count active task
						}
						return nil
					})

					if err != nil {
						log.Printf("⚠️ Badger read error: %v", err)
					}

					it.Next()
				}
			}

			// **3️⃣ Assign pending tasks (P → A)**
			it.Seek([]byte("tasks_by_status/P/"))
			for it.ValidForPrefix([]byte("tasks_by_status/P/")) {
				item := it.Item()
				taskIDStr := string(item.Key()[len("tasks_by_status/P/"):]) // Extract TaskID
				taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
				if err != nil {
					log.Printf("⚠️ Failed to parse task ID from index: %v", err)
					it.Next()
					continue
				}

				// Fetch the actual task
				taskKey := []byte(fmt.Sprintf("tasks/%d", taskID))
				taskItem, err := txn.Get(taskKey)
				if err != nil {
					log.Printf("⚠️ Task ID %d found in P but missing in tasks/", taskID)
					it.Next()
					continue
				}

				var task Task
				err = taskItem.Value(func(val []byte) error {
					return decode(val, &task)
				})
				if err != nil {
					log.Printf("⚠️ Task decode error: %v", err)
					it.Next()
					continue
				}

				// **Find an available worker**
				assigned := false
				for workerID, worker := range workers {
					if assignedCounts[workerID] < worker.Concurrency {
						task.Status = "A"
						task.WorkerID = &workerID
						updatedTaskBytes, _ := encode(task)

						// **Remove from "P", move to "A"**
						txn.Delete(item.Key())                                             // ❌ Remove from `P`
						txn.Set(taskKey, updatedTaskBytes)                                 // ✅ Update main task entry
						txn.Set([]byte(fmt.Sprintf("tasks_by_status/A/%d", task.ID)), nil) // ✅ Index in `A`

						log.Printf("✅ Assigned task %d to worker %d", task.ID, workerID)
						assignedCounts[workerID]++
						assigned = true
						break
					}
				}

				if !assigned {
					log.Printf("⚠️ No available worker for task %d", task.ID)
				}

				it.Next()
			}
			return nil
		})

		if err != nil {
			log.Printf("❌ Task assignment loop error: %v", err)
		}

		s.mu.Unlock()
	}
}

// **SubmitTask**
func (s *taskQueueServer) SubmitTask(ctx context.Context, req *pb.TaskRequest) (*pb.TaskResponse, error) {
	var taskID uint32

	err := s.db.Update(func(txn *badger.Txn) error {
		// **Generate new task ID**
		id, err := getNextID(txn, []byte("task_sequence"))
		if err != nil {
			return fmt.Errorf("failed to generate task ID: %w", err)
		}
		taskID = id // Assign task ID

		// **Create Task**
		task := Task{
			ID:        taskID,
			Command:   req.Command,
			Container: req.Container,
			Status:    "P",
			CreatedAt: time.Now(),
		}

		// **Encode Task**
		taskBytes, _ := encode(task)

		// **Store in Tasks Table (Primary Storage)**
		err = txn.Set([]byte(fmt.Sprintf("tasks/%d", taskID)), taskBytes)
		if err != nil {
			return fmt.Errorf("failed to store task: %w", err)
		}

		// **Store Task ID in Status Index (tasks_by_status/P/)**
		//taskIDBytes := itob(taskID) // Convert to bytes

		// **Store Task ID in Status Index (tasks_by_status/P/)**
		err = txn.Set([]byte(fmt.Sprintf("tasks_by_status/P/%d", taskID)), nil) // ✅ Key-only index
		if err != nil {
			return fmt.Errorf("failed to store task in status index: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to submit task: %w", err)
	}

	log.Printf("✅ Task %d submitted (Command: %s, Container: %s)", taskID, req.Command, req.Container)
	return &pb.TaskResponse{TaskId: taskID}, nil
}

// register worker
func (s *taskQueueServer) RegisterWorker(ctx context.Context, req *pb.WorkerInfo) (*pb.WorkerId, error) {
	var workerID uint32
	var existingWorker Worker

	err := s.db.Update(func(txn *badger.Txn) error {
		workerIndexKey := []byte(fmt.Sprintf("worker_index/%s", req.Name))

		// **Check if worker already exists**
		item, err := txn.Get(workerIndexKey)
		if err == nil {
			// Worker exists, retrieve ID
			err = item.Value(func(val []byte) error {
				workerID = bytesToUint32(val)
				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to read existing worker ID: %w", err)
			}

			// Fetch worker data
			workerKey := []byte(fmt.Sprintf("workers/%d", workerID))
			item, err = txn.Get(workerKey)
			if err != nil {
				return fmt.Errorf("worker ID %d found in index but missing in workers bucket", workerID)
			}

			err = item.Value(func(val []byte) error {
				return decode(val, &existingWorker)
			})
			if err != nil {
				return fmt.Errorf("failed to decode existing worker: %w", err)
			}
		} else {
			// **Create new worker**
			workerID, err = getNextID(txn, []byte("worker_sequence")) // Ensure ID is in the same transaction
			if err != nil {
				return fmt.Errorf("failed to generate worker ID: %w", err)
			}

			existingWorker = Worker{
				ID:          workerID,
				Name:        req.Name,
				Concurrency: int(*req.Concurrency),
			}

			// Store worker name -> ID mapping
			err = txn.Set(workerIndexKey, itob(workerID))
			if err != nil {
				return fmt.Errorf("failed to store worker index: %w", err)
			}
		}

		// **Ensure worker is properly stored**
		workerKey := []byte(fmt.Sprintf("workers/%d", workerID))
		workerBytes, _ := encode(existingWorker)
		err = txn.Set(workerKey, workerBytes)
		if err != nil {
			return fmt.Errorf("failed to store worker: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Printf("⚠️ Failed to register worker: %v", err)
		return nil, err
	}

	log.Printf("✅ Worker %s registered with ID %d", req.Name, workerID)
	return &pb.WorkerId{WorkerId: workerID}, nil
}

func main() {
	// Open BadgerDB
	db, err := badger.Open(badger.DefaultOptions("badgerdb"))
	if err != nil {
		log.Fatalf("Failed to open BadgerDB: %v", err)
	}
	defer db.Close()

	// Load TLS credentials.
	creds, err := credentials.NewServerTLSFromFile("server.pem", "server.key")
	if err != nil {
		log.Fatalf("failed to load TLS credentials: %v", err)
	}

	// Create gRPC server
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

func (s *taskQueueServer) PingAndTakeNewTasks(ctx context.Context, req *pb.WorkerId) (*pb.TaskListAndOther, error) {
	var tasks []*pb.Task
	var worker Worker

	err := s.db.View(func(txn *badger.Txn) error {
		// **1️⃣ Retrieve Worker Info**
		workerKey := []byte(fmt.Sprintf("workers/%d", req.WorkerId))
		item, err := txn.Get(workerKey)
		if err != nil {
			return fmt.Errorf("worker ID %d not found", req.WorkerId)
		}
		err = item.Value(func(val []byte) error {
			return decode(val, &worker)
		})
		if err != nil {
			return fmt.Errorf("failed to decode worker %d: %w", req.WorkerId, err)
		}

		// **2️⃣ Retrieve Assigned Tasks (Status 'A')**
		prefix := []byte("tasks_by_status/A/")
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			taskIDStr := string(item.Key()[len(prefix):]) // Extract TaskID from Key
			taskID, err := strconv.ParseUint(taskIDStr, 10, 32)
			if err != nil {
				log.Printf("⚠️ Failed to parse task ID from index: %v", err)
				it.Next()
				continue
			}

			// **Look up the actual task data**
			taskItem, err := txn.Get([]byte(fmt.Sprintf("tasks/%d", taskID)))
			if err != nil {
				log.Printf("⚠️ Task ID %d found in index but missing in tasks/", taskID)
				continue
			}

			err = taskItem.Value(func(val []byte) error {
				var task Task
				if err := decode(val, &task); err != nil {
					log.Printf("⚠️ Task decode error: %v", err)
					return nil
				}
				// **Ensure task is still "A"**
				if task.WorkerID != nil && *task.WorkerID == uint32(req.WorkerId) && task.Status == "A" {
					tasks = append(tasks, &pb.Task{
						TaskId:    uint32(task.ID),
						Command:   task.Command,
						Container: task.Container,
						Status:    task.Status,
					})
				}
				return nil
			})
			if err != nil {
				log.Printf("Badger read error: %v", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch tasks: %w", err)
	}

	return &pb.TaskListAndOther{Tasks: tasks, Concurrency: uint32(worker.Concurrency)}, nil
}

func (s *taskQueueServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.TaskList, error) {
	var tasks []*pb.Task
	statusFilter := req.StatusFilter

	err := s.db.View(func(txn *badger.Txn) error {
		// **If a status filter is applied, read from tasks_by_status/{status}**
		var prefix []byte
		if statusFilter != nil && *statusFilter != "" {
			prefix = []byte(fmt.Sprintf("tasks_by_status/%s/", *statusFilter))
		} else {
			prefix = []byte("tasks/") // Read all tasks
		}

		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var task Task
				if err := decode(val, &task); err != nil {
					log.Printf("Task decode error: %v", err)
					return nil
				}
				tasks = append(tasks, &pb.Task{
					TaskId:    uint32(task.ID),
					Command:   task.Command,
					Container: task.Container,
					Status:    task.Status,
				})
				return nil
			})
			if err != nil {
				log.Printf("Badger read error: %v", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return &pb.TaskList{Tasks: tasks}, nil
}

// UpdateTaskStatus now updates both the task and its indexed status.
// **UpdateTaskStatus**
func (s *taskQueueServer) UpdateTaskStatus(ctx context.Context, req *pb.TaskStatusUpdate) (*pb.Ack, error) {
	taskID := req.TaskId
	newStatus := req.NewStatus

	err := s.db.Update(func(txn *badger.Txn) error {
		taskKey := []byte(fmt.Sprintf("tasks/%d", taskID))

		item, err := txn.Get(taskKey)
		if err != nil {
			return fmt.Errorf("task %d not found", taskID)
		}

		var task Task
		err = item.Value(func(val []byte) error {
			return decode(val, &task)
		})
		if err != nil {
			return fmt.Errorf("failed to decode task %d: %w", taskID, err)
		}

		// **1️⃣ Remove Task from Old Status Index**
		oldStatusKey := []byte(fmt.Sprintf("tasks_by_status/%s/%d", task.Status, taskID))
		if err := txn.Delete(oldStatusKey); err != nil && err != badger.ErrKeyNotFound {
			log.Printf("⚠️ Failed to remove task %d from %s: %v", taskID, task.Status, err)
		}

		// **2️⃣ Update Task Status**
		oldStatus := task.Status
		task.Status = newStatus

		// **3️⃣ Store Updated Task in `tasks/{TaskID}` (Primary Table)**
		taskBytes, _ := encode(task)
		if err := txn.Set(taskKey, taskBytes); err != nil {
			return fmt.Errorf("failed to update task %d: %w", taskID, err)
		}

		// **4️⃣ Store Task ID in New Status Index (tasks_by_status/{NewStatus}/{TaskID})**
		newStatusKey := []byte(fmt.Sprintf("tasks_by_status/%s/%d", newStatus, taskID))
		if err := txn.Set(newStatusKey, nil); err != nil {
			return fmt.Errorf("failed to store task %d in new status %s: %w", taskID, newStatus, err)
		}

		log.Printf("🔄 Task %d transitioned %s → %s", taskID, oldStatus, newStatus)
		return nil
	})

	if err != nil {
		log.Printf("⚠️ UpdateTaskStatus failed: %v", err)
		return &pb.Ack{Success: false}, err
	}

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

		// Store log entry in BadgerDB under logs/{task_id}/{timestamp}
		err = s.db.Update(func(txn *badger.Txn) error {
			logKey := []byte(fmt.Sprintf("logs/%d/%d", logEntry.TaskId, time.Now().UnixNano())) // Unique timestamped key

			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(logEntry); err != nil {
				return fmt.Errorf("failed to encode log entry: %w", err)
			}

			return txn.Set(logKey, buf.Bytes()) // Store log entry in BadgerDB
		})

		if err != nil {
			log.Printf("❌ Failed to store log entry for task %d: %v", logEntry.TaskId, err)
		}
	}
}

func (s *taskQueueServer) StreamTaskLogs(req *pb.TaskId, stream pb.TaskQueue_StreamTaskLogsServer) error {
	taskID := req.TaskId

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := []byte(fmt.Sprintf("logs/%d/", taskID)) // Logs stored under logs/{task_id}/timestamp
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var logEntry pb.TaskLog
				if err := gob.NewDecoder(bytes.NewReader(val)).Decode(&logEntry); err != nil {
					log.Printf("❌ Log decode error for task %d: %v", taskID, err)
					return nil
				}

				// Send log entry to the gRPC stream
				if err := stream.Send(&logEntry); err != nil {
					log.Printf("❌ Failed to send log entry for task %d: %v", taskID, err)
					return err
				}
				return nil
			})
			if err != nil {
				log.Printf("❌ Badger read error for task %d: %v", taskID, err)
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
