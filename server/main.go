package main

import (
    "context"
    "encoding/gob"
    "bytes"
    "log"
    "net"
    "sync"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    bolt "go.etcd.io/bbolt"

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
