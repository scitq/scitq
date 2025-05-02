//go:build windows
// +build windows

package cli

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func readConsoleLine(prompt string) (string, error) {
	h := windows.Handle(os.Stdin.Fd())
	writeConsole(prompt)
	var buf [256]uint16
	var read uint32
	err := windows.ReadConsole(h, &buf[0], uint32(len(buf)), &read, nil)
	if err != nil {
		return "", fmt.Errorf("failed to read username: %w", err)
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:read])), nil
}

func readConsolePassword(prompt string) (string, error) {
	h := windows.Handle(os.Stdin.Fd())
	writeConsole(prompt)

	var originalMode uint32
	err := windows.GetConsoleMode(h, &originalMode)
	if err != nil {
		return "", fmt.Errorf("GetConsoleMode: %w", err)
	}
	_ = windows.SetConsoleMode(h, originalMode&^windows.ENABLE_ECHO_INPUT)

	var buf [256]uint16
	var read uint32
	err = windows.ReadConsole(h, &buf[0], uint32(len(buf)), &read, nil)

	_ = windows.SetConsoleMode(h, originalMode)
	writeConsole("\n")

	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:read])), nil
}

func writeConsole(s string) {
	h := windows.Handle(os.Stderr.Fd())
	_ = windows.WriteConsole(h, windows.StringToUTF16Ptr(s), uint32(len(s)), nil, nil)
}
