package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/alexflint/go-arg"
	"github.com/scitq/scitq/fetch"
)

// Define CLI arguments
type Args struct {
	Command      string `arg:"positional,required" help:"Command to execute (copy or ls)"`
	Src          string `arg:"positional,required" help:"Source URI"`
	Dst          string `arg:"positional" help:"Destination URI (required for copy)"`
	RcloneConfig string `arg:"--config" default:"/etc/rclone.conf" `
}

// expandPath expands a leading ~ in a file path to the user's home directory.
func expandPath(path string) (string, error) {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[1:]), nil
	}
	return path, nil
}

func main() {
	var args Args
	arg.MustParse(&args)

	rcloneConfigString := args.RcloneConfig
	rcloneConfig, err := expandPath(rcloneConfigString)
	if err != nil {
		log.Fatalf("Could not understand Rclone config path %s", rcloneConfigString)
	}

	op, err := fetch.NewOperation(rcloneConfig, args.Src, args.Dst)
	defer fetch.CleanOperation(op)
	if err != nil {
		log.Fatalf("Could not initiate %s operation %v", args.Command, err)
	}

	switch args.Command {
	case "copy":
		if args.Dst == "" {
			log.Fatal("Error: Destination is required for copy")
		}
		err = op.Copy()
		if err != nil {
			log.Fatalf("Copy failed: %v", err)
		}
		fmt.Println("Copy successful:", args.Src, "→", args.Dst)

	case "ls":
		files, err := op.List()
		if err != nil {
			log.Fatalf("List failed: %v", err)
		}
		for _, file := range files {
			fileName := op.SrcBase() + file.String()
			if fetch.IsDir(file) {
				fileName += "/"
			}
			fmt.Println(fileName)
		}

	default:
		log.Fatalf("Unknown command: %s", args.Command)
	}
}
