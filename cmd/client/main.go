package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/scitq/scitq/client"
	"github.com/scitq/scitq/client/event"
	"github.com/scitq/scitq/client/install"
	_ "github.com/scitq/scitq/internal/banner" // logs "<app> <version>" on startup
	"github.com/scitq/scitq/internal/version"
	"github.com/scitq/scitq/lib"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

// sendInstallErrorWithRetry tries to connect and report a single 'install' error.
// Retries a few times with exponential backoff, then gives up quietly.
func sendInstallErrorWithRetry(serverAddr, token, workerName string, installErr error) {
	const (
		maxAttempts    = 5
		initialBackoff = 2 * time.Second
		maxBackoff     = 30 * time.Second
		initialRPCTime = 5 * time.Second
		maxRPCTime     = 20 * time.Second
	)

	backoff := initialBackoff
	rpcTimeout := initialRPCTime

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		qclient, connErr := lib.CreateClient(serverAddr, token)
		if connErr != nil {
			log.Printf("⚠️ [%d/%d] cannot connect to server to report install error: %v", attempt, maxAttempts, connErr)
		} else {
			func() {
				defer qclient.Close()
				ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
				defer cancel()
				event.ReportInstallError(qclient.Client, workerName, "install failed", installErr, ctx)
			}()
			// If we reached here, we consider the report done (best-effort).
			return
		}

		// Backoff before next attempt
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
		// Gradually increase per-RPC timeout too
		rpcTimeout += 5 * time.Second
		if rpcTimeout > maxRPCTime {
			rpcTimeout = maxRPCTime
		}
	}
	// After exhausting attempts, give up silently (we’ll still exit fatally upstream).
}

func makeJobReporter(serverAddr, token string, jobID uint32, workerName string) install.Reporter {
	return func(prog int, msg string) {
		// fire-and-forget with small retries; don’t block install too much
		go func(p int, m string) {
			const maxAttempts = 3
			backoff := 2 * time.Second
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				qclient, connErr := lib.CreateClient(serverAddr, token)
				if connErr != nil {
					time.Sleep(backoff)
					backoff *= 2
					if backoff > 10*time.Second {
						backoff = 10 * time.Second
					}
					continue
				}
				func() {
					defer qclient.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					// clamp progression
					if p < 0 {
						p = 0
					}
					if p > 100 {
						p = 100
					}
					_, err := qclient.Client.UpdateJob(ctx, &pb.JobUpdate{
						JobId:       jobID,
						Progression: uint32Ptr(uint32(p)),
						AppendLog:   &m,
					})
					_ = err // best-effort; swallow on failure
				}()
				return
			}
		}(prog, msg)
	}
}

func reportJobStatus(serverAddr, token string, jobID uint32, status, msg string) {
	// final status setter (F or S), with a last log line
	qclient, err := lib.CreateClient(serverAddr, token)
	if err != nil {
		return
	}
	defer qclient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	_, _ = qclient.Client.UpdateJob(ctx, &pb.JobUpdate{
		JobId:     jobID,
		Status:    &status,
		AppendLog: &msg,
		// set 100 on success, leave as-is on fail
		Progression: func() *uint32 {
			if status == "S" {
				v := uint32(100)
				return &v
			}
			return nil
		}(),
	})
}

func uint32Ptr(v uint32) *uint32 { return &v }

// main initializes the client.
func main() {
	// Parse command-line arguments
	serverAddr := flag.String("server", "localhost:50051", "gRPC server address")
	concurrency := flag.Uint("concurrency", 1, "Number of concurrent tasks")
	store := flag.String("store", "/scratch", "Path to the store directory")
	token := flag.String("token", "", "Token for authentication")
	jobID := flag.Uint("job", 0, "Job ID for this deployment (for progress reporting)")
	version_flag := flag.Bool("version", false, "Print version information and exit")

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

	if *version_flag {
		fmt.Println(version.Full())
		return
	}

	if *do_install {

		// Build a reporter only if jobID > 0
		var reporter install.Reporter
		if *jobID != 0 {
			reporter = makeJobReporter(*serverAddr, *token, uint32(*jobID), *name)
			// optional: initial "started" progress
			reporter(5, "install: started")
		}

		dockerCfg := strings.Split(*dockerStr, ":")
		var dockerRegistry string
		var dockerAuthentication string
		if len(dockerCfg) == 2 && dockerCfg[0] != "" && dockerCfg[1] != "" {
			dockerRegistry = dockerCfg[0]
			dockerAuthentication = dockerCfg[1]
		}

		err := install.Run(dockerRegistry, dockerAuthentication, float32(*swapProportion), *serverAddr, int(*concurrency), *token, reporter)
		if err != nil {
			if reporter != nil {
				// best-effort final failure (include error text)
				reporter(0, "install: failed: "+err.Error())
				// also mark job failed
				reportJobStatus(*serverAddr, *token, uint32(*jobID), "F", "install failed")
			}
			// Best-effort: send one worker_event with retries before exiting.
			sendInstallErrorWithRetry(*serverAddr, *token, *name, err)
			// Preserve original behavior: fatal log & exit non-zero.
			log.Fatalf("Could not perform install: %v", err)
		}
	}

	// Start the client
	ctx := context.Background()
	client.Run(ctx, *serverAddr, uint32(*concurrency), *name, *store, *token)
}
