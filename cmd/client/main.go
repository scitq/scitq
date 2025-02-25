package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/gmtsciencedev/scitq2/client"
	"github.com/gmtsciencedev/scitq2/client/install"
	"github.com/google/uuid"
)

// main initializes the client.
func main() {
	// Parse command-line arguments
	serverAddr := flag.String("server", "localhost:50051", "gRPC server address")
	concurrency := flag.Int("concurrency", 1, "Number of concurrent tasks")

	// Get the hostname
	hostname, err := os.Hostname()
	if err != nil {
		// Generate a UUID if hostname retrieval fails
		id := uuid.New()
		hostname = fmt.Sprintf("workerId%s", id.String())
	}

	name := flag.String("name", hostname, "Worker name")
	do_install := flag.Bool("install", false, "Perform automatic install check")
	dockerStr := flag.String("docker", ":", "Docker private registry configuration")
	swapProportion := flag.Float64("swap", 0.1, "Add automatically a swapfile in scratch of this proportion (0 to disable)")

	flag.Parse()

	if *do_install {

		dockerCfg := strings.Split(*dockerStr, ":")
		var dockerRegistry string
		var dockerAuthentication string
		if len(dockerCfg) == 2 && dockerCfg[0] != "" && dockerCfg[1] != "" {
			dockerRegistry = dockerCfg[0]
			dockerAuthentication = dockerCfg[1]
		}

		err := install.Run(dockerRegistry, dockerAuthentication, float32(*swapProportion), *serverAddr, *concurrency)
		if err != nil {
			log.Fatalf("Could not perform install: %v", err)
		}
	}

	client.Run(*serverAddr, *concurrency, *name)
}
