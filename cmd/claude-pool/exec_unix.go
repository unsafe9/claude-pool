//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func execClaude(args []string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}
	argv := append([]string{"claude"}, args...)
	return syscall.Exec(path, argv, os.Environ())
}
