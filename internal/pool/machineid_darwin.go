//go:build darwin

package pool

import (
	"os/exec"
	"strings"
)

// readMachineID returns the IOPlatformUUID, a stable per-machine identifier, by
// parsing `ioreg`. The target line looks like:
//
//	"IOPlatformUUID" = "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
//
// Empty on any failure; the caller degrades to a fallback rather than erroring.
func readMachineID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		if _, v, ok := strings.Cut(line, "="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}
