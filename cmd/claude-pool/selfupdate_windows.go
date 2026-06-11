//go:build windows

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// replaceExecutable installs tmp over exe on Windows. A running .exe cannot be
// deleted or overwritten, but it CAN be renamed (Go issue #21997 — the same
// trick `go build` itself uses): move the live exe aside to exe+".old", then
// rename tmp into place. Antivirus can hold transient locks on a fresh binary,
// so both renames retry with a growing backoff. If the second rename fails, the
// old exe is rolled back. The leftover ".old" is hidden best-effort and cleaned
// at the next session-start.
func replaceExecutable(tmp, exe string) error {
	old := exe + ".old"
	// A stale .old from a previous self-update would block the rename below.
	_ = os.Remove(old)

	if err := renameRetry(exe, old); err != nil {
		return fmt.Errorf("move running exe aside: %w", err)
	}
	if err := renameRetry(tmp, exe); err != nil {
		// Roll back so the binary is not left missing.
		_ = renameRetry(old, exe)
		return fmt.Errorf("install new exe: %w", err)
	}
	hideFile(old)
	return nil
}

// renameRetry retries os.Rename with a growing sleep — antivirus can hold a
// brief lock on a just-written binary.
func renameRetry(from, to string) error {
	var err error
	delay := 50 * time.Millisecond
	for i := 0; i < 5; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(delay)
		delay *= 2
	}
	return err
}

func hideFile(path string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_ = windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN)
}
