//go:build windows

package pool

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile / unlockFile mirror the flock(LOCK_EX)/flock(LOCK_UN) semantics used
// on unix (see filelock_unix.go) via LockFileEx/UnlockFileEx, following the same
// pattern as the Go toolchain's cmd/go/internal/lockedfile filelock. The
// maximal byte range is locked at offset zero (the zero-value Overlapped), and
// LockFileEx blocks (no LOCKFILE_FAIL_IMMEDIATELY) to match LOCK_EX.

func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
}
