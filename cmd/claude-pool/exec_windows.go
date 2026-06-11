//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// execClaude emulates unix exec(2) on Windows: it runs claude as a child
// process with inherited stdio, then exits with claude's exit code. Like the
// unix implementation, it never returns on success, matching the unix contract.
func execClaude(args []string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil // unreachable
}
