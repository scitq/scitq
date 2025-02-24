package install

import (
	"testing"
)

// TestExecuteShellCommand tests the executeShellCommand function.
func TestExecuteShellCommand(t *testing.T) {
	// Test case 1: Successful command
	command := "echo 'Hello, World!' | tr '[:lower:]' '[:upper:]'"
	expectedOutput := "HELLO, WORLD!\n"

	output, err := executeShellCommand(command)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if output != expectedOutput {
		t.Errorf("expected output %q, but got %q", expectedOutput, output)
	}

	// Test case 2: Command with error
	command = "nonexistentcommand"
	_, err = executeShellCommand(command)
	if err == nil {
		t.Error("expected error for nonexistent command, but got none")
	}
}

// TestExecuteCommands tests the executeCommands function with retries.
func TestExecuteCommands(t *testing.T) {
	// Test case 1: Successful commands
	commands := []string{
		"echo 'Hello, World!'",
		"echo 'Another command'",
	}
	err := executeCommands(3, commands...)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Test case 2: Command with error
	commands = []string{
		"echo 'This will fail'",
		"nonexistentcommand",
	}
	err = executeCommands(3, commands...)
	if err == nil {
		t.Error("expected error for nonexistent command, but got none")
	}
}
