//go:build windows

package pool

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a process that has not yet
// terminated (STILL_ACTIVE / STATUS_PENDING, 259). golang.org/x/sys/windows
// does not export a STILL_ACTIVE constant, so we define it locally.
const stillActive = 259

func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// The process exists but we lack rights to it: treat as alive,
		// mirroring the unix EPERM rule.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true
		}
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	// STILL_ACTIVE (259) can theoretically collide with a real exit code of a
	// terminated process; acceptable for a liveness heuristic.
	return code == stillActive
}

// StartDetached launches cmd in a new process group, detached from any console,
// and releases it, so it survives the parent being reaped. No console window is
// created. Stdout/Stderr are left nil.
func StartDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
