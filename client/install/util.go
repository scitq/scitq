package install

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// executeShellCommand runs a shell command and returns its output or error.
func executeShellCommand(command string) (string, error) {
	// Create a new command with the shell
	cmd := exec.Command("sh", "-c", command)

	// Create buffers to capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the command
	err := cmd.Run()

	// Check for errors
	if err != nil {
		// If there's an error, return the stderr output
		return "", fmt.Errorf("command failed: %s, error: %v", stderr.String(), err)
	}

	// Return the stdout output
	return stdout.String(), nil
}

func countCommand(command string) (int, error) {
	// Execute the command
	output, err := executeShellCommand(command)
	if err != nil {
		return -1, err
	}

	// Convert the output to an integer
	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return -1, fmt.Errorf("failed to convert output to integer: %v", err)
	}

	return count, nil
}

// isPackageInstalled checks if a package is installed on the system.
func isPackageInstalled(packageName string) (bool, error) {
	count, err := countCommand(fmt.Sprintf("dpkg -l | grep -E '^ii +%s' | wc -l", packageName))
	if err != nil {
		return false, err
	}
	if count >= 1 {
		log.Printf("Package %s is installed", packageName)
	} else {
		log.Printf("Package %s is not installed", packageName)
	}
	// Return true if the package is installed
	return count >= 1, nil
}

// executeCommands executes a series of commands with retries and stops on the first error.
func executeCommands(retryCount int, commands ...string) error {
	for _, command := range commands {
		var lastErr error
		for i := 0; i < retryCount; i++ {
			_, err := executeShellCommand(command)
			if err == nil {
				// Command succeeded, break out of the retry loop
				break
			}
			lastErr = err
			// Wait before retrying with exponential backoff
			time.Sleep(time.Second * 2 << i)
		}
		if lastErr != nil {
			// If all retries failed, return the last error
			return lastErr
		}
	}
	return nil
}

// check if a file does not exist
func fileNotExist(filePath string) bool {
	_, err := os.Stat(filePath)
	return os.IsNotExist(err)
}

// write a certain content to a certain file, overwriting if needed
func writeFile(filePath string, fileContent string, overwrite bool) error {
	// Check if the file exists
	if fileNotExist(filePath) || overwrite {
		// Open the file for writing, create it if it doesn't exist, and truncate it if it does
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}
		defer file.Close()

		// Write the content to the file
		_, err = file.WriteString(fileContent)
		if err != nil {
			return err
		}

		return nil
	} else if !overwrite {
		return fmt.Errorf("file %s already exists and overwrite is set to false", filePath)
	}

	return nil
}
