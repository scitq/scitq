package integration_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/gmtsciencedev/scitq2/server"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestIntegration(t *testing.T) {
	// Start a temporary PostgreSQL instance
	req := testcontainers.ContainerRequest{
		Image:        "postgres:latest",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "scitq_test",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithStartupTimeout(10 * time.Second),
	}

	pgContainer, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer pgContainer.Terminate(context.Background())

	// Get DB connection details
	host, _ := pgContainer.Host(context.Background())
	pgPort, _ := pgContainer.MappedPort(context.Background(), "5432/tcp")
	dbURL := fmt.Sprintf("postgres://test:test@%s:%s/scitq_test?sslmode=disable", host, pgPort.Port())

	// Create temporary log directory
	tempLogRoot := "./tmp_logs"
	os.MkdirAll(tempLogRoot, 0755)
	defer os.RemoveAll(tempLogRoot)

	// Use a different port for the test server
	serverPort := 50052

	// Start server in a separate goroutine
	go func() {
		if err := server.Serve(dbURL, tempLogRoot, serverPort); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Allow some time for the server to start
	time.Sleep(2 * time.Second)

	// TODO: Launch client and CLI to interact with the server
}
