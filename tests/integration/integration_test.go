package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crypto/rand"

	"github.com/docker/go-connections/nat"
	"github.com/scitq/scitq/cli"
	"github.com/scitq/scitq/client"
	"github.com/scitq/scitq/lib"
	"github.com/scitq/scitq/server"
	"github.com/scitq/scitq/server/config"
	"golang.org/x/crypto/bcrypt"

	"github.com/creasty/defaults"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	pb "github.com/scitq/scitq/gen/taskqueuepb"

	"database/sql"

	"github.com/stretchr/testify/assert"

	_ "github.com/lib/pq"
)

var serverStartMu sync.Mutex

func captureOutput(f func()) string {
	// Create a pipe to capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	// Run the function that prints output
	f()

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// RunRawQuery executes an arbitrary SQL query and prints the raw output
// use like this: RunRawQuery(dbURL, "SELECT task_id, command, container, status::text FROM task ORDER BY task_id")
func RunRawQuery(dbURL, query string) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to test DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		log.Fatalf("Failed to execute query: %v", err)
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		log.Fatalf("Failed to get columns: %v", err)
	}

	// Create a slice of interface{} to hold row values
	values := make([]interface{}, len(columns))
	valuePtrs := make([]interface{}, len(columns))

	// Print column headers (raw output)
	//fmt.Println(columns)
	fmt.Println(strings.Join(columns, " | "))

	for rows.Next() {
		// Assign pointers to the values
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan the row into valuePtrs
		err := rows.Scan(valuePtrs...)
		if err != nil {
			log.Fatalf("Failed to scan row: %v", err)
		}

		// Print raw row output
		//fmt.Println(values)
		// Convert values to string and print them with "|"
		strValues := make([]string, len(values))
		for i, v := range values {
			if v != nil {
				strValues[i] = fmt.Sprintf("%v", v)
			} else {
				strValues[i] = "NULL"
			}
		}
		fmt.Println(strings.Join(strValues, " | "))
	}

	if err = rows.Err(); err != nil {
		log.Fatalf("Row iteration error: %v", err)
	}
}

// SendRawGRPCRequest connects to the server and sends a `ListTasks` request
// use like this: 	SendRawGRPCRequest(server_connection_string)
func SendRawGRPCRequest(serverAddr, token string) {
	// Set up a gRPC connection
	qcclient, err := lib.CreateClient(serverAddr, token)
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	defer qcclient.Close()

	// Prepare request
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &pb.ListTasksRequest{}

	// Send gRPC request
	res, err := qcclient.Client.ListTasks(ctx, req)
	if err != nil {
		log.Fatalf("gRPC ListTasks request failed: %v", err)
	}

	// Print raw response
	fmt.Println("🛠️ DEBUG: Raw gRPC Response:")
	for _, task := range res.Tasks {
		fmt.Printf("ID: %d | Command: %s | Container: %s | Status: %s\n",
			task.TaskId, task.Command, task.Container, task.Status)
	}
}

func runCLICommand(c cli.CLI, args []string) (string, error) {
	server, timeout, token := c.Attr.Server, c.Attr.TimeOut, c.Attr.Token
	c2 := cli.CLI{
		Attr: cli.Attr{
			Server:  server,
			TimeOut: timeout,
			Token:   token,
		},
	}
	//c.Attr = cli.Attr{Server: server, TimeOut: timeout, Token: token} // Reset before parsing
	os.Args = append([]string{"scitq-cli"}, args...)

	var err error
	output := captureOutput(func() {
		err = cli.Run(c2) // Generic CLI entry point if available
	})
	return output, err
}

