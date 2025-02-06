package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/docker/go-connections/nat"
	"github.com/gmtsciencedev/scitq2/cli"
	"github.com/gmtsciencedev/scitq2/lib"
	"github.com/gmtsciencedev/scitq2/server"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stretchr/testify/assert"
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

func TestIntegration(t *testing.T) {

	// Define the PostgreSQL test container with proper readiness check
	req := testcontainers.ContainerRequest{
		Image:        "postgres:latest",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "scitq_test",
		},
		WaitingFor: wait.ForSQL("5432/tcp", "postgres", func(host string, port nat.Port) string {
			return fmt.Sprintf("postgres://test:test@%s:%s/scitq_test?sslmode=disable", host, port.Port())
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

	// Use a different port for the test server
	serverPort := 50052

	// Start server in a separate goroutine
	go func() {
		if err := server.Serve(dbURL, tempLogRoot, serverPort, "", "", ""); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Allow some time for the server to start
	time.Sleep(2 * time.Second)

	// Initializing CLI
	server_connection_string := fmt.Sprintf("localhost:%d", serverPort)
	var c cli.CLI
	qc, err := lib.CreateClient(server_connection_string)
	assert.NoError(t, err)
	c.QC = qc

	// creating Task
	os.Args = []string{"scitq-cli", "task", "create", "--container", "ubuntu", "--command", "ls -la"}
	arg.MustParse(&c.Attr)
	err = c.TaskCreate()
	assert.NoError(t, err)

	// looking up Task
	os.Args = []string{"scitq-cli", "task", "list", "--status", "P"}
	arg.MustParse(&c.Attr)
	output := captureOutput(func() {
		err = c.TaskList()
	})
	assert.NoError(t, err)
	assert.Contains(t, output, "📋 Task List:\n🆔 ID: 1 | Command: ls -la | Container: ubuntu | Status: P\n")

	// TODO: Launch client and CLI to interact with the server
}
