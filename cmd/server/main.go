package main

import (
	"log"

	"github.com/gmtsciencedev/scitq2/server"
)

func main() {
	defaultDBURL := "postgres://scitq_user:dsofposiudipopipII9@localhost/scitq2?sslmode=disable"
	defaultLogRoot := "log"
	defaultPort := 50051

	if err := server.Serve(defaultDBURL, defaultLogRoot, defaultPort); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
