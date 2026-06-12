//go:build windows

package pool

import (
	"os/exec"
	"strings"
)

// readMachineID returns the registry MachineGuid, a stable per-install
// identifier, by parsing `reg query`. The target line looks like:
//
//	MachineGuid    REG_SZ    XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX
//
// Empty on any failure; the caller degrades to a fallback rather than erroring.
func readMachineID() string {
	out, err := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "MachineGuid") {
			continue
		}
		if i := strings.Index(line, "REG_SZ"); i >= 0 {
			return strings.TrimSpace(line[i+len("REG_SZ"):])
		}
	}
	return ""
}
