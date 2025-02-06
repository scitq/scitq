package main

import (
	"log"

	"github.com/alexflint/go-arg"
	"github.com/gmtsciencedev/scitq2/cli"
	"github.com/gmtsciencedev/scitq2/lib"
)

func main() {
	var c cli.CLI
	arg.MustParse(&c.Attr)

	// Establish gRPC connection
	qc, err := lib.CreateClient(c.Attr.Server)
	if err != nil {
		log.Fatalf("Could not connect to server: %v", err)
	}
	c.QC = qc
	defer c.QC.Close()

	// Handle commands properly
	switch {
	// Task commands
	case c.Attr.Task != nil:
		switch {
		case c.Attr.Task.Create != nil:
			c.TaskCreate()
		case c.Attr.Task.List != nil:
			c.TaskList()
		case c.Attr.Task.Output != nil:
			c.TaskOutput()
		}
	// Worker commands
	case c.Attr.Worker != nil:
		switch {
		case c.Attr.Worker.List != nil:
			c.WorkerList()
		}
	default:
		log.Fatal("No command specified. Run with --help for usage.")
	}
}
