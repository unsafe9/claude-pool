//go:build !windows

package main

import "os"

// replaceExecutable installs tmp over exe. On unix a running binary can be
// overwritten in place, so a chmod + rename is enough (same-volume rename is
// atomic, and tmp lives in exe's directory).
func replaceExecutable(tmp, exe string) error {
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, exe)
}
