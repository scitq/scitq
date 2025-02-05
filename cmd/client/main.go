package main

import (
	"flag"

	"github.com/gmtsciencedev/scitq2/client"
)

// main initializes the client.
func main() {
	// Parse command-line arguments
	serverAddr := flag.String("server", "localhost:50051", "gRPC server address")
	concurrency := flag.Int("concurrency", 2, "Number of concurrent tasks")
	name := flag.String("name", "worker-1", "Worker name")
	flag.Parse()

	client.Run(*serverAddr, *concurrency, *name)
}
