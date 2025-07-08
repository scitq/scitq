package main

import (
	"fmt"

	"github.com/scitq/scitq/cli"
)

func main() {
	var c cli.CLI
	err := cli.Run(c)

	if err != nil {
		fmt.Printf("Command failed because %v", err)
	}
}
