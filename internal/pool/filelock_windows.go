//go:build windows

package pool

import (
	"errors"
	"os"
)

func lockFile(f *os.File) error   { return errors.New("file lock: not implemented on windows yet") }
func unlockFile(f *os.File) error { return errors.New("file lock: not implemented on windows yet") }