// startServerForTest boots a Postgres testcontainer and starts the scitq server.
// It returns the gRPC address, the worker token, admin credentials, and a cleanup func.
func startServerForTest(t *testing.T, override *config.Config) (serverAddr, workerToken, adminUser, adminPassword string, cleanup func()) {
	t.Helper()
	serverStartMu.Lock()
	defer serverStartMu.Unlock()

	// Define the PostgreSQL test container with proper readiness check
	req := testcontainers.ContainerRequest{
		Image:        "postgres:17-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":        "test",
			"POSTGRES_PASSWORD":    "test",
			"POSTGRES_DB":          "scitq_test",
			"POSTGRES_INITDB_ARGS": "--encoding=UTF8",
		},
		WaitingFor: wait.ForSQL("5432/tcp", "postgres", func(host string, port nat.Port) string {
			return fmt.Sprintf("postgres://test:test@%s:%s/scitq_test?sslmode=disable&client_encoding=UTF8", host, port.Port())
		}).WithStartupTimeout(15 * time.Second),
	}

	// Start container
	pgContainer, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Retrieve host and mapped port
	host, _ := pgContainer.Host(context.Background())
	pgPort, _ := pgContainer.MappedPort(context.Background(), "5432/tcp")

	// Construct DB URL
	dbURL := fmt.Sprintf("postgres://test:test@%s:%s/scitq_test?sslmode=disable", host, pgPort.Port())
	fmt.Println("Using Database URL:", dbURL)

	// Temp dirs (unique per test)
	baseTmp := t.TempDir()
	tempLogRoot := filepath.Join(baseTmp, "logs")
	if err := os.MkdirAll(tempLogRoot, 0o755); err != nil {
		t.Fatalf("failed to create temp log root: %v", err)
	}
	tempScriptRoot := filepath.Join(baseTmp, "scripts")
	if err := os.MkdirAll(tempScriptRoot, 0o755); err != nil {
		t.Fatalf("failed to create temp script root: %v", err)
	}
	tempPythonEnv := filepath.Join(baseTmp, "python")
	if err := os.MkdirAll(tempPythonEnv, 0o755); err != nil {
		t.Fatalf("failed to create temp python env root: %v", err)
	}

	// Pick a free server port to avoid collisions between tests
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot grab free port: %v", err)
	}
	serverPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	// Generate secrets
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("Failed to generate random token: %v", err)
	}
	workerToken = fmt.Sprintf("test-worker-%x", b)
	jwtSecret := fmt.Sprintf("test-jwt-secret-%x", b)
	adminUser = "admin"
	adminPassword = fmt.Sprintf("secret-%x", b)
	adminHashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("Failed to generate admin hashed password: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start server
	go func() {
		var cfg config.Config

		if override != nil {
			cfg = *override
		}

		cfg.Scitq.DBURL = dbURL
		cfg.Scitq.Port = serverPort
		cfg.Scitq.ServerFQDN = "localhost"
		cfg.Scitq.LogLevel = "debug"
		cfg.Scitq.LogRoot = tempLogRoot
		cfg.Scitq.WorkerToken = workerToken
		cfg.Scitq.JwtSecret = jwtSecret
		cfg.Scitq.ScriptRoot = tempScriptRoot
		cfg.Scitq.ScriptVenv = tempPythonEnv
		cfg.Scitq.RecruitmentInterval = 2
		cfg.Scitq.AdminUser = adminUser
		cfg.Scitq.AdminHashedPassword = string(adminHashedPassword)
		cfg.Scitq.DisableHTTPS = true
		cfg.Scitq.DisableGRPCWeb = true
		defaults.Set(&cfg)
		if err := server.Serve(cfg, ctx, cancel); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Println("🛑 Serve() exited normally due to context cancellation")
			} else {
				log.Fatalf("Server failed: %v", err)
			}
		}
	}()

	// allow server to start
	time.Sleep(2 * time.Second)

	serverAddr = fmt.Sprintf("localhost:%d", serverPort)

	// Cleanup func closes container and temp dirs
	cleanup = func() {
		log.Println("🧼 Server cleanup called")
		cancel()
		time.Sleep(200 * time.Millisecond)
		_ = pgContainer.Terminate(context.Background())
		// t.TempDir() handles cleanup of temp subdirs
	}

	t.Logf("🏁 Starting server with address %s token %s admin %s and pass %s",
		serverAddr, workerToken, adminUser, adminPassword)
	return serverAddr, workerToken, adminUser, adminPassword, cleanup
}

