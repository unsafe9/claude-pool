//go:build windows

package pool

import (
	"errors"
	"os/exec"
)

func pidAlive(pid int) bool { return false }

// StartDetached launches cmd detached from this process so it survives the
// parent being reaped.
func StartDetached(cmd *exec.Cmd) error {
	return errors.New("detached start: not implemented on windows yet")
}
