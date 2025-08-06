package main

import (
	"flag"
	"log"
	"sync"
	"time"

	"github.com/scitq/scitq/server"
	"github.com/scitq/scitq/server/config"
	"github.com/scitq/scitq/server/updater/run"
)

// runUpdater wraps the updater call with a recover() so that panics don't crash the application.
func runUpdater(cfg config.Config, providerCfg config.ProviderConfig) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Updater for provider %s recovered from panic: %v", providerCfg.GetName(), r)
		}
	}()

	if err := run.Run(cfg, providerCfg); err != nil {
		log.Printf("Updater for provider %s error: %v", providerCfg.GetName(), err)
	}
}

func main() {
	config_file := flag.String("config", "", "Configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*config_file)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// // Launch the HTTP server as a goroutine.
	// go func() {
	// 	defer wg.Done()
	// 	if _, err := server.HttpServer(*cfg); err != nil {
	// 		log.Fatalf("HTTP server error: %v", err)
	// 	}
	// }()

	// Launch the gRPC server as a goroutine.
	go func() {
		defer wg.Done()
		if err := server.Serve(*cfg); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	// Iterate over all provider configurations.
	// Only launch the updater if GetUpdatePeriodicity() is non-nil.
	for _, providerCfg := range cfg.GetProviders() {
		if periodicity := providerCfg.GetUpdatePeriodicity(); periodicity != 0 {

			go func(pc config.ProviderConfig) {
				ticker := time.NewTicker(pc.GetUpdatePeriodicity())
				defer ticker.Stop()

				// Run the updater once immediately.
				runUpdater(*cfg, pc)

				// Then run periodically.
				for range ticker.C {
					runUpdater(*cfg, pc)
				}
			}(providerCfg)

		}
	}

	// Wait for both servers to exit (which should not happen in normal operation).
	wg.Wait()
}