// startClientForTest launches a worker client with an isolated temp store.
// Returns a cleanup func and the store path (if you need to inspect it).
func startClientForTest(t *testing.T, serverAddr, workerName, workerToken string, concurrency int) (cleanup func(), store string) {
	t.Helper()

	baseTmp := t.TempDir()
	store = filepath.Join(baseTmp, "client_store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatalf("failed to create temp client store: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		client.Run(ctx, serverAddr, int32(concurrency), workerName, store, workerToken, true, nil, nil)
	}()

	// t.TempDir() handles cleanup
	cleanup = func() {
		log.Printf("🧼 Client %s cleanup called", workerName)
		cancel()
	}
	return cleanup, store
}

func TestIntegration(t *testing.T) {

	server_connection_string, workerToken, adminUser, adminPassword, cleanup := startServerForTest(t, nil)
	defer cleanup()

	//////////////////////////////////////////////////////////////////////////////
	//
	//                  CLI
	//
	//////////////////////////////////////////////////////////////////////////////

	// Initializing CLI
	var c cli.CLI
	c.Attr.Server = server_connection_string

	// login and recover token
	output, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	assert.NoError(t, err)
	token := strings.TrimSpace(output)
	assert.NotEmpty(t, token)
	c.Attr.Token = token

	// creating Task
	_, err = runCLICommand(c, []string{"task", "create", "--container", "ubuntu", "--command", "ls -la"})
	assert.NoError(t, err)

	// looking up Task
	output, err = runCLICommand(c, []string{"task", "list", "--status", "P"})
	assert.NoError(t, err)
	assert.Contains(t, output, "🆔 ID: 1 | Command: ls -la | Container: ubuntu | Status: P")

	//////////////////////////////////////////////////////////////////////////////
	//
	//                  Client
	//
	//////////////////////////////////////////////////////////////////////////////

	// launch client
	cleanupClient, _ := startClientForTest(t, server_connection_string, "test-worker-1", workerToken, 1)
	defer cleanupClient()

	// Allow some time for the client to start && register
	time.Sleep(1 * time.Second)

	// looking up Worker
	output, err = runCLICommand(c, []string{"worker", "list"})
	assert.NoError(t, err)
	assert.Contains(t, output, "👷 Worker List:\n🔹 ID: 1 | Name: test-worker-1 | Concurrency: 1")

	// looking up Task and check status is now S (allow a short convergence window)
	var ok bool
	for i := 0; i < 30; i++ { // 20 * 500ms = ~10s max wait
		output, err = runCLICommand(c, []string{"task", "list"})
		assert.NoError(t, err)
		if strings.Contains(output, "🆔 ID: 1 | Command: ls -la | Container: ubuntu | Status: S") {
			ok = true
			log.Println("Task 1 passed")
			break
		}
		log.Printf("Waiting for task to reach status S, current output:\n%s", output)
		time.Sleep(500 * time.Millisecond)
	}
	if !ok {
		// show what we saw last to aid debugging
		t.Fatalf("task did not reach S; last output:\n%s", output)
	}

	// look for task output
	output, err = runCLICommand(c, []string{"task", "output", "--id", "1"})
	assert.NoError(t, err)
	assert.Contains(t, output, "sbin -> usr/sbin")

	// test failing task
	_, err = runCLICommand(c, []string{"task", "create", "--container", "ubuntu", "--command", "ls non-existing-file"})
	assert.NoError(t, err)
	// Allow some time for the client to accept and execute task
	time.Sleep(5 * time.Second)
	// looking up Task and check status is now S (allow a short convergence window)
	ok = false
	for i := 0; i < 30; i++ { // 20 * 500ms = ~10s max wait
		output, err = runCLICommand(c, []string{"task", "list"})
		assert.NoError(t, err)
		if strings.Contains(output, "ID: 2 | Command: ls non-existing-file | Container: ubuntu | Status: F") {
			ok = true
			log.Println("Task 2 passed")
			break
		}
		log.Printf("Waiting for task to reach status S, current output:\n%s", output)
		time.Sleep(500 * time.Millisecond)
	}
	if !ok {
		// show what we saw last to aid debugging
		t.Fatalf("task did not reach S; last output:\n%s", output)
	}
}
