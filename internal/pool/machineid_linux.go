//go:build linux

package pool

import (
	"os"
	"strings"
)

// readMachineID returns /etc/machine-id (falling back to the D-Bus copy), the
// systemd-provisioned per-installation id. Empty if neither is readable; the
// caller degrades to a fallback rather than erroring.
func readMachineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := strings.TrimSpace(string(b)); id != "" {
				return id
			}
		}
	}
	return ""
}
