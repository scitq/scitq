//go:build !windows
// +build !windows

package cli

import "fmt"

func readConsoleLine(prompt string) (string, error) {
	return "", fmt.Errorf("readConsoleLine is only implemented on Windows")
}

func readConsolePassword(prompt string) (string, error) {
	return "", fmt.Errorf("readConsolePassword is only implemented on Windows")
}

func writeConsole(s string) {}
