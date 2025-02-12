package main

import (
	"flag"
	"log"
	"sync"

	"github.com/gmtsciencedev/scitq2/server"
	"github.com/gmtsciencedev/scitq2/server/config"
)

func main() {
	config_file := flag.String("config", "", "Configuration file")

	cfg, err := config.LoadConfig(*config_file)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Launch the HTTP server as a goroutine.
	go func() {
		defer wg.Done()
		if err := server.HttpServer(*cfg); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Launch the gRPC server as a goroutine.
	go func() {
		defer wg.Done()
		if err := server.Serve(*cfg); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Wait for both servers to exit (which should not happen in normal operation).
	wg.Wait()
}
