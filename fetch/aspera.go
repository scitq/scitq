package fetch

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/rclone/rclone/fs"
)

// AsperaBackend represents an Aspera (FASP) backend that supports only copying to LocalBackend.
type AsperaBackend struct{}

// NewAsperaBackend initializes an AsperaBackend.
func NewAsperaBackend() *AsperaBackend {
	return &AsperaBackend{}
}

// Copy implements the Copy method for AsperaBackend.
// Supports only copying to a local backend.
func (ab *AsperaBackend) Copy(otherFs FileSystemInterface, src, dst URI, selfIsSource bool) error {
	if !selfIsSource {
		return fmt.Errorf("AsperaBackend can only be used as a source, not a destination")
	}

	v, ok := otherFs.(*LocalBackend)
	if !ok {
		return fmt.Errorf("AsperaBackend only supports copying to a local backend")
	}

	absPath, err := v.AbsolutePath(dst.Path)
	if err != nil {
		return fmt.Errorf("AsperaBackend could not get output folder right: %w", err)
	}

	// Validate that the source is a single file (no directories)
	if src.File == "" {
		return fmt.Errorf("directory fetching is not supported for fasp:// URIs")
	}

	// Extract Aspera components from URI
	user := src.User
	server := src.Component
	filePath := src.Path + "/" + src.File

	//log.Printf("Aspera downloading %s to %s", src, destPath)

	// Ensure directory exists
	if err := os.MkdirAll(dst.Path, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %v", err)
	}

	if len(server) == 0 {
		return fmt.Errorf("empty server not possible")
	}
	if server[len(server)-1] == ':' {
		server = server[:len(server)-1]
	}

	// Construct Aspera command
	cmdArgs := []string{
		"--rm",
		"-v", absPath + ":/output",
		"martinlaurent/ascli:4.18.0",
		"--url=ssh://" + server + ":33001",
		"--username=" + user,
		"--ssh-keys=/aspera_sdk/aspera_bypass_dsa.pem",
		"--ts=@json:{\"target_rate_kbps\":300000}",
		"server",
		"download", filePath,
		"--to-folder=/output/",
	}

	// Running the command
	cmd := exec.Command("docker", append([]string{"run"}, cmdArgs...)...)

	output, err := cmd.CombinedOutput()
	//fmt.Println("Command output:", string(output))

	if err != nil {
		return fmt.Errorf("AsperaBackend download failure %s -> %s : %v\n%s", src, dst, err, output)
	}

	// Rename file if needed
	if dst.File != "" && dst.File != src.File {
		oldPath := dst.Path + dst.Separator + src.File
		newPath := dst.CompletePath()
		log.Printf("Renaming %s -> %s", oldPath, newPath)
		err = os.Rename(oldPath, newPath)
		if err != nil {
			return fmt.Errorf("AsperaBackend failed to rename downloaded file %s -> %s : %v", oldPath, newPath, err)
		}
	}

	return nil
}

func (ab *AsperaBackend) List(path string) (fs.DirEntries, error) {
	return nil, fmt.Errorf("AsperaBackend does not support list")
}

func (rb *AsperaBackend) Mkdir(path string) error {
	return fmt.Errorf("AsperaBackend does not support Mkdir")
}

func (rb *AsperaBackend) Info(path string) (fs.DirEntry, error) {
	return nil, fmt.Errorf("AsperaBackend does not support Info")
}
