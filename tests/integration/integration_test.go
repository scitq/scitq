package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
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
	server, timeout := c.Attr.Server, c.Attr.TimeOut
	c.Attr = cli.Attr{Server: server, TimeOut: timeout} // Reset before parsing
	os.Args = append([]string{"scitq-cli"}, args...)

	var err error
	output := captureOutput(func() {
		err = cli.Run(c) // Generic CLI entry point if available
	})
	return output, err
}

func TestIntegration(t *testing.T) {

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

	// Start the container **after** defining the readiness strategy
	pgContainer, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer pgContainer.Terminate(context.Background())

	// Retrieve the actual host and mapped port
	host, _ := pgContainer.Host(context.Background())
	pgPort, _ := pgContainer.MappedPort(context.Background(), "5432/tcp")

	// Construct the final database URL
	dbURL := fmt.Sprintf("postgres://test:test@%s:%s/scitq_test?sslmode=disable", host, pgPort.Port())

	// Print DB connection for debugging
	fmt.Println("Using Database URL:", dbURL)

	// Create temporary log directory
	tempLogRoot := "./tmp_logs"
	os.MkdirAll(tempLogRoot, 0755)
	defer os.RemoveAll(tempLogRoot)

	// Create temporary script_root
	tempScriptRoot := "./tmp_scripts"
	os.MkdirAll(tempScriptRoot, 0755)
	defer os.RemoveAll(tempScriptRoot)

	// Use a different port for the test server
	serverPort := 50052

	// generate a random token and random jwtSecret
	b := make([]byte, 4)
	_, err = rand.Read(b)
	if err != nil {
		t.Fatalf("Failed to generate random token: %v", err)
	}
	workerToken := fmt.Sprintf("test-worker-%x", b)
	jwtSecret := fmt.Sprintf("test-jwt-secret-%x", b)
	adminUser := "admin"
	adminPassword := fmt.Sprintf("secret-%x", b)
	adminHashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("Failed to generate admin hashed password: %v", err)
	}

	// Start server in a separate goroutine
	go func() {
		var cfg config.Config
		cfg.Scitq.DBURL = dbURL
		cfg.Scitq.Port = serverPort
		cfg.Scitq.LogLevel = "debug"
		cfg.Scitq.LogRoot = tempLogRoot
		cfg.Scitq.WorkerToken = workerToken
		cfg.Scitq.JwtSecret = jwtSecret
		cfg.Scitq.ScriptRoot = tempScriptRoot
		cfg.Scitq.ScriptInterpreter = "/usr/bin/python3"
		cfg.Scitq.RecruitmentInterval = 2
		cfg.Scitq.AdminUser = adminUser
		cfg.Scitq.AdminHashedPassword = string(adminHashedPassword)
		defaults.Set(&cfg) // ✅ apply struct tag defaults here
		if err := server.Serve(cfg); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Allow some time for the server to start
	time.Sleep(2 * time.Second)

	//////////////////////////////////////////////////////////////////////////////
	//
	//                  CLI
	//
	//////////////////////////////////////////////////////////////////////////////

	// Initializing CLI
	server_connection_string := fmt.Sprintf("localhost:%d", serverPort)
	var c cli.CLI
	c.Attr.Server = server_connection_string

	// login and recover token
	output, err := runCLICommand(c, []string{"login", "--user", adminUser, "--password", adminPassword})
	assert.NoError(t, err)
	token := strings.TrimSpace(output)
	assert.NotEmpty(t, token)
	// Export the token so future CLI calls can use it
	os.Setenv("SCITQ_TOKEN", token)

	// creating Task
	_, err = runCLICommand(c, []string{"task", "create", "--container", "ubuntu", "--command", "ls -la"})
	assert.NoError(t, err)

	// looking up Task
	output, err = runCLICommand(c, []string{"task", "list", "--status", "P"})
	assert.NoError(t, err)
	assert.Contains(t, output, "📋 Task List:\n🆔 ID: 1 | Command: ls -la | Container: ubuntu | Status: P\n")

	//////////////////////////////////////////////////////////////////////////////
	//
	//                  Client
	//
	//////////////////////////////////////////////////////////////////////////////

	tempStorage := "./tmp_client_store"
	os.MkdirAll(tempStorage, 0755)
	defer os.RemoveAll(tempStorage)

	// launch client
	go func() {
		client.Run(server_connection_string, 1, "test-worker-1", tempStorage, workerToken)
		if err != nil {
			log.Fatalf("Server crashed: %v", err)
		}
	}()

	// Allow some time for the client to start && register
	time.Sleep(1 * time.Second)

	// looking up Worker
	output, err = runCLICommand(c, []string{"worker", "list"})
	assert.NoError(t, err)
	assert.Contains(t, output, "👷 Worker List:\n🔹 ID: 1 | Name: test-worker-1 | Concurrency: 1")

	// looking up Task and check status is now S (allow a short convergence window)
	var ok bool
	for i := 0; i < 20; i++ { // 20 * 500ms = ~10s max wait
		output, err = runCLICommand(c, []string{"task", "list"})
		assert.NoError(t, err)
		if strings.Contains(output, "📋 Task List:\n🆔 ID: 1 | Command: ls -la | Container: ubuntu | Status: S\n") {
			ok = true
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
	time.Sleep(10 * time.Second)
	output, err = runCLICommand(c, []string{"task", "list"})
	assert.NoError(t, err)
	assert.Contains(t, output, "ID: 2 | Command: ls non-existing-file | Container: ubuntu | Status: F")

}
