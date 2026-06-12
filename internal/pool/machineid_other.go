//go:build !darwin && !linux && !windows

package pool

// readMachineID has no source on platforms without a known machine-id; the
// caller degrades to a fallback rather than erroring, so encryption still runs.
func readMachineID() string {
	return ""
}
