//go:build !windows

package pool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// StartDetached launches cmd in a new session (Setsid) and releases it, so it
// survives the parent being reaped. Stdout/Stderr are left nil (→ /dev/null).
func StartDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
