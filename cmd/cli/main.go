package main

import (
	"log"

	"github.com/alexflint/go-arg"
	"github.com/gmtsciencedev/scitq2/cli"
	"github.com/gmtsciencedev/scitq2/lib"
)

func main() {
	var args cli.CLI
	arg.MustParse(&args)

	// Establish gRPC connection
	qc, err := lib.CreateClient(args.Server)
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	args.QC = qc
	defer args.QC.Close()

	// Handle commands properly
	switch {
	// Task commands
	case args.Task != nil:
		switch {
		case args.Task.Create != nil:
			args.TaskCreate()
		case args.Task.List != nil:
			args.TaskList()
		case args.Task.Output != nil:
			args.TaskOutput()
		}
	// Worker commands
	case args.Worker != nil:
		switch {
		case args.Worker.List != nil:
			args.WorkerList()
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}
}
